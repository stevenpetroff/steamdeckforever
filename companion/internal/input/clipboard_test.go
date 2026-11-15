package input

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/jezek/xgb/xproto"
)

var testAtoms = clipAtoms{clipboard: 100, targets: 101, utf8: 102, text: 103, prop: 104}

func TestSelectionReplyUTF8(t *testing.T) {
	text := "héllo 🙂"
	for _, target := range []xproto.Atom{testAtoms.utf8, testAtoms.text} {
		typ, format, data, ok := testAtoms.reply(target, text)
		if !ok || typ != testAtoms.utf8 || format != 8 || !bytes.Equal(data, []byte(text)) {
			t.Fatalf("target %d: typ=%d format=%d data=%q ok=%v", target, typ, format, data, ok)
		}
	}
}

func TestSelectionReplyString(t *testing.T) {
	typ, format, data, ok := testAtoms.reply(xproto.AtomString, "héllo 🙂")
	if !ok || typ != xproto.AtomString || format != 8 {
		t.Fatalf("typ=%d format=%d ok=%v", typ, format, ok)
	}
	if want := []byte("h\xe9llo ?"); !bytes.Equal(data, want) {
		t.Fatalf("latin-1 = %q want %q", data, want)
	}
}

func TestSelectionReplyTargets(t *testing.T) {
	typ, format, data, ok := testAtoms.reply(testAtoms.targets, "x")
	if !ok || typ != xproto.AtomAtom || format != 32 {
		t.Fatalf("typ=%d format=%d ok=%v", typ, format, ok)
	}
	var got []uint32
	for i := 0; i+4 <= len(data); i += 4 {
		got = append(got, binary.LittleEndian.Uint32(data[i:]))
	}
	want := []uint32{101, 102, uint32(xproto.AtomString), 103}
	if len(got) != len(want) {
		t.Fatalf("targets = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("targets = %v want %v", got, want)
		}
	}
}

func TestSelectionReplyRefusesUnknownTarget(t *testing.T) {
	if _, _, _, ok := testAtoms.reply(999, "x"); ok {
		t.Fatal("unknown target accepted")
	}
}
