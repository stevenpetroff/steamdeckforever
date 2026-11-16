package input

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/focus"
)

// X11Clipboard owns CLIPBOARD on an X display (gamescope's Xwayland on the
// Deck) and serves the text to whoever pastes. It is pure Go (xgb), so it
// builds everywhere but needs an X server to do anything.
//
// It uses two X connections: an owner that holds the selection and answers
// SelectionRequest events from a goroutine, and a separate reader that
// converts the selection like any other client on the display would. The
// reader is the "read back on the game's display" check of KTD3.
type X11Clipboard struct {
	display    string
	xauthority string
	// ReadTimeout bounds one ReadBack round trip (default 200ms).
	ReadTimeout time.Duration

	mu    sync.Mutex // serializes Set and owner (re)connects
	owner *ownerConn

	rmu    sync.Mutex // serializes ReadBack
	reader *readerConn
}

type clipAtoms struct {
	clipboard, targets, utf8, text, prop xproto.Atom
}

type ownerConn struct {
	conn  *xgb.Conn
	win   xproto.Window
	atoms clipAtoms
	dead  atomic.Bool
	text  atomic.Pointer[string]
}

type readerConn struct {
	conn   *xgb.Conn
	win    xproto.Window
	atoms  clipAtoms
	notify chan xproto.SelectionNotifyEvent // closed when the connection dies
}

var errClipboardLost = errors.New("clipboard: X connection lost; reconnecting on next call")

// NewX11Clipboard connects to display (":0", or "" for $DISPLAY) using
// $XAUTHORITY.
func NewX11Clipboard(display string) (*X11Clipboard, error) {
	return NewX11ClipboardAuth(display, "")
}

// NewX11ClipboardAuth is NewX11Clipboard with an explicit Xauthority file,
// as returned by focus.FindGamescopeDisplay.
func NewX11ClipboardAuth(display, xauthority string) (*X11Clipboard, error) {
	c := &X11Clipboard{display: display, xauthority: xauthority, ReadTimeout: 200 * time.Millisecond}
	o, err := c.connectOwner()
	if err != nil {
		return nil, err
	}
	c.owner = o
	return c, nil
}

func dialWindow(display, xauthority string) (*xgb.Conn, xproto.Window, clipAtoms, error) {
	var a clipAtoms
	conn, err := focus.DialX(display, xauthority)
	if err != nil {
		return nil, 0, a, fmt.Errorf("clipboard: connect %s: %w", display, err)
	}
	fail := func(err error) (*xgb.Conn, xproto.Window, clipAtoms, error) {
		conn.Close()
		return nil, 0, a, fmt.Errorf("clipboard: %w", err)
	}
	for _, x := range []struct {
		p    *xproto.Atom
		name string
	}{
		{&a.clipboard, "CLIPBOARD"}, {&a.targets, "TARGETS"}, {&a.utf8, "UTF8_STRING"},
		{&a.text, "TEXT"}, {&a.prop, "DECKCHAT_SELECTION"},
	} {
		r, err := xproto.InternAtom(conn, false, uint16(len(x.name)), x.name).Reply()
		if err != nil {
			return fail(err)
		}
		*x.p = r.Atom
	}
	win, err := xproto.NewWindowId(conn)
	if err != nil {
		return fail(err)
	}
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	// Unmapped 1x1 InputOnly window: never visible, never takes focus.
	err = xproto.CreateWindowChecked(conn, 0, win, root, -10, -10, 1, 1, 0,
		xproto.WindowClassInputOnly, 0, 0, nil).Check()
	if err != nil {
		return fail(err)
	}
	return conn, win, a, nil
}

func (c *X11Clipboard) connectOwner() (*ownerConn, error) {
	conn, win, a, err := dialWindow(c.display, c.xauthority)
	if err != nil {
		return nil, err
	}
	o := &ownerConn{conn: conn, win: win, atoms: a}
	empty := ""
	o.text.Store(&empty)
	go o.loop()
	return o, nil
}

// Set makes text the CLIPBOARD contents and (re)asserts ownership. If the X
// connection has died, Set returns an error and the next call reconnects.
func (c *X11Clipboard) Set(text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.owner != nil && c.owner.dead.Load() {
		c.owner.conn.Close()
		c.owner = nil
		return errClipboardLost
	}
	if c.owner == nil {
		o, err := c.connectOwner()
		if err != nil {
			return err
		}
		c.owner = o
	}
	o := c.owner
	o.text.Store(&text)
	err := xproto.SetSelectionOwnerChecked(o.conn, o.win, o.atoms.clipboard, xproto.TimeCurrentTime).Check()
	if err == nil {
		var r *xproto.GetSelectionOwnerReply
		r, err = xproto.GetSelectionOwner(o.conn, o.atoms.clipboard).Reply()
		if err == nil && r.Owner != o.win {
			return errors.New("clipboard: could not take CLIPBOARD ownership")
		}
	}
	if err != nil {
		var xe xgb.Error
		if !errors.As(err, &xe) { // transport error: drop the connection
			o.dead.Store(true)
			o.conn.Close()
			c.owner = nil
		}
		return fmt.Errorf("clipboard: set owner: %w", err)
	}
	return nil
}

