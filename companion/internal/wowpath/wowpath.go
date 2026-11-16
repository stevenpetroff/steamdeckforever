// Package wowpath finds the WoW Forever install inside Steam's Proton
// prefixes on a Steam Deck.
package wowpath

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ErrNotFound means no WoW Forever install was discovered.
var ErrNotFound = errors.New("wowpath: WoW Forever install not found")

const (
	wowDirName     = "World of Warcraft"
	foreverDirName = "_forever_"
	// maxDepth bounds how deep below a drive root the search descends to find
	// the "World of Warcraft" directory (e.g. "Program Files (x86)/World of Warcraft" is 2).
	maxDepth = 6
)

// Install describes one WoW Forever client directory.
type Install struct {
	Root      string // .../World of Warcraft/_forever_
	LogPath   string // Root/Logs/WoWChatLog.txt
	AddOnsDir string // Root/Interface/AddOns
	ModTime   time.Time
}

func newInstall(root string, mod time.Time) Install {
	return Install{
		Root:      root,
		LogPath:   filepath.Join(root, "Logs", "WoWChatLog.txt"),
		AddOnsDir: filepath.Join(root, "Interface", "AddOns"),
		ModTime:   mod,
	}
}

// driveLetter matches Wine dosdevices drive links like "d:" (not raw "d::" devices).
var driveLetter = regexp.MustCompile(`^[a-y]:$`)

// skipDirs are large directories under a drive root that never hold the game.
var skipDirs = map[string]bool{"windows": true, "AppData": true, "ProgramData": true}

func compatdata(home string) string {
	return filepath.Join(home, ".local", "share", "Steam", "steamapps", "compatdata")
}

// SearchedGlobs describes the locations Find searches, for error messages.
func SearchedGlobs(home string) []string {
	base := compatdata(home)
	tail := filepath.Join("**", wowDirName, foreverDirName)
	return []string{
		filepath.Join(base, "*", "pfx", "drive_c", tail),
		filepath.Join(base, "*", "pfx", "dosdevices", "*:", tail),
	}
}

// Find searches every Proton prefix under home for WoW Forever installs.
// It searches each prefix's drive_c and the targets of its dosdevices drive
// links (e.g. d: -> /run/media/...). Results are de-duplicated by real path and
// sorted most recently modified first. A missing compatdata dir yields no results.
func Find(home string) ([]Install, error) {
	prefixes, err := os.ReadDir(compatdata(home))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("wowpath: %w", err)
	}

	searched := map[string]bool{}
	found := map[string]Install{}
	for _, p := range prefixes {
		pfx := filepath.Join(compatdata(home), p.Name(), "pfx")
		for _, r := range driveRoots(pfx) {
			real, err := filepath.EvalSymlinks(r)
			if err != nil || searched[real] || unsafeRoot(real) {
				continue
			}
			searched[real] = true
			search(real, 0, found)
		}
	}

	out := make([]Install, 0, len(found))
	for _, in := range found {
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ModTime.Equal(out[j].ModTime) {
			return out[i].ModTime.After(out[j].ModTime)
		}
		return out[i].Root < out[j].Root
	})
	return out, nil
}

// driveRoots lists drive_c plus every dosdevices drive-letter link of a prefix.
func driveRoots(pfx string) []string {
	roots := []string{filepath.Join(pfx, "drive_c")}
	entries, err := os.ReadDir(filepath.Join(pfx, "dosdevices"))
	if err != nil {
		return roots
	}
	for _, e := range entries {
		if driveLetter.MatchString(e.Name()) {
			roots = append(roots, filepath.Join(pfx, "dosdevices", e.Name()))
		}
	}
	return roots
}

// unsafeRoot rejects drive targets that would make the search walk system trees
// (Wine's z: maps to /, which the driveLetter pattern already excludes).
func unsafeRoot(p string) bool {
	if p == "/" {
		return true
	}
	for _, sys := range []string{"/dev", "/proc", "/sys"} {
		if p == sys || strings.HasPrefix(p, sys+"/") {
			return true
		}
	}
	return false
}

// search walks dir up to maxDepth, recording "World of Warcraft/_forever_" dirs.
// It does not follow symlinked subdirectories and ignores unreadable dirs.
func search(dir string, depth int, found map[string]Install) {
	if depth > maxDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || skipDirs[e.Name()] {
			continue
		}
		child := filepath.Join(dir, e.Name())
		if e.Name() == wowDirName {
			if in, ok := installAt(filepath.Join(child, foreverDirName)); ok {
				found[in.Root] = in
				continue
			}
		}
		search(child, depth+1, found)
	}
}

// installAt returns the Install rooted at root if it is an existing directory.
// Root is the resolved real path so the same install found twice de-duplicates.
func installAt(root string) (Install, bool) {
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Install{}, false
	}
	fi, err := os.Stat(real)
	if err != nil || !fi.IsDir() {
		return Install{}, false
	}
	return newInstall(real, fi.ModTime()), true
}

// Resolve picks the install to use. A non-empty override must be an existing
// _forever_ directory (no fallback to discovery). Otherwise it returns the most
// recently modified discovered install plus every install found.
func Resolve(home, override string) (Install, []Install, error) {
	if override != "" {
		fi, err := os.Stat(override)
		if err != nil {
			return Install{}, nil, fmt.Errorf("wowpath: configured wow_path %q: %w", override, err)
		}
		if !fi.IsDir() {
			return Install{}, nil, fmt.Errorf("wowpath: configured wow_path %q is not a directory", override)
		}
		in := newInstall(override, fi.ModTime())
		return in, []Install{in}, nil
	}
	all, err := Find(home)
	if err != nil {
		return Install{}, nil, err
	}
	if len(all) == 0 {
		return Install{}, nil, fmt.Errorf("%w; searched %s", ErrNotFound, strings.Join(SearchedGlobs(home), " and "))
	}
	return all[0], all, nil
}
