//go:build linux && linuxintegration

// Integration tests for the real uinput keyboard and X11 clipboard. Run in
// Docker via test/linux/run.sh: needs --privileged (/dev/uinput) and an X
// server on $DISPLAY (Xvfb).
package input

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"golang.org/x/sys/unix"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/protocol"
)

var devSeq atomic.Int32

func uniqueName() string {
	return fmt.Sprintf("DeckChat Test %d-%d", os.Getpid(), devSeq.Add(1))
}

// openEventNode finds the evdev node for a device name via sysfs and opens
// it. Docker's /dev is populated at container start, so a device created
// later may have no /dev/input node; then we mknod one from sysfs.
func openEventNode(t *testing.T, name string) *os.File {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		dirs, _ := filepath.Glob("/sys/class/input/event*")
		for _, d := range dirs {
			b, err := os.ReadFile(filepath.Join(d, "device", "name"))
			if err != nil || strings.TrimSpace(string(b)) != name {
				continue
			}
			node := filepath.Join("/dev/input", filepath.Base(d))
			if _, err := os.Stat(node); err != nil {
				devb, err := os.ReadFile(filepath.Join(d, "dev"))
				if err != nil {
					t.Fatal(err)
				}
				mm := strings.Split(strings.TrimSpace(string(devb)), ":")
				maj, _ := strconv.Atoi(mm[0])
				min, _ := strconv.Atoi(mm[1])
				node = filepath.Join(t.TempDir(), filepath.Base(d))
				if err := unix.Mknod(node, unix.S_IFCHR|0o600, int(unix.Mkdev(uint32(maj), uint32(min)))); err != nil {
					t.Fatalf("mknod %s: %v", node, err)
				}
			}
			f, err := os.OpenFile(node, os.O_RDONLY|unix.O_NONBLOCK, 0)
			if err != nil {
				t.Fatalf("open %s: %v", node, err)
			}
			t.Logf("device %q is %s", name, node)
			return f
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no /sys/class/input/event* named %q", name)
	return nil
}

// readEvents reads input events until n arrive or the timeout passes.
func readEvents(t *testing.T, f *os.File, n int, timeout time.Duration) []InputEvent {
	t.Helper()
	var out []InputEvent
	buf := make([]byte, inputEventSize*64)
	f.SetReadDeadline(time.Now().Add(timeout))
	for len(out) < n {
		k, err := f.Read(buf)
		for i := 0; i+inputEventSize <= k; i += inputEventSize {
			ev, _ := ParseInputEvent(buf[i : i+inputEventSize])
			out = append(out, ev)
		}
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				break
			}
			t.Fatalf("read: %v", err)
		}
	}
	return out
}

