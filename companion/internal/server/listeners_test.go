package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

type fakeNet struct {
	mu        sync.Mutex
	addrs     []net.IPAddr
	requested []string
	real      map[string]net.Listener // requested addr -> real loopback listener
	failNext  bool
}

func (f *fakeNet) set(addrs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addrs = nil
	for _, a := range addrs {
		f.addrs = append(f.addrs, net.IPAddr{IP: net.ParseIP(a)})
	}
}

func (f *fakeNet) Addrs() ([]net.IPAddr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]net.IPAddr(nil), f.addrs...), nil
}

func (f *fakeNet) Listen(network, addr string) (net.Listener, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requested = append(f.requested, addr)
	if f.failNext {
		f.failNext = false
		return nil, errors.New("cannot assign requested address")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f.real[addr] = ln
	return ln, nil
}

func (f *fakeNet) realAddr(t *testing.T, addr string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	ln, ok := f.real[addr]
	if !ok {
		t.Fatalf("no listener for %s (requested %v)", addr, f.requested)
	}
	return ln.Addr().String()
}

func waitChange(t *testing.T, ch <-chan []net.IP) []net.IP {
	t.Helper()
	select {
	case ips := <-ch:
		return ips
	case <-time.After(3 * time.Second):
		t.Fatal("OnChange not called")
		return nil
	}
}

func ipStrings(ips []net.IP) []string {
	out := []string{}
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out
}

func startListeners(t *testing.T, fn *fakeNet, mod func(*Listeners)) <-chan []net.IP {
	t.Helper()
	changes := make(chan []net.IP, 16)
	l := &Listeners{
		Port:     7480,
		Interval: 10 * time.Millisecond,
		Addrs:    fn.Addrs,
		Listen:   fn.Listen,
		OnChange: func(ips []net.IP) { changes <- ips },
	}
	if mod != nil {
		mod(l)
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.Run(ctx, h) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("Run did not return after cancel")
		}
	})
	return changes
}

func TestListenersFollowAddresses(t *testing.T) {
	fn := &fakeNet{real: map[string]net.Listener{}}
	changes := startListeners(t, fn, nil)

	if ips := waitChange(t, changes); len(ips) != 0 {
		t.Fatalf("initial OnChange = %v, want empty", ips)
	}

	// Wildcards, public, and loopback addresses must be ignored.
	fn.set("0.0.0.0", "::", "8.8.8.8", "127.0.0.1", "::1", "192.168.1.20")
	if got := fmt.Sprint(ipStrings(waitChange(t, changes))); got != "[192.168.1.20]" {
		t.Fatalf("OnChange = %s, want [192.168.1.20]", got)
	}
	real := fn.realAddr(t, "192.168.1.20:7480")
	resp, err := http.Get("http://" + real + "/")
	if err != nil {
		t.Fatalf("listener not serving: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}

	fn.set()
	if ips := waitChange(t, changes); len(ips) != 0 {
		t.Fatalf("OnChange = %v, want empty", ips)
	}
	if c, err := net.DialTimeout("tcp", real, 200*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("listener still accepting after address vanished")
	}

	fn.mu.Lock()
	defer fn.mu.Unlock()
	for _, a := range fn.requested {
		host, _, _ := net.SplitHostPort(a)
		ip := net.ParseIP(host)
		if host == "" || ip == nil || ip.IsUnspecified() || !ip.IsPrivate() {
			t.Errorf("bound unexpected address %q", a)
		}
	}
	if len(fn.requested) != 1 {
		t.Errorf("requested = %v, want exactly one bind", fn.requested)
	}
}

func TestListenersLoopbackFlagAndRetry(t *testing.T) {
	fn := &fakeNet{real: map[string]net.Listener{}, failNext: true}
	fn.set("10.0.0.5")
	changes := startListeners(t, fn, func(l *Listeners) { l.IncludeLoopback = true })

	// First attempt for one of the addresses fails; it must be retried.
	var got string
	for i := 0; i < 3; i++ {
		got = fmt.Sprint(ipStrings(waitChange(t, changes)))
		if got == "[10.0.0.5 127.0.0.1]" {
			break
		}
	}
	if got != "[10.0.0.5 127.0.0.1]" {
		t.Fatalf("OnChange = %s, want [10.0.0.5 127.0.0.1]", got)
	}
}
