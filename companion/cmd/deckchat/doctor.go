package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/config"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/focus"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/input"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/pairing"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/server"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/wowpath"
)

// doctor prints what the service would use. It never prints the pairing
// secret or the full pairing URL.
func doctor(ctx context.Context) error {
	ok := true
	check := func(pass bool, label, detail string) {
		mark := "ok  "
		if !pass {
			mark, ok = "FAIL", false
		}
		fmt.Printf("[%s] %-18s %s\n", mark, label, detail)
	}

	p, err := userPaths()
	if err != nil {
		return err
	}
	cfg, err := config.Load(p.config)
	check(err == nil, "config", fmt.Sprintf("%s (port %d)", p.config, cfg.Port))

	_, err = pairing.LoadOrCreate(p.secret)
	check(err == nil, "pairing secret", p.secret)

	inst, all, err := wowpath.Resolve(p.home, cfg.WoWPath)
	if err != nil {
		check(false, "WoW Forever", err.Error())
	} else {
		check(true, "WoW Forever", fmt.Sprintf("%s (%d found)", inst.Root, len(all)))
		_, lerr := os.Stat(inst.LogPath)
		check(lerr == nil, "chat log", inst.LogPath)
		addon := filepath.Join(inst.AddOnsDir, "DeckChat")
		_, aerr := os.Stat(filepath.Join(addon, "DeckChat.toc"))
		check(aerr == nil, "add-on", addon)
		_, perr := os.Stat(filepath.Join(addon, pairing.LuaFileName))
		check(perr == nil, "pairing file", "written by the running service")
	}

	kb, err := input.OpenUinput()
	if err == nil {
		kb.Close()
		check(true, "virtual keyboard", "/dev/uinput is writable")
	} else {
		check(false, "virtual keyboard", err.Error()+" — re-run the installer")
	}

	cands := focus.FindGamescopeDisplays()
	if len(cands) == 0 {
		check(false, "game display", "no gamescope display found (is Game Mode running?)")
	} else {
		c := cands[0]
		fc := focus.NewGamescope(c.Display)
		fc.XAuthority = c.XAuthority
		st, serr := fc.State()
		fc.Close()
		detail := fmt.Sprintf("%s via %s, focused window %q, WoW focused: %v", c.Display, c.Source, st.Name, st.Focused)
		check(serr == nil && c.HasFocusedApp, "game display", detail)
	}

	ips, err := server.InterfaceAddrs()
	var lan []string
	for _, a := range ips {
		if server.AllowedRemote(a.IP) && !a.IP.IsLoopback() {
			lan = append(lan, a.IP.String())
		}
	}
	check(err == nil && len(lan) > 0, "local network", fmt.Sprint(lan))

	if !ok {
		return fmt.Errorf("some checks failed")
	}
	return nil
}
