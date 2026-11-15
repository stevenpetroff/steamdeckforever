package server

import (
	"net"
	"net/netip"
)

// allowedPrefixes are the private and link-local ranges the server talks to
// (plan KTD8). Loopback is allowed too so the page can be opened on the Deck
// itself and in local tests; the listener manager never binds loopback unless
// asked.
var allowedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("::1/128"),
}

// AllowedRemote reports whether ip is a private, link-local or loopback
// address. IPv4-mapped IPv6 addresses are judged by their IPv4 form.
// Everything else, including unspecified and multicast addresses, is refused.
func AllowedRemote(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	return allowedAddr(a)
}

func allowedAddr(a netip.Addr) bool {
	a = a.Unmap().WithZone("")
	for _, p := range allowedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// allowedRemoteAddr applies AllowedRemote to an http.Request.RemoteAddr.
func allowedRemoteAddr(remote string) bool {
	ap, err := netip.ParseAddrPort(remote)
	if err != nil {
		host, _, serr := net.SplitHostPort(remote)
		if serr != nil {
			return false
		}
		a, perr := netip.ParseAddr(host)
		if perr != nil {
			return false
		}
		return allowedAddr(a)
	}
	return allowedAddr(ap.Addr())
}
