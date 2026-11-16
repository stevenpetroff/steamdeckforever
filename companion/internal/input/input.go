// Package input delivers one phone message into WoW's chat box (plan U5,
// KTD3, KTD4, KTD5), or pastes text into whichever WoW text box has focus.
//
// Every message is pasted, never typed: the text goes on the game display's
// X clipboard, the clipboard is read back until it matches, and only then
// does a virtual keyboard press the add-on chord (CTRL-ALT-SHIFT-F12, which
// opens a clean chat box), Ctrl+V and Enter. Message characters never become
// key events, so a chat box that failed to open can't turn letters into
// keybinds.
//
// Paste mode skips the chat box: it presses the add-on's paste chord
// (CTRL-ALT-SHIFT-F11) and Ctrl+V, and never Enter. A focused WoW edit box
// swallows the chord and takes the paste. With nothing focused the chord
// fires the add-on binding instead, which briefly blocks Ctrl+V so the paste
// can't trigger a keybind.
package input

// Key is a Linux evdev key code (linux/input-event-codes.h).
type Key uint16

// The only keys the sender ever presses.
const (
	KeyEnter     Key = 28 // KEY_ENTER
	KeyLeftCtrl  Key = 29 // KEY_LEFTCTRL
	KeyLeftShift Key = 42 // KEY_LEFTSHIFT
	KeyV         Key = 47 // KEY_V
	KeyLeftAlt   Key = 56 // KEY_LEFTALT
	KeyF11       Key = 87 // KEY_F11
	KeyF12       Key = 88 // KEY_F12
)

// AllKeys lists every key the virtual keyboard registers.
var AllKeys = []Key{KeyLeftCtrl, KeyLeftAlt, KeyLeftShift, KeyF11, KeyF12, KeyV, KeyEnter}

// Chord is the DeckChat add-on's open-chat binding (KTD4).
var Chord = []Key{KeyLeftCtrl, KeyLeftAlt, KeyLeftShift, KeyF12}

// PasteChord is the add-on's paste guard binding: it only fires when no
// WoW edit box has focus, and then blocks the Ctrl+V that follows.
var PasteChord = []Key{KeyLeftCtrl, KeyLeftAlt, KeyLeftShift, KeyF11}

func (k Key) String() string {
	switch k {
	case KeyEnter:
		return "ENTER"
	case KeyLeftCtrl:
		return "LEFTCTRL"
	case KeyLeftShift:
		return "LEFTSHIFT"
	case KeyV:
		return "V"
	case KeyLeftAlt:
		return "LEFTALT"
	case KeyF11:
		return "F11"
	case KeyF12:
		return "F12"
	}
	return "KEY?"
}

// Keyboard is a virtual keyboard.
type Keyboard interface {
	// Press presses a chord: every key down in order, then every key up in
	// reverse order. Each key that went down gets a key-up, even if a write
	// fails part-way (best-effort release).
	Press(keys ...Key) error
	Close() error
}

// Clipboard owns the game display's CLIPBOARD selection.
type Clipboard interface {
	// Set makes text the clipboard contents.
	Set(text string) error
	// ReadBack reads the clipboard as another client on the game's display
	// would see it.
	ReadBack() (string, error)
}

// FocusChecker reports whether WoW is the focused window.
// focus.Checker implements it.
type FocusChecker interface {
	Focused() (bool, error)
}
