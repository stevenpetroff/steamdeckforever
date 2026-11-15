package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/chatlog"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/config"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/hub"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/input"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/pairing"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/protocol"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/server"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/wowpath"
)

// staleAfter is how long without any new chat line or log write before the
// phone shows "chat log is stale".
const staleAfter = 10 * time.Minute

func run(ctx context.Context, log *slog.Logger) error {
	p, err := userPaths()
	if err != nil {
		return err
	}
	cfg, err := config.Load(p.config)
	if err != nil {
		return err
	}
	secrets, err := newSecretFile(p.secret)
	if err != nil {
		return err
	}

	inst, err := waitForInstall(ctx, log, p.home, cfg.WoWPath)
	if err != nil {
		return err
	}
	log.Info("using WoW Forever install", "root", inst.Root)

	h := hub.New(500)
	go func() {
		err := chatlog.Tail(ctx, inst.LogPath, chatlog.TailOptions{}, func(m protocol.Message) { h.Publish(m) })
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Error("chat log reader stopped", "err", err)
		}
	}()

	game := newGameDisplay()
	sender := input.NewSender(input.OpenUinput, game, game, input.Options{})
	defer sender.Close()

	srv := server.New(server.Options{
		Feed:   hubFeed{h},
		Sender: sender,
		Status: &statusSource{hub: h, game: game, sender: sender, logPath: inst.LogPath},
		Secret: secrets,
		Static: server.WebFS(),
		Port:   cfg.Port,
		Logger: log,
	})

	pair := &pairWriter{addonDir: filepath.Join(inst.AddOnsDir, "DeckChat"), port: cfg.Port, secrets: secrets, log: log}
	go secrets.watch(ctx, log, func() {
		srv.DisconnectAll()
		pair.rewrite()
	})

	ls := &server.Listeners{Port: cfg.Port, Logger: log, OnChange: pair.setIPs}
	return ls.Run(ctx, srv.Handler())
}

func waitForInstall(ctx context.Context, log *slog.Logger, home, override string) (wowpath.Install, error) {
	for {
		inst, all, err := wowpath.Resolve(home, override)
		if err == nil {
			for _, other := range all[1:] {
				log.Info("also found WoW Forever install (not used)", "root", other.Root)
			}
			return inst, nil
		}
		log.Warn("waiting for WoW Forever install", "err", err)
		select {
		case <-ctx.Done():
			return wowpath.Install{}, ctx.Err()
		case <-time.After(30 * time.Second):
		}
	}
}

// hubFeed adapts the hub to server.Feed.
type hubFeed struct{ h *hub.Hub }

func (f hubFeed) Snapshot() []protocol.Message { return f.h.Snapshot() }

func (f hubFeed) Subscribe() ([]protocol.Message, <-chan server.FeedEvent, func()) {
	history, sub := f.h.Subscribe(256)
	out := make(chan server.FeedEvent)
	done := make(chan struct{})
	go func() {
		defer close(out)
		for {
			select {
			case <-done:
				return
			case ev, ok := <-sub.C():
				if !ok {
					return
				}
				select {
				case out <- server.FeedEvent{Message: ev.Message, Resync: ev.Resync}:
				case <-done:
					return
				}
			}
		}
	}()
	var once sync.Once
	return history, out, func() {
		once.Do(func() {
			close(done)
			sub.Close()
		})
	}
}

// statusSource builds the phone status line.
type statusSource struct {
	hub     *hub.Hub
	game    *gameDisplay
	sender  *input.Sender
	logPath string
}

func (s *statusSource) Status() protocol.Status {
	focused, _ := s.game.Focused()
	last := s.hub.LastAt()
	if fi, err := os.Stat(s.logPath); err == nil && fi.ModTime().After(last) {
		last = fi.ModTime()
	}
	st := protocol.Status{
		WoWFocused:     focused,
		LogFresh:       !last.IsZero() && time.Since(last) < staleAfter,
		InputAvailable: s.sender.Available(),
		// Whether the log records instance chat is unverified (plan Risks);
		// until it's checked on the Deck this is never reported.
		InstancePaused: false,
	}
	if !last.IsZero() {
		st.LastLogAt = last.UTC().Format(time.RFC3339)
	}
	return st
}

// secretFile serves the current pairing secret and notices `deckchat pair
// --reset` run from another process.
type secretFile struct {
	path string
	mu   sync.Mutex
	cur  pairing.Secret
	mod  time.Time
}

func newSecretFile(path string) (*secretFile, error) {
	s, err := pairing.LoadOrCreate(path)
	if err != nil {
		return nil, err
	}
	f := &secretFile{path: path, cur: s}
	if fi, err := os.Stat(path); err == nil {
		f.mod = fi.ModTime()
	}
	return f, nil
}

func (f *secretFile) Current() pairing.Secret {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cur
}

func (f *secretFile) watch(ctx context.Context, log *slog.Logger, changed func()) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		fi, err := os.Stat(f.path)
		if err != nil {
			continue
		}
		f.mu.Lock()
		same := fi.ModTime().Equal(f.mod)
		f.mu.Unlock()
		if same {
			continue
		}
		s, err := pairing.LoadOrCreate(f.path)
		if err != nil {
			log.Error("reading pairing secret", "err", err)
			continue
		}
		f.mu.Lock()
		f.cur, f.mod = s, fi.ModTime()
		f.mu.Unlock()
		log.Info("pairing code changed; signing out connected phones")
		changed()
	}
}

// pairWriter keeps Pairing.lua in the add-on folder pointing at a bound
// address with the current secret.
type pairWriter struct {
	addonDir string
	port     int
	secrets  *secretFile
	log      *slog.Logger

	mu sync.Mutex
	ip net.IP
}

func (w *pairWriter) setIPs(ips []net.IP) {
	ip, ok := pairingIP(ips)
	w.mu.Lock()
	if ok {
		w.ip = ip
	}
	w.mu.Unlock()
	if !ok {
		w.log.Warn("no local network address yet; waiting for Wi-Fi")
		return
	}
	w.rewrite()
}

func (w *pairWriter) rewrite() {
	w.mu.Lock()
	ip := w.ip
	w.mu.Unlock()
	if ip == nil {
		return
	}
	if _, err := os.Stat(w.addonDir); err != nil {
		w.log.Warn("DeckChat add-on folder missing; re-run the installer", "dir", w.addonDir)
		return
	}
	url := pairing.URL(ip.String(), w.port, w.secrets.Current())
	if err := pairing.WriteLua(w.addonDir, url); err != nil {
		w.log.Error("writing Pairing.lua", "err", err)
		return
	}
	w.log.Info("pairing code updated", "ip", ip.String())
}

func sendOnce(ctx context.Context, text string) error {
	game := newGameDisplay()
	sender := input.NewSender(input.OpenUinput, game, game, input.Options{})
	defer sender.Close()
	res := sender.Send(ctx, fmt.Sprintf("cli-%d", time.Now().UnixNano()), text)
	if res.Outcome != protocol.OutcomeSent {
		return fmt.Errorf("not sent: %s", res.Reason)
	}
	fmt.Println("sent")
	return nil
}
