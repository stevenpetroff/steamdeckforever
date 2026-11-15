//go:build !linux

package input

import (
	"errors"
	"time"
)

// DefaultUinputName is the virtual keyboard's device name.
const DefaultUinputName = "DeckChat Virtual Keyboard"

// UinputOptions configures OpenUinputWith (Linux only).
type UinputOptions struct {
	Path        string
	Name        string
	KeyDelay    time.Duration
	CreateSleep time.Duration
}

var errNoUinput = errors.New("uinput: virtual keyboard is only available on Linux")

// OpenUinput always fails off Linux.
func OpenUinput() (Keyboard, error) { return nil, errNoUinput }

// OpenUinputWith always fails off Linux.
func OpenUinputWith(UinputOptions) (Keyboard, error) { return nil, errNoUinput }
