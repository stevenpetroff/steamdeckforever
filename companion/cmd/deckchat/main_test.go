package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/hub"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/protocol"
)

func TestPairingIPPrefersPrivateIPv4(t *testing.T) {
	ips := []net.IP{net.ParseIP("fe80::1"), net.ParseIP("192.168.1.20"), net.ParseIP("10.0.0.5")}
	ip, ok := pairingIP(ips)
	if !ok || ip.String() != "192.168.1.20" {
		t.Fatalf("got %v %v", ip, ok)
	}
	ip, ok = pairingIP([]net.IP{net.ParseIP("fe80::1")})
	if !ok || ip.String() != "fe80::1" {
		t.Fatalf("v6 only: got %v %v", ip, ok)
	}
	if _, ok := pairingIP(nil); ok {
		t.Fatal("no addresses should report false")
	}
}

func TestHubFeedDeliversAndCancels(t *testing.T) {
	h := hub.New(10)
	h.Publish(protocol.Message{Text: "old"})
	history, events, cancel := hubFeed{h}.Subscribe()
	if len(history) != 1 || history[0].Text != "old" {
		t.Fatalf("history = %+v", history)
	}
	h.Publish(protocol.Message{Text: "new"})
	select {
	case ev := <-events:
		if ev.Message == nil || ev.Message.Text != "new" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	cancel()
	cancel() // idempotent
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("events not closed after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("events not closed after cancel")
	}
}

func TestCopyDirKeepsPairingFile(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(src, "DeckChat.toc"), []byte("toc"), 0o644)
	os.WriteFile(filepath.Join(src, "Pairing.lua"), []byte("from source"), 0o644)
	os.WriteFile(filepath.Join(dst, "Pairing.lua"), []byte("live"), 0o600)
	if err := copyDir(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "DeckChat.toc")); string(b) != "toc" {
		t.Fatalf("toc = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "Pairing.lua")); string(b) != "live" {
		t.Fatalf("Pairing.lua overwritten: %q", b)
	}
}

func TestSecretFileNoticesReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	f, err := newSecretFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before := f.Current().Hex()
	// Simulate `deckchat pair --reset` from another process.
	if err := os.WriteFile(path, []byte("00112233445566778899aabbccddeeff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(path, time.Now().Add(time.Hour), time.Now().Add(time.Hour))
	changed := make(chan struct{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go f.watch(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), func() { changed <- struct{}{} })
	select {
	case <-changed:
	case <-ctx.Done():
		t.Fatal("reset not noticed")
	}
	if got := f.Current().Hex(); got == before || got != "00112233445566778899aabbccddeeff" {
		t.Fatalf("secret not reloaded")
	}
}
