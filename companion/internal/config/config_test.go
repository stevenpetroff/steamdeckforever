package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	got := DefaultPath("/home/deck")
	want := "/home/deck/.config/deckchat/config.toml"
	if got != want {
		t.Fatalf("DefaultPath = %q, want %q", got, want)
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope", "config.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != DefaultPort || cfg.WoWPath != "" {
		t.Fatalf("got %+v, want defaults", cfg)
	}
}

func TestLoadValidFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, p, "port = 8123\nwow_path = \"/games/World of Warcraft/_forever_\"\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 8123 || cfg.WoWPath != "/games/World of Warcraft/_forever_" {
		t.Fatalf("got %+v", cfg)
	}
}

func TestLoadPartialFileKeepsDefaultPort(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, p, "wow_path = \"/x\"\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != DefaultPort {
		t.Fatalf("Port = %d, want %d", cfg.Port, DefaultPort)
	}
}

func TestLoadInvalidTOML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, p, "port = = 1\n")
	if _, err := Load(p); err == nil {
		t.Fatal("Load accepted invalid TOML")
	}
}

func TestLoadBadPort(t *testing.T) {
	for _, port := range []string{"80", "1023", "65536", "0", "-1"} {
		p := filepath.Join(t.TempDir(), "config.toml")
		writeFile(t, p, "port = "+port+"\n")
		if _, err := Load(p); err == nil {
			t.Errorf("Load accepted port %s", port)
		}
	}
}

func TestSaveLoadRoundTripAndMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deckchat")
	p := filepath.Join(dir, "config.toml")
	want := Config{Port: 9001, WoWPath: "/run/media/sd/World of Warcraft/_forever_"}
	if err := Save(p, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Fatalf("round trip got %+v, want %+v", got, want)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Fatalf("file mode = %o, want 600", mode)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := di.Mode().Perm(); mode != 0o700 {
		t.Fatalf("dir mode = %o, want 700", mode)
	}
	// Saving again overwrites in place and leaves no temp files behind.
	want.Port = 9002
	if err := Save(p, want); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want 1", len(entries))
	}
}

func TestSaveRejectsBadPort(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(p, Config{Port: 22}); err == nil {
		t.Fatal("Save accepted port 22")
	}
}

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}
