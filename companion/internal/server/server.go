// Package server serves the phone page and the authenticated chat WebSocket
// on the Deck's LAN addresses only (plan U6, KTD7, KTD8). The wire format is
// docs/protocol.md.
//
// The server never logs the pairing secret, MACs or chat text.
package server

import (
	"context"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/pairing"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/protocol"
)

// CSP is the Content-Security-Policy sent with every response.
const CSP = "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; font-src 'self'"

const (
	// MaxFrameBytes is the largest client frame accepted; larger frames
	// close the socket with 1009.
	MaxFrameBytes = 8 << 10
	// MaxHistory is the most messages sent in one history frame.
	MaxHistory = 500
	// StaleAfter is how far a send's ts may be from the server clock.
	StaleAfter = 10 * time.Second

	defaultAuthTimeout    = 5 * time.Second
	defaultStatusInterval = time.Second
	writeTimeout          = 10 * time.Second
)

// FeedEvent is one item from the chat feed: a new message, or a request to
// resend the full history because this subscriber fell behind.
type FeedEvent struct {
	Message *protocol.Message
	Resync  bool
}

// Feed is the chat hub as seen by the server.
type Feed interface {
	// Subscribe returns the current history (oldest first), a channel of
	// subsequent events and a cancel func. Closing the channel ends sessions.
	Subscribe() (history []protocol.Message, events <-chan FeedEvent, cancel func())
	// Snapshot returns the current history, oldest first.
	Snapshot() []protocol.Message
}

// Sender types one message into the game. Implementations own the
// per-id idempotency (a repeated id returns the first attempt's result).
type Sender interface {
	Send(ctx context.Context, id, text string) protocol.SendResult
}

// StatusSource reports service health for the phone status line.
type StatusSource interface {
	Status() protocol.Status
}

// SecretSource returns the current pairing secret. It is read on every new
// connection so a reset takes effect immediately.
type SecretSource interface {
	Current() pairing.Secret
}

// Options configures a Server.
type Options struct {
	Feed   Feed
	Sender Sender
	Status StatusSource
	Secret SecretSource
	// Static is the built phone page; index.html at its root.
	Static fs.FS
	// Port is the port the composition root listens on. The handler itself
	// doesn't depend on it; it is kept here so callers configure one place.
	Port   int
	Logger *slog.Logger
	Now    func() time.Time

	// AuthTimeout is how long a client has to send auth (default 5s).
	AuthTimeout time.Duration
	// StatusInterval is how often Status is polled for changes (default 1s).
	StatusInterval time.Duration
}

// Server is the phone-facing HTTP and WebSocket server.
type Server struct {
	opts    Options
	log     *slog.Logger
	handler http.Handler

	mu       sync.Mutex
	sessions map[*session]struct{}
}

// New builds a Server. Feed, Sender, Status and Secret are required.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.AuthTimeout <= 0 {
		opts.AuthTimeout = defaultAuthTimeout
	}
	if opts.StatusInterval <= 0 {
		opts.StatusInterval = defaultStatusInterval
	}
	s := &Server{
		opts:     opts,
		log:      opts.Logger,
		sessions: make(map[*session]struct{}),
	}

	var static http.Handler = http.NotFoundHandler()
	if opts.Static != nil {
		static = http.FileServerFS(opts.Static)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws", s.serveWS)
	mux.Handle("/", staticHandler(static))
	s.handler = s.guard(mux)
	return s
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// DisconnectAll closes every open WebSocket with 1008. Call it after a
// pairing reset so phones holding the old secret drop off.
func (s *Server) DisconnectAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ss := range s.sessions {
		ss.kill()
	}
	s.log.Info("disconnected all phone sessions", "count", len(s.sessions))
}

func (s *Server) register(ss *session) {
	s.mu.Lock()
	s.sessions[ss] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) unregister(ss *session) {
	s.mu.Lock()
	delete(s.sessions, ss)
	s.mu.Unlock()
}

// guard sets the security headers and drops requests from outside the LAN or
// addressed to a non-IP host name (DNS rebinding).
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", CSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if !allowedRemoteAddr(r.RemoteAddr) {
			s.log.Warn("rejected non-LAN client", "remote", r.RemoteAddr)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !allowedHost(r.Host) {
			s.log.Warn("rejected request for foreign host", "remote", r.RemoteAddr)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func staticHandler(fsrv http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Cache-Control", "no-store")
		}
		fsrv.ServeHTTP(w, r)
	})
}

// allowedHost accepts IP-literal hosts (what the pairing URL uses), localhost
// and mDNS .local names. Anything else could be a DNS-rebinding page.
func allowedHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "" {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "localhost" || strings.HasSuffix(host, ".local")
}

// originAllowed is true when Origin is absent or names the request's Host.
func originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
