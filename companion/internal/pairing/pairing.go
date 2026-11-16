// Package pairing owns the pairing secret and publishes the pairing URL and
// its QR matrix to the in-game add-on (plan KTD6, docs/protocol.md).
//
// The secret must never be logged. Secret's String, GoString and LogValue all
// return "[redacted]" so an accidental %v or slog attribute is harmless; use
// Hex or Bytes only where the raw value is genuinely needed.
package pairing

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// SecretLen is the size of the pairing secret in bytes (128 bits).
const SecretLen = 16

// LuaFileName is the file the add-on loads at login or /reload.
const LuaFileName = "Pairing.lua"

const redacted = "[redacted]"

// Secret is the 128-bit pairing secret. The zero value is not a valid secret.
type Secret struct {
	b     [SecretLen]byte
	valid bool
}

// Generate returns a fresh random secret.
func Generate() (Secret, error) {
	var s Secret
	if _, err := rand.Read(s.b[:]); err != nil {
		return Secret{}, fmt.Errorf("pairing: generate secret: %w", err)
	}
	s.valid = true
	return s, nil
}

// ParseHex parses 32 lowercase hex characters.
func ParseHex(h string) (Secret, error) {
	if len(h) != 2*SecretLen || strings.ToLower(h) != h {
		return Secret{}, errors.New("pairing: secret must be 32 lowercase hex characters")
	}
	var s Secret
	if _, err := hex.Decode(s.b[:], []byte(h)); err != nil {
		return Secret{}, errors.New("pairing: secret must be 32 lowercase hex characters")
	}
	s.valid = true
	return s, nil
}

// Valid reports whether s holds a generated or parsed secret.
func (s Secret) Valid() bool { return s.valid }

// Hex returns the secret as 32 lowercase hex characters. Never log it.
func (s Secret) Hex() string { return hex.EncodeToString(s.b[:]) }

// Bytes returns a copy of the raw 16 secret bytes (the HMAC key).
func (s Secret) Bytes() []byte {
	out := make([]byte, SecretLen)
	copy(out, s.b[:])
	return out
}

// String never reveals the secret.
func (s Secret) String() string { return redacted }

// GoString never reveals the secret, so %#v is safe too.
func (s Secret) GoString() string { return redacted }

// LogValue keeps the secret out of structured logs.
func (s Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// LoadOrCreate reads the secret stored at path, creating and storing a new one
// (file mode 0600, directory 0700) if the file does not exist. A file that
// exists but doesn't hold a valid secret is an error, not silently replaced,
// so a corrupt config never unpairs the phone behind the player's back.
func LoadOrCreate(path string) (Secret, error) {
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		s, perr := ParseHex(strings.TrimSpace(string(raw)))
		if perr != nil {
			return Secret{}, fmt.Errorf("pairing: %s: %w", path, perr)
		}
		// Tighten permissions if someone loosened them.
		if fi, serr := os.Stat(path); serr == nil && fi.Mode().Perm() != 0o600 {
			_ = os.Chmod(path, 0o600)
		}
		return s, nil
	case errors.Is(err, os.ErrNotExist):
		return Reset(path)
	default:
		return Secret{}, fmt.Errorf("pairing: read secret: %w", err)
	}
}

// Reset generates a new secret and stores it at path, replacing any old one.
func Reset(path string) (Secret, error) {
	s, err := Generate()
	if err != nil {
		return Secret{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Secret{}, fmt.Errorf("pairing: create secret dir: %w", err)
	}
	if err := writeAtomic(path, []byte(s.Hex()+"\n")); err != nil {
		return Secret{}, fmt.Errorf("pairing: write secret: %w", err)
	}
	return s, nil
}

// URL returns the pairing URL http://<ip>:<port>/#<hex>. IPv6 addresses are
// bracketed and a zone (fe80::1%wlan0) is percent-encoded per RFC 6874.
func URL(ip string, port int, s Secret) string {
	host := strings.ReplaceAll(ip, "%", "%25")
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/#" + s.Hex()
}

// LuaSource renders the Pairing.lua content for url.
func LuaSource(url string) ([]byte, error) {
	q, err := qrcode.New(url, qrcode.Medium)
	if err != nil {
		return nil, fmt.Errorf("pairing: qr encode: %w", err)
	}
	q.DisableBorder = true
	bm := q.Bitmap()

	var b strings.Builder
	b.WriteString("DeckChatPairing = { url = ")
	b.WriteString(luaQuote(url))
	b.WriteString(", size = ")
	b.WriteString(strconv.Itoa(len(bm)))
	b.WriteString(", rows = { ")
	row := make([]byte, 0, len(bm))
	for y, r := range bm {
		if y > 0 {
			b.WriteString(", ")
		}
		row = row[:0]
		for _, on := range r {
			if on {
				row = append(row, '1')
			} else {
				row = append(row, '0')
			}
		}
		b.WriteByte('"')
		b.Write(row)
		b.WriteByte('"')
	}
	b.WriteString(" } }\n")
	return []byte(b.String()), nil
}

// WriteLua atomically writes addonDir/Pairing.lua for url. The file holds the
// secret, so it is created with mode 0600.
func WriteLua(addonDir, url string) error {
	src, err := LuaSource(url)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(addonDir, LuaFileName), src); err != nil {
		return fmt.Errorf("pairing: write %s: %w", LuaFileName, err)
	}
	return nil
}

// luaQuote returns s as a double-quoted Lua string literal.
func luaQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, `\%03d`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// writeAtomic writes data to a temp file in path's directory with mode 0600,
// syncs it and renames it over path.
func writeAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if err = f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
