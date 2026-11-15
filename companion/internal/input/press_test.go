package input

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
)

type fakeEventSink struct {
	events []InputEvent
	failAt int // fail the write that would contain event index failAt (0 = never)
	n      int
}

func (f *fakeEventSink) writeEvents(evs []InputEvent) (int, error) {
	for i, ev := range evs {
		f.n++
		if f.failAt != 0 && f.n == f.failAt {
			return i, errors.New("write: no such device")
		}
		f.events = append(f.events, ev)
	}
	return len(evs), nil
}

func describe(evs []InputEvent) []string {
	var out []string
	for _, e := range evs {
		switch {
		case e.Type == evKey && e.Value == 1:
			out = append(out, "down:"+Key(e.Code).String())
		case e.Type == evKey && e.Value == 0:
			out = append(out, "up:"+Key(e.Code).String())
		case e.Type == evSyn && e.Code == synReport:
			out = append(out, "syn")
		default:
			out = append(out, fmt.Sprintf("?%d/%d/%d", e.Type, e.Code, e.Value))
		}
	}
	return out
}

func TestPressChordEventOrder(t *testing.T) {
	sink := &fakeEventSink{}
	if err := pressChord(sink, 0, []Key{KeyLeftCtrl, KeyV}); err != nil {
		t.Fatal(err)
	}
	want := []string{"down:LEFTCTRL", "syn", "down:V", "syn", "up:V", "syn", "up:LEFTCTRL", "syn"}
	if got := describe(sink.events); !eqStrings(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// AE5: a write error mid-chord still releases every key that went down, and
// never releases keys that didn't.
func TestPressChordReleasesOnError(t *testing.T) {
	// Events: ctrl(1) syn(2) alt(3) syn(4) shift(5)... fail on shift's key
	// event, so ctrl and alt are down and shift/F12 never went down.
	sink := &fakeEventSink{failAt: 5}
	err := pressChord(sink, 0, Chord)
	if err == nil {
		t.Fatal("expected the write error")
	}
	want := []string{"down:LEFTCTRL", "syn", "down:LEFTALT", "syn", "up:LEFTALT", "syn", "up:LEFTCTRL", "syn"}
	if got := describe(sink.events); !eqStrings(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestPressChordSynFailureCountsKeyAsDown(t *testing.T) {
	// ctrl(1) syn(2) v(3) syn(4 fails): V's key event was written, so it
	// must be released.
	sink := &fakeEventSink{failAt: 4}
	if err := pressChord(sink, 0, []Key{KeyLeftCtrl, KeyV}); err == nil {
		t.Fatal("expected error")
	}
	want := []string{"down:LEFTCTRL", "syn", "down:V", "up:V", "syn", "up:LEFTCTRL", "syn"}
	if got := describe(sink.events); !eqStrings(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestPressChordKeepsReleasingAfterReleaseError(t *testing.T) {
	// ctrl(1) syn(2) v(3) syn(4) up v(5 fails) -> still release ctrl.
	sink := &fakeEventSink{failAt: 5}
	if err := pressChord(sink, 0, []Key{KeyLeftCtrl, KeyV}); err == nil {
		t.Fatal("expected error")
	}
	want := []string{"down:LEFTCTRL", "syn", "down:V", "syn", "up:LEFTCTRL", "syn"}
	if got := describe(sink.events); !eqStrings(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// struct input_event on 64-bit Linux (amd64 and arm64): 16-byte timeval,
// u16 type, u16 code, s32 value = 24 bytes, little endian.
func TestInputEventEncoding(t *testing.T) {
	b := InputEvent{Type: evKey, Code: uint16(KeyF12), Value: 1}.marshal()
	if len(b) != 24 {
		t.Fatalf("len = %d", len(b))
	}
	want := make([]byte, 24)
	binary.LittleEndian.PutUint16(want[16:], 1)
	binary.LittleEndian.PutUint16(want[18:], 88)
	binary.LittleEndian.PutUint32(want[20:], 1)
	if !bytes.Equal(b, want) {
		t.Fatalf("got % x\nwant % x", b, want)
	}
	ev, err := ParseInputEvent(b)
	if err != nil || ev.Type != evKey || ev.Code != 88 || ev.Value != 1 {
		t.Fatalf("parse = %+v %v", ev, err)
	}
}
