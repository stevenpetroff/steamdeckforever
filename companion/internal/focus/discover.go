package focus

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Candidate is a possible gamescope Xwayland display.
type Candidate struct {
	Display    string // e.g. ":0"
	XAuthority string // path, or "" if unknown
	Source     string // "proc:<pid>" or "socket"
	// HasFocusedApp is set by ranking: the display's root window carries
	// GAMESCOPE_FOCUSED_APP.
	HasFocusedApp bool
}

// FindGamescopeDisplay picks gamescope's Xwayland display and its
// Xauthority file at runtime.
//
// Best effort, and still needs confirmation on a real Deck in Game Mode
// (plan U1): gamescope runs one or more Xwayland servers (typically :0 for
// Steam and :1 for games) and exports DISPLAY/XAUTHORITY to the processes it
// launches, not necessarily to itself. Candidates come from the environment
// of processes named gamescope/gamescope-wl and their direct children, then
// from /tmp/.X11-unix/X* sockets. The first display whose root window has
// GAMESCOPE_FOCUSED_APP wins; otherwise the first candidate is returned.
func FindGamescopeDisplay() (display string, xauthority string, err error) {
	cs := FindGamescopeDisplays()
	if len(cs) == 0 {
		return "", "", errors.New("focus: no gamescope X display found")
	}
	return cs[0].Display, cs[0].XAuthority, nil
}

// FindGamescopeDisplays returns every candidate, best first.
func FindGamescopeDisplays() []Candidate {
	return rankCandidates(ScanCandidates("/"), probeFocusedApp)
}

func probeFocusedApp(c Candidate) bool {
	done := make(chan bool, 1)
	go func() {
		ch := NewGamescope(c.Display)
		ch.XAuthority = c.XAuthority
		defer ch.Close()
		st, err := ch.State()
		done <- err == nil && st.HasApp
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(3 * time.Second):
		return false
	}
}

// rankCandidates moves displays where probe is true to the front, keeping
// the scan order otherwise.
func rankCandidates(cs []Candidate, probe func(Candidate) bool) []Candidate {
	out := make([]Candidate, len(cs))
	copy(out, cs)
	for i := range out {
		out[i].HasFocusedApp = probe(out[i])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].HasFocusedApp && !out[j].HasFocusedApp })
	return out
}

type proc struct {
	pid  int
	ppid int
	comm string
}

func isGamescope(comm string) bool { return comm == "gamescope" || comm == "gamescope-wl" }

// ScanCandidates lists candidate displays found under the filesystem root
// (normally "/"; tests pass a fake tree). Order is stable: displays from
// gamescope-related processes in pid order, then remaining
// /tmp/.X11-unix/XN sockets in numeric order. Each display appears once;
// the first Xauthority seen for it is kept.
func ScanCandidates(root string) []Candidate {
	var out []Candidate
	index := map[string]int{}
	add := func(c Candidate) {
		if i, ok := index[c.Display]; ok {
			if out[i].XAuthority == "" {
				out[i].XAuthority = c.XAuthority
			}
			return
		}
		index[c.Display] = len(out)
		out = append(out, c)
	}

	procs := scanProcs(filepath.Join(root, "proc"))
	gs := map[int]bool{}
	for _, p := range procs {
		if isGamescope(p.comm) {
			gs[p.pid] = true
		}
	}
	for _, p := range procs {
		if !gs[p.pid] && !gs[p.ppid] {
			continue
		}
		env := readEnviron(filepath.Join(root, "proc", strconv.Itoa(p.pid), "environ"))
		if d := env["DISPLAY"]; d != "" {
			add(Candidate{Display: d, XAuthority: env["XAUTHORITY"], Source: "proc:" + strconv.Itoa(p.pid)})
		}
	}

	ents, _ := os.ReadDir(filepath.Join(root, "tmp", ".X11-unix"))
	var nums []int
	for _, e := range ents {
		if n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "X")); err == nil && strings.HasPrefix(e.Name(), "X") {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	for _, n := range nums {
		add(Candidate{Display: ":" + strconv.Itoa(n), Source: "socket"})
	}
	return out
}

func scanProcs(dir string) []proc {
	ents, _ := os.ReadDir(dir)
	var out []proc
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		p := proc{pid: pid}
		if b, err := os.ReadFile(filepath.Join(dir, e.Name(), "comm")); err == nil {
			p.comm = strings.TrimSpace(string(b))
		}
		if b, err := os.ReadFile(filepath.Join(dir, e.Name(), "stat")); err == nil {
			// "pid (comm) state ppid ..." — comm may contain spaces/parens.
			s := string(b)
			if i := strings.LastIndexByte(s, ')'); i >= 0 {
				if f := strings.Fields(s[i+1:]); len(f) >= 2 {
					p.ppid, _ = strconv.Atoi(f[1])
				}
			}
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pid < out[j].pid })
	return out
}

func readEnviron(path string) map[string]string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	env := map[string]string{}
	for _, kv := range bytes.Split(b, []byte{0}) {
		if k, v, ok := strings.Cut(string(kv), "="); ok {
			env[k] = v
		}
	}
	return env
}
