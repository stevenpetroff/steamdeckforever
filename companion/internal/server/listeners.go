package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"time"
)

const defaultListenInterval = 3 * time.Second

// Listeners keeps one TCP listener per private or link-local address of the
// Deck (plan KTD8). It never binds a wildcard address and runs fine with no
// address at all, so boot-before-Wi-Fi and suspend/resume just work.
type Listeners struct {
	Port int
	// Interval between address scans (default 3s).
	Interval time.Duration
	// IncludeLoopback also binds 127.0.0.1 (for development on the Deck).
	IncludeLoopback bool
	// Addrs lists the machine's interface addresses (default: net.Interfaces).
	Addrs func() ([]net.IPAddr, error)
	// Listen opens a listener (default: net.Listen).
	Listen func(network, address string) (net.Listener, error)
	// OnChange is called with the sorted set of bound IPs once at start and
	// again whenever that set changes (used to rewrite Pairing.lua). It is
	// called from Run's goroutine.
	OnChange func(ips []net.IP)
	Logger   *slog.Logger
}

type boundListener struct {
	ln net.Listener
	ip net.IP
}

// Run serves h on the tracked addresses until ctx is cancelled.
func (l *Listeners) Run(ctx context.Context, h http.Handler) error {
	interval := l.Interval
	if interval <= 0 {
		interval = defaultListenInterval
	}
	addrsFn := l.Addrs
	if addrsFn == nil {
		addrsFn = InterfaceAddrs
	}
	listen := l.Listen
	if listen == nil {
		listen = net.Listen
	}
	log := l.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelDebug),
	}
	var wg sync.WaitGroup
	bound := map[string]*boundListener{} // key: listen address
	first := true
	var lastSet []string

	rescan := func() {
		want := map[string]net.IP{}
		addrs, err := addrsFn()
		if err != nil {
			log.Warn("listing interface addresses failed", "err", err)
		}
		for _, a := range addrs {
			if !bindable(a.IP) {
				continue
			}
			host := a.IP.String()
			if a.Zone != "" && a.IP.IsLinkLocalUnicast() {
				host += "%" + a.Zone
			}
			want[net.JoinHostPort(host, strconv.Itoa(l.Port))] = a.IP
		}
		if l.IncludeLoopback {
			lo := net.IPv4(127, 0, 0, 1)
			want[net.JoinHostPort(lo.String(), strconv.Itoa(l.Port))] = lo
		}

		for addr, b := range bound {
			if _, ok := want[addr]; !ok {
				log.Info("closing listener", "addr", addr)
				b.ln.Close()
				delete(bound, addr)
			}
		}
		for addr, ip := range want {
			if _, ok := bound[addr]; ok {
				continue
			}
			ln, err := listen("tcp", addr)
			if err != nil {
				log.Warn("listen failed; will retry", "addr", addr, "err", err)
				continue
			}
			log.Info("listening", "addr", addr)
			bound[addr] = &boundListener{ln: ln, ip: ip}
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
					log.Debug("serve ended", "addr", addr, "err", err)
				}
			}()
		}

		ips := make([]net.IP, 0, len(bound))
		for _, b := range bound {
			ips = append(ips, b.ip)
		}
		slices.SortFunc(ips, func(a, b net.IP) int {
			x, _ := netip.AddrFromSlice(a)
			y, _ := netip.AddrFromSlice(b)
			return x.Unmap().Compare(y.Unmap())
		})
		set := make([]string, len(ips))
		for i, ip := range ips {
			set[i] = ip.String()
		}
		if first || !slices.Equal(set, lastSet) {
			first = false
			lastSet = set
			if l.OnChange != nil {
				l.OnChange(ips)
			}
		}
	}

	rescan()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			for _, b := range bound {
				b.ln.Close()
			}
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = srv.Shutdown(shutdownCtx)
			cancel()
			wg.Wait()
			return nil
		case <-t.C:
			rescan()
		}
	}
}

// bindable is true for private and link-local unicast addresses. Loopback,
// unspecified (0.0.0.0, ::), public and multicast addresses are never bound.
func bindable(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
		return false
	}
	return AllowedRemote(ip)
}

// InterfaceAddrs lists the unicast addresses of every up interface, with
// zones set for IPv6 link-local addresses.
func InterfaceAddrs() ([]net.IPAddr, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []net.IPAddr
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			ia := net.IPAddr{IP: ip}
			if ip.To4() == nil && ip.IsLinkLocalUnicast() {
				ia.Zone = ifc.Name
			}
			out = append(out, ia)
		}
	}
	return out, nil
}
