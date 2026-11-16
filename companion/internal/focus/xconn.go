package focus

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jezek/xgb"
)

// DialX connects to an X display. With an empty xauthority it behaves like
// xgb.NewConnDisplay (DISPLAY/XAUTHORITY from the environment). With a path,
// it reads the MIT-MAGIC-COOKIE-1 for that display from the file itself, so
// connecting to gamescope's Xwayland never mutates the process environment.
// Only local displays (":N", "unix:N") are supported with an explicit file.
func DialX(display, xauthority string) (*xgb.Conn, error) {
	if xauthority == "" {
		return xgb.NewConnDisplay(display)
	}
	num, ok := displayNumber(display)
	if !ok {
		return nil, fmt.Errorf("x11: display %q is not a local display", display)
	}
	file, err := os.ReadFile(xauthority)
	if err != nil {
		return nil, fmt.Errorf("x11: read xauthority: %w", err)
	}
	cookie, err := cookieFor(file, num)
	if err != nil {
		return nil, fmt.Errorf("x11: %s: %w", xauthority, err)
	}
	nc, err := net.DialTimeout("unix", "/tmp/.X11-unix/X"+num, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("x11: dial %s: %w", display, err)
	}
	c, err := xgb.NewConnNetWithCookieHex(nc, hex.EncodeToString(cookie))
	if err != nil {
		nc.Close()
		return nil, err
	}
	return c, nil
}

// displayNumber returns N for ":N", ":N.S" and "unix:N".
func displayNumber(display string) (string, bool) {
	rest, ok := strings.CutPrefix(display, "unix")
	if !ok {
		rest = display
	}
	rest, ok = strings.CutPrefix(rest, ":")
	if !ok {
		return "", false
	}
	if i := strings.IndexByte(rest, '.'); i >= 0 {
		rest = rest[:i]
	}
	if _, err := strconv.Atoi(rest); err != nil {
		return "", false
	}
	return rest, true
}

// cookieFor returns the first MIT-MAGIC-COOKIE-1 in an Xauthority file whose
// display number matches (an empty number matches any display). The address
// is not compared: gamescope's file is written for the local host, and the
// hostname can differ from what this process sees.
func cookieFor(file []byte, number string) ([]byte, error) {
	r := file
	next := func() ([]byte, error) {
		if len(r) < 2 {
			return nil, errors.New("truncated xauthority")
		}
		n := int(binary.BigEndian.Uint16(r))
		if len(r) < 2+n {
			return nil, errors.New("truncated xauthority")
		}
		v := r[2 : 2+n]
		r = r[2+n:]
		return v, nil
	}
	for len(r) > 0 {
		if len(r) < 2 {
			return nil, errors.New("truncated xauthority")
		}
		r = r[2:] // family
		var fields [4][]byte
		for i := range fields {
			v, err := next()
			if err != nil {
				return nil, err
			}
			fields[i] = v
		}
		num, name, data := string(fields[1]), string(fields[2]), fields[3]
		if (num == number || num == "") && name == "MIT-MAGIC-COOKIE-1" && len(data) == 16 {
			return data, nil
		}
	}
	return nil, errors.New("no MIT-MAGIC-COOKIE-1 for display :" + number)
}
