// Package focus tells whether WoW is the window gamescope has focused
// (plan KTD5). The sender asks before every send and before each key.
package focus

import (
	"errors"
	"strings"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// DefaultMatch is the substring the focused window's title must contain.
const DefaultMatch = "World of Warcraft"

// Checker reads gamescope's focus properties from an Xwayland root window.
// It is safe for concurrent use. It (re)connects lazily, so a gamescope
// restart (e.g. a Desktop to Game Mode switch) costs one failed call.
type Checker struct {
	Display    string // X display, e.g. ":0"
	XAuthority string // optional Xauthority path (see DialX)
	Match      string // window title substring; DefaultMatch if empty
	// AppID, if non-zero, additionally requires GAMESCOPE_FOCUSED_APP to
	// equal it (the Steam app id of the WoW shortcut).
	AppID uint32

	mu   sync.Mutex
	conn *xgb.Conn
	root xproto.Window
	// Interned atoms; 0 = not yet known to exist.
	atomWindow, atomApp, atomNetName, atomUTF8 xproto.Atom
}

// State is one focus reading.
type State struct {
	HasApp    bool   // GAMESCOPE_FOCUSED_APP is present on the root
	App       uint32 // its value
	HasWindow bool   // GAMESCOPE_FOCUSED_WINDOW is present
	Window    uint32 // its value
	Name      string // the focused window's title ("" if none found)
	Focused   bool
}

// NewGamescope returns a checker for the given display.
func NewGamescope(display string) *Checker {
	return &Checker{Display: display}
}

// Focused reports whether the focused window's title contains Match.
// Missing gamescope atoms or properties mean (false, nil). A connection
// error is returned and the next call reconnects.
func (c *Checker) Focused() (bool, error) {
	st, err := c.State()
	return st.Focused, err
}

// Close drops the X connection.
func (c *Checker) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reset()
}

func (c *Checker) reset() {
	if c.conn != nil {
		c.conn.Close()
	}
	c.conn = nil
	c.atomWindow, c.atomApp, c.atomNetName, c.atomUTF8 = 0, 0, 0, 0
}

// State reads the current focus state.
func (c *Checker) State() (State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		conn, err := DialX(c.Display, c.XAuthority)
		if err != nil {
			return State{}, err
		}
		c.conn = conn
		c.root = xproto.Setup(conn).DefaultScreen(conn).Root
	}
	st, err := c.read()
	if err != nil && !isXProtocolError(err) {
		c.reset()
		return State{}, err
	}
	return st, nil
}

func (c *Checker) read() (State, error) {
	var st State
	var err error
	// Atoms are looked up only-if-exists: until gamescope creates them,
	// focus is simply unknown (false). Once found they stay valid for the
	// life of the connection.
	for _, a := range []struct {
		p    *xproto.Atom
		name string
	}{
		{&c.atomWindow, "GAMESCOPE_FOCUSED_WINDOW"},
		{&c.atomApp, "GAMESCOPE_FOCUSED_APP"},
		{&c.atomNetName, "_NET_WM_NAME"},
		{&c.atomUTF8, "UTF8_STRING"},
	} {
		if *a.p == 0 {
			if *a.p, err = c.atom(a.name); err != nil {
				return st, err
			}
		}
	}

	if c.atomApp != 0 {
		v, ok, err := c.cardinal(c.root, c.atomApp)
		if err != nil {
			return st, err
		}
		st.HasApp, st.App = ok, v
	}
	if c.atomWindow == 0 {
		return st, nil
	}
	v, ok, err := c.cardinal(c.root, c.atomWindow)
	if err != nil {
		return st, err
	}
	st.HasWindow, st.Window = ok, v
	if !ok || v == 0 {
		return st, nil
	}

	name, err := c.titleOf(xproto.Window(v), 2)
	if err != nil {
		if isXProtocolError(err) { // window vanished between reads
			return st, nil
		}
		return st, err
	}
	st.Name = name
	match := c.Match
	if match == "" {
		match = DefaultMatch
	}
	st.Focused = name != "" && strings.Contains(name, match)
	if c.AppID != 0 && (!st.HasApp || st.App != c.AppID) {
		st.Focused = false
	}
	return st, nil
}

func (c *Checker) atom(name string) (xproto.Atom, error) {
	r, err := xproto.InternAtom(c.conn, true, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, err
	}
	return r.Atom, nil
}

// cardinal reads the first 32-bit value of a property of any type.
func (c *Checker) cardinal(w xproto.Window, prop xproto.Atom) (uint32, bool, error) {
	r, err := xproto.GetProperty(c.conn, false, w, prop, xproto.GetPropertyTypeAny, 0, 1).Reply()
	if err != nil {
		return 0, false, err
	}
	if r.Format != 32 || len(r.Value) < 4 {
		return 0, false, nil
	}
	return xgb.Get32(r.Value), true, nil
}

// titleOf returns _NET_WM_NAME, else WM_NAME, of w. If w has neither (a
// frame or wrapper window), it searches children up to depth levels.
func (c *Checker) titleOf(w xproto.Window, depth int) (string, error) {
	if c.atomNetName != 0 {
		r, err := xproto.GetProperty(c.conn, false, w, c.atomNetName, xproto.GetPropertyTypeAny, 0, 256).Reply()
		if err != nil {
			return "", err
		}
		if r.Format == 8 && len(r.Value) > 0 {
			return string(r.Value), nil
		}
	}
	r, err := xproto.GetProperty(c.conn, false, w, xproto.AtomWmName, xproto.GetPropertyTypeAny, 0, 256).Reply()
	if err != nil {
		return "", err
	}
	if r.Format == 8 && len(r.Value) > 0 {
		return string(r.Value), nil
	}
	if depth == 0 {
		return "", nil
	}
	tree, err := xproto.QueryTree(c.conn, w).Reply()
	if err != nil {
		return "", err
	}
	for _, ch := range tree.Children {
		name, err := c.titleOf(ch, depth-1)
		if err != nil && !isXProtocolError(err) {
			return "", err
		}
		if name != "" {
			return name, nil
		}
	}
	return "", nil
}

// isXProtocolError tells X error replies (BadWindow and friends, which
// leave the connection usable) from transport errors.
func isXProtocolError(err error) bool {
	if err == nil {
		return false
	}
	var xe xgb.Error
	return errors.As(err, &xe)
}
