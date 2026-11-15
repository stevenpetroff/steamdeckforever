package chatlog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/protocol"
)

func line(n int) string { return fmt.Sprintf("11/14 18:00:%02d.000  [Bob] says: msg %d\n", n%60, n) }

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

type tailRun struct {
	ch   chan protocol.Message
	done chan error
	stop context.CancelFunc
}

func startTail(t *testing.T, path string, seed int) *tailRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &tailRun{ch: make(chan protocol.Message, 1000), done: make(chan error, 1), stop: cancel}
	go func() {
		r.done <- Tail(ctx, path, TailOptions{Poll: 10 * time.Millisecond, SeedLines: seed}, func(m protocol.Message) { r.ch <- m })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(2 * time.Second):
			t.Error("Tail did not return after cancel")
		}
	})
	return r
}

func (r *tailRun) expect(t *testing.T, texts ...string) {
	t.Helper()
	for _, want := range texts {
		select {
		case m := <-r.ch:
			if m.Text != want {
				t.Fatalf("got text %q, want %q", m.Text, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
}

func (r *tailRun) expectNone(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case m := <-r.ch:
		t.Fatalf("unexpected emit %+v", m)
	case <-time.After(d):
	}
}

func msgs(from, to int) []string {
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, fmt.Sprintf("msg %d", i))
	}
	return out
}

func TestTailSeedsLastLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "WoWChatLog.txt")
	s := ""
	for i := 1; i <= 50; i++ {
		s += line(i)
	}
	appendTo(t, path, s)
	r := startTail(t, path, 5)
	r.expect(t, msgs(46, 50)...)
	r.expectNone(t, 100*time.Millisecond)
}

func TestTailSeedLargerThanFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "WoWChatLog.txt")
	appendTo(t, path, line(1)+"\n"+line(2))
	r := startTail(t, path, 0) // default 200
	r.expect(t, "msg 1", "msg 2")
	r.expectNone(t, 100*time.Millisecond)
}

func TestTailFollowsAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "WoWChatLog.txt")
	appendTo(t, path, line(1))
	r := startTail(t, path, 200)
	r.expect(t, "msg 1")
	appendTo(t, path, line(2)+line(3)+line(4))
	r.expect(t, msgs(2, 4)...)
}

func TestTailBuffersPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "WoWChatLog.txt")
	appendTo(t, path, line(1))
	r := startTail(t, path, 200)
	r.expect(t, "msg 1")
	full := line(2)
	appendTo(t, path, full[:20])
	r.expectNone(t, 150*time.Millisecond)
	appendTo(t, path, full[20:])
	r.expect(t, "msg 2")
}

func TestTailSeedWithTrailingPartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "WoWChatLog.txt")
	full := line(2)
	appendTo(t, path, line(1)+full[:15])
	r := startTail(t, path, 200)
	r.expect(t, "msg 1")
	r.expectNone(t, 100*time.Millisecond)
	appendTo(t, path, full[15:])
	r.expect(t, "msg 2")
}

func TestTailTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "WoWChatLog.txt")
	appendTo(t, path, line(1)+line(2)+line(3))
	r := startTail(t, path, 200)
	r.expect(t, msgs(1, 3)...)
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	appendTo(t, path, line(10)+line(11))
	r.expect(t, "msg 10", "msg 11")
	r.expectNone(t, 100*time.Millisecond)
}

func TestTailReplacedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WoWChatLog.txt")
	appendTo(t, path, line(1)+line(2)+line(3)+line(4))
	r := startTail(t, path, 200)
	r.expect(t, msgs(1, 4)...)
	tmp := filepath.Join(dir, "new.txt")
	appendTo(t, tmp, line(20)+line(21)+line(22)+line(23)+line(24)+line(25))
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	r.expect(t, msgs(20, 25)...)
	r.expectNone(t, 100*time.Millisecond)
}

func TestTailFileCreatedLater(t *testing.T) {
	path := filepath.Join(t.TempDir(), "WoWChatLog.txt")
	r := startTail(t, path, 200)
	r.expectNone(t, 80*time.Millisecond)
	appendTo(t, path, line(1)+line(2))
	r.expect(t, "msg 1", "msg 2")
}

func TestTailCancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "WoWChatLog.txt")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Tail(ctx, path, TailOptions{}, func(protocol.Message) {}) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Tail returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Tail did not return after cancel")
	}
}