func TestUinputPressCtrlV(t *testing.T) {
	name := uniqueName()
	kb, err := OpenUinputWith(UinputOptions{Name: name, KeyDelay: 2 * time.Millisecond, CreateSleep: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer kb.Close()
	f := openEventNode(t, name)
	defer f.Close()

	if err := kb.Press(KeyLeftCtrl, KeyV); err != nil {
		t.Fatal(err)
	}
	got := describe(readEvents(t, f, 8, 2*time.Second))
	want := []string{"down:LEFTCTRL", "syn", "down:V", "syn", "up:V", "syn", "up:LEFTCTRL", "syn"}
	if !eqStrings(got, want) {
		t.Fatalf("events = %v\nwant     %v", got, want)
	}
	// Nothing else arrives (no autorepeat, no stray keys).
	if extra := readEvents(t, f, 1, 200*time.Millisecond); len(extra) != 0 {
		t.Fatalf("extra events: %v", describe(extra))
	}
}

func TestUinputDeviceAdvertisesOnlyOurKeys(t *testing.T) {
	name := uniqueName()
	kb, err := OpenUinputWith(UinputOptions{Name: name, CreateSleep: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer kb.Close()
	f := openEventNode(t, name)
	defer f.Close()
	// /sys/class/input/inputN/capabilities/key is a hex bitmap; check via
	// the event node's parent.
	dirs, _ := filepath.Glob("/sys/class/input/input*/name")
	for _, d := range dirs {
		b, _ := os.ReadFile(d)
		if strings.TrimSpace(string(b)) != name {
			continue
		}
		caps, _ := os.ReadFile(filepath.Join(filepath.Dir(d), "capabilities", "key"))
		t.Logf("key capabilities: %s", strings.TrimSpace(string(caps)))
		if got := countBits(string(caps)); got != len(AllKeys) {
			t.Fatalf("device advertises %d keys, want %d", got, len(AllKeys))
		}
		return
	}
	t.Fatal("input device not found in sysfs")
}

func countBits(hexWords string) int {
	n := 0
	for _, w := range strings.Fields(hexWords) {
		v, _ := strconv.ParseUint(w, 16, 64)
		for ; v != 0; v &= v - 1 {
			n++
		}
	}
	return n
}

func newTestClipboard(t *testing.T) *X11Clipboard {
	t.Helper()
	c, err := NewX11Clipboard(os.Getenv("DISPLAY"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// convertAs is a third, independent X client pasting CLIPBOARD as target.
func convertAs(t *testing.T, target string) (string, xproto.Atom) {
	t.Helper()
	conn, err := xgb.NewConnDisplay(os.Getenv("DISPLAY"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	atom := func(n string) xproto.Atom {
		r, err := xproto.InternAtom(conn, false, uint16(len(n)), n).Reply()
		if err != nil {
			t.Fatal(err)
		}
		return r.Atom
	}
	win, _ := xproto.NewWindowId(conn)
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	if err := xproto.CreateWindowChecked(conn, 0, win, root, 0, 0, 1, 1, 0, xproto.WindowClassInputOnly, 0, 0, nil).Check(); err != nil {
		t.Fatal(err)
	}
	prop := atom("THIRD_CLIENT_PASTE")
	xproto.ConvertSelection(conn, win, atom("CLIPBOARD"), atom(target), prop, xproto.TimeCurrentTime)
	evc := make(chan xproto.SelectionNotifyEvent, 1)
	go func() {
		for {
			ev, xerr := conn.WaitForEvent()
			if ev == nil && xerr == nil {
				return
			}
			if e, ok := ev.(xproto.SelectionNotifyEvent); ok {
				evc <- e
				return
			}
		}
	}()
	select {
	case e := <-evc:
		if e.Property == xproto.AtomNone {
			t.Fatalf("conversion to %s refused", target)
		}
		p, err := xproto.GetProperty(conn, true, win, prop, xproto.GetPropertyTypeAny, 0, 1<<16).Reply()
		if err != nil {
			t.Fatal(err)
		}
		return string(p.Value), p.Type
	case <-time.After(2 * time.Second):
		t.Fatal("no SelectionNotify")
	}
	return "", 0
}

func TestX11ClipboardRoundTrip(t *testing.T) {
	c := newTestClipboard(t)
	text := "héllo 🙂"
	if err := c.Set(text); err != nil {
		t.Fatal(err)
	}
	got, err := c.ReadBack()
	if err != nil || got != text {
		t.Fatalf("ReadBack = %q, %v", got, err)
	}
	if got, _ := convertAs(t, "UTF8_STRING"); got != text {
		t.Fatalf("third client UTF8_STRING = %q", got)
	}
	if got, _ := convertAs(t, "TEXT"); got != text {
		t.Fatalf("third client TEXT = %q", got)
	}
	if got, _ := convertAs(t, "STRING"); got != "h\xe9llo ?" {
		t.Fatalf("third client STRING = %q", got)
	}
	targets, typ := convertAs(t, "TARGETS")
	if typ != xproto.AtomAtom || len(targets) != 16 {
		t.Fatalf("TARGETS type=%d len=%d", typ, len(targets))
	}
}

func TestX11ClipboardSetTwice(t *testing.T) {
	c := newTestClipboard(t)
	if err := c.Set("first"); err != nil {
		t.Fatal(err)
	}
	if err := c.Set("second"); err != nil {
		t.Fatal(err)
	}
	if got, err := c.ReadBack(); err != nil || got != "second" {
		t.Fatalf("ReadBack = %q, %v", got, err)
	}
	if got, _ := convertAs(t, "UTF8_STRING"); got != "second" {
		t.Fatalf("third client = %q", got)
	}
}

func TestX11ClipboardTakesOverFromAnotherOwner(t *testing.T) {
	a := newTestClipboard(t)
	b := newTestClipboard(t)
	a.Set("from a")
	b.Set("from b")
	if got, _ := a.ReadBack(); got != "from b" {
		t.Fatalf("after b took over, a reads %q", got)
	}
	a.Set("from a again")
	if got, _ := b.ReadBack(); got != "from a again" {
		t.Fatalf("after a re-asserted, b reads %q", got)
	}
}

func TestX11ClipboardReconnects(t *testing.T) {
	c := newTestClipboard(t)
	if err := c.Set("before"); err != nil {
		t.Fatal(err)
	}
	// Kill the owner connection behind the clipboard's back, as a
	// gamescope restart would.
	c.mu.Lock()
	o := c.owner
	c.mu.Unlock()
	o.conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for !o.dead.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := c.Set("during"); err == nil {
		t.Fatal("Set on a dead connection succeeded")
	}
	if err := c.Set("after"); err != nil {
		t.Fatalf("Set after reconnect: %v", err)
	}
	if got, err := c.ReadBack(); err != nil || got != "after" {
		t.Fatalf("ReadBack = %q, %v", got, err)
	}
}

type alwaysFocused struct{}

func (alwaysFocused) Focused() (bool, error) { return true, nil }

// End to end: real uinput keyboard, real X clipboard, fake focus.
func TestSenderEndToEnd(t *testing.T) {
	name := uniqueName()
	clip := newTestClipboard(t)
	open := func() (Keyboard, error) {
		return OpenUinputWith(UinputOptions{Name: name, KeyDelay: 2 * time.Millisecond, CreateSleep: 50 * time.Millisecond})
	}
	s := NewSender(open, clip, alwaysFocused{}, Options{})
	defer s.Close()
	if !s.Available() { // creates the device so we can open its node first
		t.Fatal("keyboard unavailable")
	}
	f := openEventNode(t, name)
	defer f.Close()

	text := "héllo 🙂"
	if got := s.Send(context.Background(), "e2e", text); got != (protocol.SendResult{ID: "e2e", Outcome: protocol.OutcomeSent}) {
		t.Fatalf("Send = %+v", got)
	}
	var keys []string
	for _, e := range describe(readEvents(t, f, 28, 2*time.Second)) {
		if e != "syn" {
			keys = append(keys, e)
		}
	}
	if !eqStrings(keys, fullSequence) {
		t.Fatalf("keys = %v\nwant   %v", keys, fullSequence)
	}
	if got, _ := convertAs(t, "UTF8_STRING"); got != text {
		t.Fatalf("clipboard = %q", got)
	}
	// Same id again: replayed, no new key events.
	s.Send(context.Background(), "e2e", text)
	if extra := readEvents(t, f, 1, 300*time.Millisecond); len(extra) != 0 {
		t.Fatalf("duplicate id produced events: %v", describe(extra))
	}
}

func TestX11ClipboardWithXauthority(t *testing.T) {
	display, file := os.Getenv("DECKCHAT_AUTH_DISPLAY"), os.Getenv("DECKCHAT_AUTH_FILE")
	if display == "" || file == "" {
		t.Skip("DECKCHAT_AUTH_DISPLAY/FILE not set")
	}
	c, err := NewX11ClipboardAuth(display, file)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Set("/g hi"); err != nil {
		t.Fatal(err)
	}
	if got, err := c.ReadBack(); err != nil || got != "/g hi" {
		t.Fatalf("ReadBack = %q, %v", got, err)
	}
}
