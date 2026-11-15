// Command deckchat bridges WoW Forever chat on a Steam Deck to a phone on the
// same local network. See docs/plans for the design.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/config"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/pairing"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/wowpath"
)

const usage = `deckchat — WoW Forever chat on your phone

Usage:
  deckchat run               run the service (systemd does this)
  deckchat doctor            check the install, WoW, keyboard and display
  deckchat pair --reset      issue a new pairing code; old phones are signed out
  deckchat send <text>       send one chat message into WoW (for testing)
  deckchat install-addon DIR copy the DeckChat add-on folder into WoW
`

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"run"}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch args[0] {
	case "run":
		err = run(ctx, log)
	case "doctor":
		err = doctor(ctx)
	case "pair":
		if len(args) != 2 || args[1] != "--reset" {
			err = errors.New("usage: deckchat pair --reset")
			break
		}
		err = pairReset()
	case "send":
		if len(args) != 2 {
			err = errors.New("usage: deckchat send <text>")
			break
		}
		err = sendOnce(ctx, args[1])
	case "install-addon":
		if len(args) != 2 {
			err = errors.New("usage: deckchat install-addon DIR")
			break
		}
		err = installAddon(args[1])
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "deckchat:", err)
		os.Exit(1)
	}
}

// paths are the per-user files the service owns.
type paths struct {
	home, config, secret string
}

func userPaths() (paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return paths{}, err
	}
	return paths{
		home:   home,
		config: config.DefaultPath(home),
		secret: filepath.Join(filepath.Dir(config.DefaultPath(home)), "secret"),
	}, nil
}

func pairReset() error {
	p, err := userPaths()
	if err != nil {
		return err
	}
	if _, err := pairing.Reset(p.secret); err != nil {
		return err
	}
	fmt.Println("New pairing code issued. In WoW, type /reload, then /deckchat pair and scan it.")
	return nil
}

func installAddon(src string) error {
	p, err := userPaths()
	if err != nil {
		return err
	}
	cfg, err := config.Load(p.config)
	if err != nil {
		return err
	}
	inst, _, err := wowpath.Resolve(p.home, cfg.WoWPath)
	if err != nil {
		return err
	}
	dst := filepath.Join(inst.AddOnsDir, "DeckChat")
	if err := copyDir(src, dst); err != nil {
		return err
	}
	fmt.Println(dst)
	return nil
}

// copyDir copies a flat directory tree, keeping an existing Pairing.lua.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Name() == pairing.LuaFileName {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

// pairingIP picks the address to put in the QR code: private IPv4 first,
// then any other bound address.
func pairingIP(ips []net.IP) (net.IP, bool) {
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil && ip4.IsPrivate() {
			return ip4, true
		}
	}
	if len(ips) > 0 {
		return ips[0], true
	}
	return nil, false
}
