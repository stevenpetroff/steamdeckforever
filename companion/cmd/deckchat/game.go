package main

import (
	"errors"
	"sync"
	"time"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/focus"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/input"
)

// gameDisplay finds gamescope's Xwayland display on demand and keeps a focus
// checker and clipboard bound to it. The service usually starts before Game
// Mode (lingering), and gamescope restarts on every Desktop/Game Mode switch,
// so discovery is retried whenever a call fails.
type gameDisplay struct {
	mu   sync.Mutex
	find func() (display, xauthority string, err error)
	fc   *focus.Checker
	clip *input.X11Clipboard

	// After a failed discovery, don't rescan /proc again until retryAt.
	lastErr error
	retryAt time.Time
}

// discoveryBackoff spaces out rescans while Game Mode isn't running.
const discoveryBackoff = 5 * time.Second

// locate runs discovery, backing off after a failure. Callers hold g.mu.
func (g *gameDisplay) locate() (display, xauthority string, err error) {
	if time.Now().Before(g.retryAt) {
		return "", "", g.lastErr
	}
	display, xauthority, err = g.find()
	if err != nil {
		g.lastErr, g.retryAt = err, time.Now().Add(discoveryBackoff)
	}
	return display, xauthority, err
}

func newGameDisplay() *gameDisplay {
	return &gameDisplay{find: focus.FindGamescopeDisplay}
}

func (g *gameDisplay) checker() (*focus.Checker, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fc != nil {
		return g.fc, nil
	}
	display, xauth, err := g.locate()
	if err != nil {
		return nil, err
	}
	fc := focus.NewGamescope(display)
	fc.XAuthority = xauth
	g.fc = fc
	return fc, nil
}

func (g *gameDisplay) clipboard() (*input.X11Clipboard, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.clip != nil {
		return g.clip, nil
	}
	display, xauth, err := g.locate()
	if err != nil {
		return nil, err
	}
	clip, err := input.NewX11ClipboardAuth(display, xauth)
	if err != nil {
		return nil, err
	}
	g.clip = clip
	return clip, nil
}

// reset drops the cached connections so the next call rediscovers.
func (g *gameDisplay) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fc != nil {
		g.fc.Close()
		g.fc = nil
	}
	if g.clip != nil {
		g.clip.Close()
		g.clip = nil
	}
}

// Focused implements input.FocusChecker.
func (g *gameDisplay) Focused() (bool, error) {
	fc, err := g.checker()
	if err != nil {
		return false, err
	}
	ok, err := fc.Focused()
	if err != nil {
		g.reset()
	}
	return ok, err
}

// Set implements input.Clipboard.
func (g *gameDisplay) Set(text string) error {
	clip, err := g.clipboard()
	if err != nil {
		return err
	}
	if err := clip.Set(text); err != nil {
		g.reset()
		return err
	}
	return nil
}

// ReadBack implements input.Clipboard.
func (g *gameDisplay) ReadBack() (string, error) {
	g.mu.Lock()
	clip := g.clip
	g.mu.Unlock()
	if clip == nil {
		return "", errors.New("clipboard not set up")
	}
	return clip.ReadBack()
}
