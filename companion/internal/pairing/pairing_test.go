package pairing

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	qrcode "github.com/skip2/go-qrcode"
)

var hexRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestLoadOrCreateStableAndPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf", "secret")

	s1, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if !hexRe.MatchString(s1.Hex()) {
		t.Fatalf("Hex() = %q, want 32 lowercase hex chars", s1.Hex())
	}
	if len(s1.Bytes()) != 16 {
		t.Fatalf("Bytes() len = %d, want 16", len(s1.Bytes()))
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != s1.Hex() {
		t.Fatalf("file holds %q, want %q", raw, s1.Hex())
	}
	assertMode(t, path, 0o600)
	assertMode(t, filepath.Dir(path), 0o700)

	s2, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("second LoadOrCreate: %v", err)
	}
	if s2.Hex() != s1.Hex() {
		t.Fatalf("secret changed across loads: %s vs %s", s1.Hex(), s2.Hex())
	}

	// Bytes must be a copy: mutating it must not change the secret.
	b := s2.Bytes()
	b[0] ^= 0xff
	if s2.Hex() != s1.Hex() {
		t.Fatal("Bytes() exposed internal storage")
	}
}

func TestLoadOrCreateRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("not-hex"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(path); err == nil {
		t.Fatal("expected error for corrupt secret file")
	}
}

func TestResetRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	s1, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Reset(path)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if s1.Hex() == s2.Hex() {
		t.Fatal("Reset kept the same secret")
	}
	s3, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if s3.Hex() != s2.Hex() {
		t.Fatal("Reset secret was not persisted")
	}
	assertMode(t, path, 0o600)
}

func TestSecretRedacted(t *testing.T) {
	s, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	logger.Info("pairing", "secret", s)
	outs := []string{
		s.String(),
		fmt.Sprintf("%v", s),
		fmt.Sprintf("%+v", s),
		fmt.Sprintf("%#v", s),
		fmt.Sprintf("%s", s),
		fmt.Sprint(s),
		logBuf.String(),
	}
	for _, o := range outs {
		if strings.Contains(o, s.Hex()) {
			t.Fatalf("secret leaked in %q", o)
		}
	}
	if s.String() != "[redacted]" {
		t.Fatalf("String() = %q", s.String())
	}
}

func TestParseHex(t *testing.T) {
	s, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParseHex(s.Hex())
	if err != nil || p.Hex() != s.Hex() {
		t.Fatalf("ParseHex round trip: %v %q", err, p.Hex())
	}
	for _, bad := range []string{"", "abc", strings.Repeat("g", 32), strings.Repeat("A", 32), strings.Repeat("a", 34)} {
		if _, err := ParseHex(bad); err == nil {
			t.Errorf("ParseHex(%q) succeeded", bad)
		}
	}
}

func TestURL(t *testing.T) {
	s, _ := ParseHex("00112233445566778899aabbccddeeff")
	cases := []struct {
		ip   string
		want string
	}{
		{"192.168.1.20", "http://192.168.1.20:7480/#00112233445566778899aabbccddeeff"},
		{"fd00::5", "http://[fd00::5]:7480/#00112233445566778899aabbccddeeff"},
		{"fe80::1%wlan0", "http://[fe80::1%25wlan0]:7480/#00112233445566778899aabbccddeeff"},
	}
	for _, c := range cases {
		if got := URL(c.ip, 7480, s); got != c.want {
			t.Errorf("URL(%q) = %q, want %q", c.ip, got, c.want)
		}
	}
}

var luaRe = regexp.MustCompile(`^DeckChatPairing = \{ url = "((?:[^"\\]|\\.)*)", size = (\d+), rows = \{ (.*) \} \}\n$`)

func TestWriteLua(t *testing.T) {
	dir := t.TempDir()
	url := "http://192.168.1.20:7480/#00112233445566778899aabbccddeeff"
	if err := WriteLua(dir, url); err != nil {
		t.Fatalf("WriteLua: %v", err)
	}
	path := filepath.Join(dir, "Pairing.lua")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertMode(t, path, 0o600)
	m := luaRe.FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("Pairing.lua does not match expected shape:\n%s", raw)
	}
	if m[1] != url {
		t.Fatalf("url = %q, want %q", m[1], url)
	}
	size, _ := strconv.Atoi(m[2])
	rows := strings.Split(m[3], ", ")
	if len(rows) != size {
		t.Fatalf("rows = %d, size = %d", len(rows), size)
	}
	q, err := qrcode.New(url, qrcode.Medium)
	if err != nil {
		t.Fatal(err)
	}
	q.DisableBorder = true
	want := q.Bitmap()
	if len(want) != size {
		t.Fatalf("size %d, want %d", size, len(want))
	}
	for y, r := range rows {
		r = strings.Trim(r, `"`)
		if len(r) != size {
			t.Fatalf("row %d len %d, want %d", y, len(r), size)
		}
		for x, c := range r {
			if (c == '1') != want[y][x] || (c != '0' && c != '1') {
				t.Fatalf("module (%d,%d) = %q, want %v", x, y, c, want[y][x])
			}
		}
	}

	// Rewriting with a new URL replaces the file and leaves no temp files.
	url2 := "http://10.0.0.5:7480/#00112233445566778899aabbccddeeff"
	if err := WriteLua(dir, url2); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if m := luaRe.FindStringSubmatch(string(raw)); m == nil || m[1] != url2 {
		t.Fatalf("rewrite did not update url: %s", raw)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftover files in addon dir: %v", entries)
	}
}

func TestLuaQuote(t *testing.T) {
	got := luaQuote("a\"b\\c\nd\x01]]")
	want := `"a\"b\\c\nd\001]]"`
	if got != want {
		t.Fatalf("luaQuote = %s, want %s", got, want)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