func (o *ownerConn) loop() {
	for {
		ev, xerr := o.conn.WaitForEvent()
		if ev == nil && xerr == nil {
			o.dead.Store(true)
			return
		}
		if e, ok := ev.(xproto.SelectionRequestEvent); ok {
			o.serve(e)
		}
		// SelectionClearEvent needs no action: the next Set re-asserts
		// ownership, and the reader's check catches a foreign owner.
	}
}

func (o *ownerConn) serve(e xproto.SelectionRequestEvent) {
	prop := e.Property
	if prop == xproto.AtomNone { // obsolete requestor
		prop = e.Target
	}
	if e.Selection != o.atoms.clipboard || e.Owner != o.win {
		prop = xproto.AtomNone
	} else if typ, format, data, ok := o.atoms.reply(e.Target, *o.text.Load()); ok {
		n := uint32(len(data))
		if format == 32 {
			n /= 4
		}
		xproto.ChangeProperty(o.conn, xproto.PropModeReplace, e.Requestor, prop, typ, format, n, data)
	} else {
		prop = xproto.AtomNone
	}
	ev := xproto.SelectionNotifyEvent{
		Time: e.Time, Requestor: e.Requestor, Selection: e.Selection, Target: e.Target, Property: prop,
	}
	xproto.SendEvent(o.conn, false, e.Requestor, xproto.EventMaskNoEvent, string(ev.Bytes()))
}

// reply builds the property contents for a conversion target: TARGETS, then
// UTF8_STRING/TEXT as UTF-8, STRING as Latin-1 (others become '?').
func (a clipAtoms) reply(target xproto.Atom, text string) (typ xproto.Atom, format byte, data []byte, ok bool) {
	switch target {
	case a.targets:
		list := []xproto.Atom{a.targets, a.utf8, xproto.AtomString, a.text}
		data = make([]byte, 4*len(list))
		for i, t := range list {
			xgb.Put32(data[4*i:], uint32(t))
		}
		return xproto.AtomAtom, 32, data, true
	case a.utf8, a.text:
		return a.utf8, 8, []byte(text), true
	case xproto.AtomString:
		for _, r := range text {
			if r < 256 {
				data = append(data, byte(r))
			} else {
				data = append(data, '?')
			}
		}
		return xproto.AtomString, 8, data, true
	}
	return 0, 0, nil, false
}

// ReadBack converts CLIPBOARD to UTF8_STRING from a second, independent
// connection and returns the text, as another client on the display (the
// game) would see it.
func (c *X11Clipboard) ReadBack() (string, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	if c.reader == nil {
		r, err := c.connectReader()
		if err != nil {
			return "", err
		}
		c.reader = r
	}
	r := c.reader
	fail := func(err error) (string, error) {
		r.conn.Close()
		c.reader = nil
		return "", err
	}

	// Drop notifications left over from an earlier timed-out read.
	for drained := false; !drained; {
		select {
		case _, ok := <-r.notify:
			if !ok {
				return fail(errClipboardLost)
			}
		default:
			drained = true
		}
	}
	err := xproto.ConvertSelectionChecked(r.conn, r.win, r.atoms.clipboard, r.atoms.utf8, r.atoms.prop, xproto.TimeCurrentTime).Check()
	if err != nil {
		return fail(fmt.Errorf("clipboard: convert: %w", err))
	}
	timeout := c.ReadTimeout
	if timeout <= 0 {
		timeout = 200 * time.Millisecond
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-r.notify:
			if !ok {
				return fail(errClipboardLost)
			}
			if ev.Requestor != r.win || ev.Selection != r.atoms.clipboard {
				continue
			}
			if ev.Property == xproto.AtomNone {
				return "", errors.New("clipboard: no owner or conversion refused")
			}
			p, err := xproto.GetProperty(r.conn, true, r.win, ev.Property, xproto.GetPropertyTypeAny, 0, 1<<16).Reply()
			if err != nil {
				var xe xgb.Error
				if errors.As(err, &xe) {
					return "", err
				}
				return fail(err)
			}
			return string(p.Value), nil
		case <-timer.C:
			return "", errors.New("clipboard: read-back timed out")
		}
	}
}

func (c *X11Clipboard) connectReader() (*readerConn, error) {
	conn, win, a, err := dialWindow(c.display, c.xauthority)
	if err != nil {
		return nil, err
	}
	r := &readerConn{conn: conn, win: win, atoms: a, notify: make(chan xproto.SelectionNotifyEvent, 8)}
	go func() {
		defer close(r.notify)
		for {
			ev, xerr := conn.WaitForEvent()
			if ev == nil && xerr == nil {
				return
			}
			if e, ok := ev.(xproto.SelectionNotifyEvent); ok {
				select {
				case r.notify <- e:
				default: // nobody waiting; drop
				}
			}
		}
	}()
	return r, nil
}

// Close drops both X connections. The clipboard contents go with the owner.
func (c *X11Clipboard) Close() error {
	c.mu.Lock()
	if c.owner != nil {
		c.owner.conn.Close()
		c.owner = nil
	}
	c.mu.Unlock()
	c.rmu.Lock()
	if c.reader != nil {
		c.reader.conn.Close()
		c.reader = nil
	}
	c.rmu.Unlock()
	return nil
}
