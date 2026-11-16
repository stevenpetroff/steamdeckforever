package input

import (
	"encoding/binary"
	"errors"
	"time"
)

// Linux input event constants (linux/input-event-codes.h).
const (
	evSyn     uint16 = 0x00
	evKey     uint16 = 0x01
	synReport uint16 = 0
)

// inputEventSize is sizeof(struct input_event) on 64-bit Linux, both amd64
// and arm64: struct timeval {s64 tv_sec; s64 tv_usec} (16 bytes), u16 type,
// u16 code, s32 value.
const inputEventSize = 24

// InputEvent is one struct input_event without its timestamp (the kernel
// stamps events written to uinput).
type InputEvent struct {
	Type  uint16
	Code  uint16
	Value int32
}

// marshal encodes the event as the 64-bit little-endian struct input_event
// with a zero timeval. Both Deck (amd64) and Docker on Apple Silicon (arm64)
// are little endian.
func (e InputEvent) marshal() []byte {
	b := make([]byte, inputEventSize)
	binary.LittleEndian.PutUint16(b[16:], e.Type)
	binary.LittleEndian.PutUint16(b[18:], e.Code)
	binary.LittleEndian.PutUint32(b[20:], uint32(e.Value))
	return b
}

// ParseInputEvent decodes a 24-byte struct input_event as read from
// /dev/input/eventN, dropping the timestamp.
func ParseInputEvent(b []byte) (InputEvent, error) {
	if len(b) < inputEventSize {
		return InputEvent{}, errors.New("input: short input_event")
	}
	return InputEvent{
		Type:  binary.LittleEndian.Uint16(b[16:]),
		Code:  binary.LittleEndian.Uint16(b[18:]),
		Value: int32(binary.LittleEndian.Uint32(b[20:])),
	}, nil
}

// eventSink writes events; it returns how many whole events went out.
type eventSink interface {
	writeEvents(evs []InputEvent) (int, error)
}

// emitKey writes one key event and its SYN_REPORT. went reports whether the
// key event itself reached the device (so it must later be released).
func emitKey(w eventSink, k Key, value int32) (went bool, err error) {
	n, err := w.writeEvents([]InputEvent{
		{Type: evKey, Code: uint16(k), Value: value},
		{Type: evSyn, Code: synReport},
	})
	return n >= 1, err
}

// pressChord presses keys down in order, then releases them in reverse. On
// a write error it stops pressing, still releases every key that went down
// (continuing past release errors), and returns the first error. It never
// releases a key it didn't press, so keys the player is holding are left
// alone (AE5). If a release write itself fails, the caller closes the device;
// the kernel releases a destroyed device's held keys.
func pressChord(w eventSink, delay time.Duration, keys []Key) error {
	var firstErr error
	down := make([]Key, 0, len(keys))
	for _, k := range keys {
		went, err := emitKey(w, k, 1)
		if went {
			down = append(down, k)
		}
		if err != nil {
			firstErr = err
			break
		}
		sleep(delay)
	}
	for i := len(down) - 1; i >= 0; i-- {
		if _, err := emitKey(w, down[i], 0); err != nil && firstErr == nil {
			firstErr = err
		}
		if i > 0 {
			sleep(delay)
		}
	}
	return firstErr
}

func sleep(d time.Duration) {
	if d > 0 {
		time.Sleep(d)
	}
}
