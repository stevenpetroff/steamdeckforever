package focus

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeProc writes /proc/<pid>/{comm,stat,environ} under root.
func fakeProc(t *testing.T, root string, pid, ppid int, comm string, env ...string) {
	t.Helper()
	dir := filepath.Join(root, "proc", itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "comm"), comm+"\n")
	// The comm in stat can contain spaces and parens; the parser must use
	// the last ')'.
	write(t, filepath.Join(dir, "stat"), itoa(pid)+" ("+comm+") S "+itoa(ppid)+" 1 1 0 -1\n")
	write(t, filepath.Join(dir, "environ"), strings.Join(env, "\x00")+"\x00")
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func displays(cs []Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Display+"|"+c.XAuthority)
	}
	return out
}

func TestScanCandidates(t *testing.T) {
	root := t.TempDir()
	// Unrelated processes, including one with a DISPLAY.
	fakeProc(t, root, 10, 1, "systemd", "PATH=/usr/bin")
	fakeProc(t, root, 20, 1, "plasmashell", "DISPLAY=:5", "XAUTHORITY=/tmp/other")
	// gamescope itself has no DISPLAY; its children do (gamescope sets it).
	fakeProc(t, root, 100, 1, "gamescope-wl", "XDG_RUNTIME_DIR=/run/user/1000")
	fakeProc(t, root, 150, 100, "steam", "DISPLAY=:0", "XAUTHORITY=/run/user/1000/xauth_abc")
	fakeProc(t, root, 160, 100, "steamwebhelper", "DISPLAY=:0", "XAUTHORITY=/run/user/1000/xauth_abc")
	fakeProc(t, root, 170, 100, "reaper", "DISPLAY=:1", "XAUTHORITY=/run/user/1000/xauth_abc")
	// A second gamescope (nested) carrying DISPLAY itself.
	fakeProc(t, root, 200, 1, "gamescope", "DISPLAY=:2")
	// Non-numeric /proc entries are ignored.
	write(t, filepath.Join(root, "proc", "self", "comm"), "x")
	// Sockets.
	for _, n := range []string{"X0", "X1", "X12", "X3", "junk"} {
		write(t, filepath.Join(root, "tmp", ".X11-unix", n), "")
	}

	got := displays(ScanCandidates(root))
	want := []string{
		":0|/run/user/1000/xauth_abc",
		":1|/run/user/1000/xauth_abc",
		":2|",
		":3|",
		":12|",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

func TestScanCandidatesEmpty(t *testing.T) {
	if got := ScanCandidates(t.TempDir()); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestRankPrefersFocusedAppDisplay(t *testing.T) {
	cs := []Candidate{{Display: ":0"}, {Display: ":1"}, {Display: ":2"}, {Display: ":3"}}
	got := rankCandidates(cs, func(c Candidate) bool { return c.Display == ":1" || c.Display == ":3" })
	if g := displays(got); strings.Join(g, ",") != ":1|,:3|,:0|,:2|" {
		t.Fatalf("got %v", g)
	}
	if !got[0].HasFocusedApp || got[2].HasFocusedApp {
		t.Fatalf("HasFocusedApp not set: %+v", got)
	}
}

func TestParseDisplay(t *testing.T) {
	for in, want := range map[string]string{":0": "0", ":1.0": "1", "unix:12": "12", ":x": "", "": "", "host:1": ""} {
		if got, _ := displayNumber(in); got != want {
			t.Errorf("%q -> %q want %q", in, got, want)
		}
	}
}

func xauthEntry(family uint16, addr, number, name string, data []byte) []byte {
	var b bytes.Buffer
	put := func(s []byte) {
		binary.Write(&b, binary.BigEndian, uint16(len(s)))
		b.Write(s)
	}
	binary.Write(&b, binary.BigEndian, family)
	put([]byte(addr))
	put([]byte(number))
	put([]byte(name))
	put(data)
	return b.Bytes()
}

func TestCookieFromXauthority(t *testing.T) {
	c0 := bytes.Repeat([]byte{0xaa}, 16)
	c1 := bytes.Repeat([]byte{0xbb}, 16)
	any := bytes.Repeat([]byte{0xcc}, 16)
	var file []byte
	file = append(file, xauthEntry(256, "steamdeck", "0", "MIT-MAGIC-COOKIE-1", c0)...)
	file = append(file, xauthEntry(256, "steamdeck", "1", "XDM-AUTHORIZATION-1", []byte{1, 2})...)
	file = append(file, xauthEntry(256, "steamdeck", "1", "MIT-MAGIC-COOKIE-1", c1)...)
	file = append(file, xauthEntry(65535, "", "", "MIT-MAGIC-COOKIE-1", any)...)

	for num, want := range map[string][]byte{"0": c0, "1": c1, "7": any} {
		got, err := cookieFor(file, num)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("display %s: % x %v", num, got, err)
		}
	}
	if _, err := cookieFor(file[:10], "0"); err == nil {
		t.Error("truncated file should fail")
	}
	if _, err := cookieFor(xauthEntry(256, "h", "0", "MIT-MAGIC-COOKIE-1", c0), "3"); err == nil {
		t.Error("no matching entry should fail")
	}
}
