//go:build linux && linuxintegration

// Integration tests against Xvfb, emulating gamescope's root-window focus
// properties. Run via test/linux/run.sh.
package focus

import (
	"os"
	"testing"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

type xfix struct {
	t    *testing.T
	conn *xgb.Conn
	root xproto.Window
}

func newXFix(t *testing.T, display string) *xfix {
	t.Helper()
	conn, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	return &xfix{t: t, conn: conn, root: xproto.Setup(conn).DefaultScreen(conn).Root}
}

func (x *xfix) atom(name string) xproto.Atom {
	r, err := xproto.InternAtom(x.conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		x.t.Fatal(err)
	}
	return r.Atom
}

func (x *xfix) window(name string) xproto.Window {
	w, _ := xproto.NewWindowId(x.conn)
	if err := xproto.CreateWindowChecked(x.conn, 0, w, x.root, 0, 0, 10, 10, 0, xproto.WindowClassInputOutput, 0, 0, nil).Check(); err != nil {
		x.t.Fatal(err)
	}
	if name != "" {
		x.setName(w, name)
	}
	return w
}

func (x *xfix) setName(w xproto.Window, name string) {
	if err := xproto.ChangePropertyChecked(x.conn, xproto.PropModeReplace, w, xproto.AtomWmName, xproto.AtomString, 8, uint32(len(name)), []byte(name)).Check(); err != nil {
		x.t.Fatal(err)
	}
}

func (x *xfix) setCardinal(w xproto.Window, prop string, v uint32) {
	b := make([]byte, 4)
	xgb.Put32(b, v)
	if err := xproto.ChangePropertyChecked(x.conn, xproto.PropModeReplace, w, x.atom(prop), xproto.AtomCardinal, 32, 1, b).Check(); err != nil {
		x.t.Fatal(err)
	}
}

func (x *xfix) deleteProp(w xproto.Window, prop string) {
	xproto.DeletePropertyChecked(x.conn, w, x.atom(prop)).Check()
}

func mustFocused(t *testing.T, c *Checker, want bool) State {
	t.Helper()
	st, err := c.State()
	if err != nil {
		t.Fatal(err)
	}
	if st.Focused != want {
		t.Fatalf("Focused = %v, want %v (state %+v)", st.Focused, want, st)
	}
	return st
}

// The atoms have never been interned on this fresh server.
func TestAtomsAbsent(t *testing.T) {
	display := os.Getenv("DECKCHAT_FRESH_DISPLAY")
	if display == "" {
		t.Skip("DECKCHAT_FRESH_DISPLAY not set")
	}
	c := NewGamescope(display)
	defer c.Close()
	st := mustFocused(t, c, false)
	if st.HasApp || st.HasWindow {
		t.Fatalf("state %+v", st)
	}
	if probeFocusedApp(Candidate{Display: display}) {
		t.Fatal("probe true without GAMESCOPE_FOCUSED_APP")
	}
}

func TestGamescopeFocus(t *testing.T) {
	display := os.Getenv("DISPLAY")
	x := newXFix(t, display)
	c := NewGamescope(display)
	defer c.Close()

	// Atoms exist (interned) but the properties are absent.
	x.atom("GAMESCOPE_FOCUSED_WINDOW")
	x.atom("GAMESCOPE_FOCUSED_APP")
	x.deleteProp(x.root, "GAMESCOPE_FOCUSED_WINDOW")
	x.deleteProp(x.root, "GAMESCOPE_FOCUSED_APP")
	mustFocused(t, c, false)

	wow := x.window("World of Warcraft")
	x.setCardinal(x.root, "GAMESCOPE_FOCUSED_APP", 4242)
	x.setCardinal(x.root, "GAMESCOPE_FOCUSED_WINDOW", uint32(wow))
	st := mustFocused(t, c, true)
	if !st.HasApp || st.App != 4242 || st.Name != "World of Warcraft" {
		t.Fatalf("state %+v", st)
	}

	x.setName(wow, "Steam")
	mustFocused(t, c, false)
	// A browser tab about WoW must never receive a paste.
	x.setName(wow, "World of Warcraft - Wowhead - Mozilla Firefox")
	mustFocused(t, c, false)
	x.setName(wow, "World of Warcraft")
	mustFocused(t, c, true)

	// Focus moves to another window.
	other := x.window("Steam Big Picture")
	x.setCardinal(x.root, "GAMESCOPE_FOCUSED_WINDOW", uint32(other))
	mustFocused(t, c, false)

	// Title on a child window (wrapper/frame case).
	frame := x.window("")
	child, _ := xproto.NewWindowId(x.conn)
	xproto.CreateWindowChecked(x.conn, 0, child, frame, 0, 0, 5, 5, 0, xproto.WindowClassInputOutput, 0, 0, nil).Check()
	x.setName(child, "World of Warcraft")
	x.setCardinal(x.root, "GAMESCOPE_FOCUSED_WINDOW", uint32(frame))
	mustFocused(t, c, true)

	// Stale window id (BadWindow) is not focused and not an error.
	x.setCardinal(x.root, "GAMESCOPE_FOCUSED_WINDOW", 0x7ffffff0)
	mustFocused(t, c, false)

	// AppID filter.
	x.setCardinal(x.root, "GAMESCOPE_FOCUSED_WINDOW", uint32(wow))
	c.AppID = 1
	mustFocused(t, c, false)
	c.AppID = 4242
	mustFocused(t, c, true)
	c.AppID = 0

	// Custom title: matched exactly, not as a substring.
	c.Match = "Warcraft"
	mustFocused(t, c, false)
	c.Match = "World of Warcraft"
	mustFocused(t, c, true)
	c.Match = "Diablo"
	mustFocused(t, c, false)
	c.Match = ""

	if !probeFocusedApp(Candidate{Display: display}) {
		t.Fatal("probe false with GAMESCOPE_FOCUSED_APP set")
	}
	ranked := rankCandidates([]Candidate{{Display: os.Getenv("DECKCHAT_FRESH_DISPLAY")}, {Display: display}}, probeFocusedApp)
	if ranked[0].Display != display {
		t.Fatalf("ranked %+v", ranked)
	}

	// Window properties absent again -> false.
	x.deleteProp(x.root, "GAMESCOPE_FOCUSED_WINDOW")
	mustFocused(t, c, false)
}

func TestCheckerReconnects(t *testing.T) {
	display := os.Getenv("DISPLAY")
	c := NewGamescope(display)
	defer c.Close()
	if _, err := c.State(); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.conn.Close()
	c.mu.Unlock()
	// The dead connection errors once, then the checker reconnects.
	var errs int
	for range 3 {
		if _, err := c.State(); err != nil {
			errs++
		}
	}
	if errs > 1 {
		t.Fatalf("%d errors after connection loss, want at most 1", errs)
	}
	if _, err := c.State(); err != nil {
		t.Fatalf("still failing: %v", err)
	}
}

func TestDialXWithXauthority(t *testing.T) {
	display, file := os.Getenv("DECKCHAT_AUTH_DISPLAY"), os.Getenv("DECKCHAT_AUTH_FILE")
	if display == "" || file == "" {
		t.Skip("DECKCHAT_AUTH_DISPLAY/FILE not set")
	}
	t.Setenv("XAUTHORITY", "/nonexistent")
	if conn, err := DialX(display, ""); err == nil {
		conn.Close()
		t.Fatal("connected to an auth-protected server without a cookie")
	}
	conn, err := DialX(display, file)
	if err != nil {
		t.Fatalf("DialX with xauthority: %v", err)
	}
	conn.Close()
	c := NewGamescope(display)
	c.XAuthority = file
	defer c.Close()
	if _, err := c.Focused(); err != nil {
		t.Fatal(err)
	}
}
