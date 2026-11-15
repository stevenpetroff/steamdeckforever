package server

import (
	"net"
	"testing"
)

func TestAllowedRemote(t *testing.T) {
	cases := map[string]bool{
		"192.168.1.20":         true,
		"10.0.0.5":             true,
		"172.16.4.2":           true,
		"172.31.255.255":       true,
		"169.254.10.1":         true,
		"fe80::1":              true,
		"fd12:3456::1":         true,
		"fc00::1":              true,
		"127.0.0.1":            true,
		"::1":                  true,
		"::ffff:192.168.1.20":  true,
		"::ffff:10.0.0.5":      true,
		"8.8.8.8":              false,
		"2001:db8::1":          false,
		"172.32.0.1":           false,
		"172.15.255.255":       false,
		"192.169.0.1":          false,
		"100.64.0.1":           false,
		"0.0.0.0":              false,
		"::":                   false,
		"::ffff:8.8.8.8":       false,
		"2606:4700:4700::1111": false,
		"224.0.0.1":            false,
		"255.255.255.255":      false,
	}
	for s, want := range cases {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("bad test ip %q", s)
		}
		if got := AllowedRemote(ip); got != want {
			t.Errorf("AllowedRemote(%s) = %v, want %v", s, got, want)
		}
	}
	if AllowedRemote(nil) {
		t.Error("AllowedRemote(nil) = true")
	}
}

func TestAllowedRemoteAddr(t *testing.T) {
	cases := map[string]bool{
		"192.168.1.20:5000":    true,
		"[fe80::1%wlan0]:5000": true,
		"[2001:db8::1]:5000":   false,
		"8.8.8.8:1":            false,
		"garbage":              false,
		"":                     false,
	}
	for s, want := range cases {
		if got := allowedRemoteAddr(s); got != want {
			t.Errorf("allowedRemoteAddr(%q) = %v, want %v", s, got, want)
		}
	}
}
