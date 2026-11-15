package main

import (
	"errors"
	"sync"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/focus"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/input"
)

// gameDisplay finds gamescope's Xwayland display on demand and keeps a focus
// checker and clipboard bound to it. The service usually starts before Game
// Mode (lingering), and gamescope restarts on every Desktop/Game Mode switch,
// so discovery is retried whenever a call fails.
type gameDisplay struct {
	mu    sync.Mutex
	find  func() (display, xauthority string, err error)
	fc    *focus.Checker
	clip  *input.X11Clipboard
	where string
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
	display, xauth, err := g.find()
	if err != nil {
		return nil, err
	}
	fc := focus.NewGamescope(display)
	fc.XAuthority = xauth
	g.fc, g.where = fc, display
	return fc, nil
}

func (g *gameDisplay) clipboard() (*input.X11Clipboard, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.clip != nil {
		return g.clip, nil
	}
	display, xauth, err := g.find()
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
	g.clip = nil
	g.where = ""
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

// Display reports the display in use, or "" before discovery.
func (g *gameDisplay) Display() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.where
}
