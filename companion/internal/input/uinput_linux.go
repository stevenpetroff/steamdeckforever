//go:build linux

package input

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// uinput ioctls (linux/uinput.h). The _IOW/_IO encoding is the asm-generic
// one, identical on amd64 and arm64.
const (
	uiDevCreate  = 0x5501     // _IO('U', 1)
	uiDevDestroy = 0x5502     // _IO('U', 2)
	uiDevSetup   = 0x405c5503 // _IOW('U', 3, struct uinput_setup), sizeof = 92
	uiSetEvBit   = 0x40045564 // _IOW('U', 100, int)
	uiSetKeyBit  = 0x40045565 // _IOW('U', 101, int)

	busVirtual = 0x06

	uinputMaxNameSize = 80
	uinputSetupSize   = 8 + uinputMaxNameSize + 4          // struct uinput_setup
	uinputUserDevSize = uinputMaxNameSize + 8 + 4 + 4*64*4 // legacy struct uinput_user_dev
)

// DefaultUinputName is the virtual keyboard's device name.
const DefaultUinputName = "DeckChat Virtual Keyboard"

// UinputOptions configures OpenUinputWith. Zero values take defaults.
type UinputOptions struct {
	Path        string        // default /dev/uinput
	Name        string        // default DefaultUinputName
	KeyDelay    time.Duration // between events inside a chord, default 15ms
	CreateSleep time.Duration // after UI_DEV_CREATE so consumers see the device, default 300ms
}

type uinputKeyboard struct {
	mu    sync.Mutex
	fd    int
	delay time.Duration
}

// OpenUinput creates the virtual keyboard on /dev/uinput with defaults.
func OpenUinput() (Keyboard, error) { return OpenUinputWith(UinputOptions{}) }

// OpenUinputWith creates a virtual keyboard that can press only the keys in
// AllKeys.
func OpenUinputWith(o UinputOptions) (Keyboard, error) {
	if o.Path == "" {
		o.Path = "/dev/uinput"
	}
	if o.Name == "" {
		o.Name = DefaultUinputName
	}
	if o.KeyDelay <= 0 {
		o.KeyDelay = 15 * time.Millisecond
	}
	if o.CreateSleep <= 0 {
		o.CreateSleep = 300 * time.Millisecond
	}
	if len(o.Name) >= uinputMaxNameSize {
		return nil, errors.New("uinput: device name too long")
	}

	fd, err := unix.Open(o.Path, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("uinput: open %s: %w", o.Path, err)
	}
	fail := func(step string, err error) (Keyboard, error) {
		unix.Close(fd)
		return nil, fmt.Errorf("uinput: %s: %w", step, err)
	}

	if err := unix.IoctlSetInt(fd, uiSetEvBit, int(evKey)); err != nil {
		return fail("UI_SET_EVBIT", err)
	}
	for _, k := range AllKeys {
		if err := unix.IoctlSetInt(fd, uiSetKeyBit, int(k)); err != nil {
			return fail("UI_SET_KEYBIT", err)
		}
	}

	if err := devSetup(fd, o.Name); err != nil {
		if !errors.Is(err, unix.ENOTTY) && !errors.Is(err, unix.EINVAL) {
			return fail("UI_DEV_SETUP", err)
		}
		// Kernel older than 4.5: write the legacy uinput_user_dev.
		if err := writeLegacyUserDev(fd, o.Name); err != nil {
			return fail("write uinput_user_dev", err)
		}
	}

	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uiDevCreate, 0); errno != 0 {
		return fail("UI_DEV_CREATE", errno)
	}
	time.Sleep(o.CreateSleep)
	return &uinputKeyboard{fd: fd, delay: o.KeyDelay}, nil
}

func putInputID(b []byte) {
	// struct input_id {u16 bustype, vendor, product, version}
	ne := binary.NativeEndian
	ne.PutUint16(b[0:], busVirtual)
	ne.PutUint16(b[2:], 0x1209) // pid.codes test vendor
	ne.PutUint16(b[4:], 0xdc01)
	ne.PutUint16(b[6:], 1)
}

func devSetup(fd int, name string) error {
	// struct uinput_setup {struct input_id id; char name[80]; u32 ff_effects_max;}
	b := make([]byte, uinputSetupSize)
	putInputID(b[0:8])
	copy(b[8:8+uinputMaxNameSize], name)
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uiDevSetup, uintptr(unsafe.Pointer(&b[0]))); errno != 0 {
		return errno
	}
	return nil
}

func writeLegacyUserDev(fd int, name string) error {
	// struct uinput_user_dev {char name[80]; struct input_id id; u32
	// ff_effects_max; s32 absmax[64], absmin[64], absfuzz[64], absflat[64];}
	b := make([]byte, uinputUserDevSize)
	copy(b[0:uinputMaxNameSize], name)
	putInputID(b[uinputMaxNameSize : uinputMaxNameSize+8])
	n, err := unix.Write(fd, b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return errors.New("short write")
	}
	return nil
}

func (k *uinputKeyboard) writeEvents(evs []InputEvent) (int, error) {
	buf := make([]byte, 0, len(evs)*inputEventSize)
	for _, e := range evs {
		buf = append(buf, e.marshal()...)
	}
	n, err := unix.Write(k.fd, buf)
	if n < 0 {
		n = 0
	}
	whole := n / inputEventSize
	if err == nil && whole < len(evs) {
		err = errors.New("uinput: short write")
	}
	if err != nil {
		return whole, fmt.Errorf("uinput: write: %w", err)
	}
	return whole, nil
}

func (k *uinputKeyboard) Press(keys ...Key) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.fd < 0 {
		return errors.New("uinput: keyboard closed")
	}
	return pressChord(k, k.delay, keys)
}

// Close destroys the device. The kernel releases any key still down on a
// destroyed device.
func (k *uinputKeyboard) Close() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.fd < 0 {
		return nil
	}
	unix.Syscall(unix.SYS_IOCTL, uintptr(k.fd), uiDevDestroy, 0)
	err := unix.Close(k.fd)
	k.fd = -1
	return err
}
