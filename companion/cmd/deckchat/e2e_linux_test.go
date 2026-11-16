//go:build linux && linuxintegration

// End-to-end test of the whole service against Xvfb (standing in for
// gamescope's Xwayland), a real /dev/uinput keyboard and a fake WoW Forever
// install. Run via test/linux/run.sh.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/pairing"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/server"
)

func TestServiceEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".local/share/Steam/steamapps/compatdata/4242/pfx/drive_c/Program Files (x86)/World of Warcraft/_forever_")
	addon := filepath.Join(root, "Interface/AddOns/DeckChat")
	logPath := filepath.Join(root, "Logs/WoWChatLog.txt")
	for _, d := range []string{addon, filepath.Dir(logPath)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(addon, "DeckChat.toc"), []byte("## Title: DeckChat\n"), 0o644)
	os.WriteFile(logPath, []byte("11/14 18:00:00.000  [Guild] [Bob]: hello from before\n"), 0o644)

	focusWoW(t, os.Getenv("DISPLAY"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	url := waitPairingURL(t, filepath.Join(addon, pairing.LuaFileName))
	m := regexp.MustCompile(`^http://([^/]+)/#([0-9a-f]{32})$`).FindStringSubmatch(url)
	if m == nil {
		t.Fatalf("unexpected pairing url shape (redacted): %d chars", len(url))
	}
	host, secretHex := m[1], m[2]
	secret, err := pairing.ParseHex(secretHex)
	if err != nil {
		t.Fatal(err)
	}

	cctx, ccancel := context.WithTimeout(ctx, 20*time.Second)
	defer ccancel()
	conn, _, err := websocket.Dial(cctx, "ws://"+host+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	var challenge struct{ Type, Nonce string }
	readJSON(t, cctx, conn, &challenge)
	if challenge.Type != "challenge" {
		t.Fatalf("first frame %q, want challenge", challenge.Type)
	}
	writeJSON(t, cctx, conn, map[string]any{"type": "auth", "mac": server.AuthMAC(secret.Bytes(), challenge.Nonce)})

	var history struct {
		Type     string
		Messages []struct{ Channel, Sender, Text string }
	}
	readJSON(t, cctx, conn, &history)
	if history.Type != "history" || len(history.Messages) != 1 || history.Messages[0].Text != "hello from before" {
		t.Fatalf("history = %+v", history)
	}

	f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	fmt.Fprintln(f, "11/14 18:00:05.000  [Thrall-Doomhowl] whispers: you there?")
	f.Close()

	ts := time.Now().UnixMilli()
	text := "/w Thrall-Doomhowl yes — héllo 🙂"
	writeJSON(t, cctx, conn, map[string]any{
		"type": "send", "id": "e2e1", "ts": ts, "text": text,
		"mac": server.SendMAC(secret.Bytes(), challenge.Nonce, "e2e1", ts, text),
	})

	var gotWhisper, gotSent bool
	for !(gotWhisper && gotSent) {
		var frame struct {
			Type, ID, Outcome, Reason string
			Message                   struct{ Channel, Sender, SenderRealm, Text string } `json:"message"`
		}
		readJSON(t, cctx, conn, &frame)
		switch frame.Type {
		case "message":
			if frame.Message.Channel == "whisper" && frame.Message.Sender == "Thrall" && frame.Message.Text == "you there?" {
				gotWhisper = true
			}
		case "send_result":
			if frame.ID != "e2e1" || frame.Outcome != "sent" {
				t.Fatalf("send_result = %+v", frame)
			}
			gotSent = true
		}
	}
}

// focusWoW makes the Xvfb root look like gamescope with WoW focused.
func focusWoW(t *testing.T, display string) {
	t.Helper()
	conn, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	w, _ := xproto.NewWindowId(conn)
	if err := xproto.CreateWindowChecked(conn, 0, w, root, 0, 0, 10, 10, 0, xproto.WindowClassInputOutput, 0, 0, nil).Check(); err != nil {
		t.Fatal(err)
	}
	name := "World of Warcraft"
	xproto.ChangePropertyChecked(conn, xproto.PropModeReplace, w, xproto.AtomWmName, xproto.AtomString, 8, uint32(len(name)), []byte(name)).Check()
	set := func(prop string, v uint32) {
		a, err := xproto.InternAtom(conn, false, uint16(len(prop)), prop).Reply()
		if err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 4)
		xgb.Put32(b, v)
		if err := xproto.ChangePropertyChecked(conn, xproto.PropModeReplace, root, a.Atom, xproto.AtomCardinal, 32, 1, b).Check(); err != nil {
			t.Fatal(err)
		}
	}
	set("GAMESCOPE_FOCUSED_APP", 4242)
	set("GAMESCOPE_FOCUSED_WINDOW", uint32(w))
}

func waitPairingURL(t *testing.T, path string) string {
	t.Helper()
	re := regexp.MustCompile(`url = "([^"]+)"`)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			if m := re.FindSubmatch(b); m != nil {
				return strings.ReplaceAll(string(m[1]), `\/`, "/")
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("Pairing.lua was not written")
	return ""
}

func readJSON(t *testing.T, ctx context.Context, c *websocket.Conn, v any) {
	t.Helper()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
}

func writeJSON(t *testing.T, ctx context.Context, c *websocket.Conn, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}
