package wowpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeInstall creates <base>/<rel>/World of Warcraft/_forever_ and sets its mtime.
func makeInstall(t *testing.T, base, rel string, mtime time.Time) string {
	t.Helper()
	root := filepath.Join(base, rel, "World of Warcraft", "_forever_")
	if err := os.MkdirAll(filepath.Join(root, "Interface", "AddOns"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(root, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return root
}

func prefix(home, appID string) string {
	return filepath.Join(home, ".local", "share", "Steam", "steamapps", "compatdata", appID, "pfx")
}

func realpath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestFindOnePrefix(t *testing.T) {
	home := t.TempDir()
	root := makeInstall(t, filepath.Join(prefix(home, "1234567"), "drive_c"), "Program Files (x86)", time.Now())

	got, err := Find(home)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d installs, want 1: %+v", len(got), got)
	}
	in := got[0]
	if realpath(t, in.Root) != realpath(t, root) {
		t.Fatalf("Root = %q, want %q", in.Root, root)
	}
	if in.LogPath != filepath.Join(in.Root, "Logs", "WoWChatLog.txt") {
		t.Errorf("LogPath = %q", in.LogPath)
	}
	if in.AddOnsDir != filepath.Join(in.Root, "Interface", "AddOns") {
		t.Errorf("AddOnsDir = %q", in.AddOnsDir)
	}
	if in.ModTime.IsZero() {
		t.Error("ModTime is zero")
	}
}

func TestFindTwoPrefixesNewestFirst(t *testing.T) {
	home := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	oldRoot := makeInstall(t, filepath.Join(prefix(home, "111"), "drive_c"), "Program Files (x86)", old)
	newRoot := makeInstall(t, filepath.Join(prefix(home, "222"), "drive_c"), "Games", newer)

	got, err := Find(home)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d installs, want 2: %+v", len(got), got)
	}
	if realpath(t, got[0].Root) != realpath(t, newRoot) || realpath(t, got[1].Root) != realpath(t, oldRoot) {
		t.Fatalf("order wrong: %q, %q", got[0].Root, got[1].Root)
	}

	in, all, err := Resolve(home, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if realpath(t, in.Root) != realpath(t, newRoot) || len(all) != 2 {
		t.Fatalf("Resolve picked %q of %d", in.Root, len(all))
	}
}

func TestFindFollowsDosdevicesSymlink(t *testing.T) {
	home := t.TempDir()
	sd := t.TempDir() // stands in for /run/media/deck/SD
	root := makeInstall(t, sd, "Games", time.Now())

	pfx := prefix(home, "999")
	if err := os.MkdirAll(filepath.Join(pfx, "drive_c"), 0o755); err != nil {
		t.Fatal(err)
	}
	dd := filepath.Join(pfx, "dosdevices")
	if err := os.MkdirAll(dd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../drive_c", filepath.Join(dd, "c:")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sd, filepath.Join(dd, "d:")); err != nil {
		t.Fatal(err)
	}

	got, err := Find(home)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d installs, want 1: %+v", len(got), got)
	}
	if realpath(t, got[0].Root) != realpath(t, root) {
		t.Fatalf("Root = %q, want %q", got[0].Root, root)
	}
}

func TestFindDeduplicatesSameInstall(t *testing.T) {
	home := t.TempDir()
	pfx := prefix(home, "555")
	makeInstall(t, filepath.Join(pfx, "drive_c"), "Games", time.Now())
	dd := filepath.Join(pfx, "dosdevices")
	if err := os.MkdirAll(dd, 0o755); err != nil {
		t.Fatal(err)
	}
	// c: points back at drive_c, so the same install is reachable twice.
	if err := os.Symlink("../drive_c", filepath.Join(dd, "c:")); err != nil {
		t.Fatal(err)
	}
	got, err := Find(home)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d installs, want 1 (dedup): %+v", len(got), got)
	}
}

func TestFindIgnoresWoWWithoutForever(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(prefix(home, "1"), "drive_c", "Games", "World of Warcraft", "_classic_")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Find(home)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want none", got)
	}
}

func TestResolveOverrideExists(t *testing.T) {
	home := t.TempDir()
	root := makeInstall(t, t.TempDir(), "x", time.Now())
	in, _, err := Resolve(home, root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Root != root || in.LogPath != filepath.Join(root, "Logs", "WoWChatLog.txt") {
		t.Fatalf("got %+v", in)
	}
}

func TestResolveOverrideMissingNoFallback(t *testing.T) {
	home := t.TempDir()
	// A discoverable install exists, but a bad override must not fall back to it.
	makeInstall(t, filepath.Join(prefix(home, "1"), "drive_c"), "Games", time.Now())
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, _, err := Resolve(home, missing)
	if err == nil {
		t.Fatal("Resolve accepted a missing override")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("missing override should not be ErrNotFound")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error %q does not name the override path", err)
	}
}

func TestResolveNoneFound(t *testing.T) {
	home := t.TempDir()
	_, _, err := Resolve(home, "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "compatdata/*/pfx/drive_c") ||
		!strings.Contains(err.Error(), "dosdevices") {
		t.Fatalf("error %q does not name the searched globs", err)
	}
}
