package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/pairing"
	"github.com/stevenpetroff/steamdeckforever/companion/internal/protocol"
)

// ---- fakes ----

type fakeFeed struct {
	mu       sync.Mutex
	history  []protocol.Message
	snapshot []protocol.Message
	subs     chan chan FeedEvent
	canceled int
}

func newFakeFeed() *fakeFeed { return &fakeFeed{subs: make(chan chan FeedEvent, 8)} }

func (f *fakeFeed) Subscribe() ([]protocol.Message, <-chan FeedEvent, func()) {
	f.mu.Lock()
	h := append([]protocol.Message(nil), f.history...)
	f.mu.Unlock()
	ch := make(chan FeedEvent, 16)
	f.subs <- ch
	return h, ch, func() {
		f.mu.Lock()
		f.canceled++
		f.mu.Unlock()
	}
}

func (f *fakeFeed) Snapshot() []protocol.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.Message(nil), f.snapshot...)
}

func (f *fakeFeed) waitSub(t *testing.T) chan FeedEvent {
	t.Helper()
	select {
	case ch := <-f.subs:
		return ch
	case <-time.After(2 * time.Second):
		t.Fatal("no feed subscription")
		return nil
	}
}

type sendCall struct{ id, text string }

type fakeSender struct {
	mu     sync.Mutex
	calls  []sendCall
	result func(id, text string) protocol.SendResult
}

func (s *fakeSender) Send(ctx context.Context, id, text string) protocol.SendResult {
	s.mu.Lock()
	s.calls = append(s.calls, sendCall{id, text})
	fn := s.result
	s.mu.Unlock()
	if fn != nil {
		return fn(id, text)
	}
	return protocol.SendResult{ID: id, Outcome: protocol.OutcomeSent}
}

func (s *fakeSender) Calls() []sendCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sendCall(nil), s.calls...)
}

type fakeStatus struct {
	mu sync.Mutex
	st protocol.Status
}

func (f *fakeStatus) Status() protocol.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st
}

func (f *fakeStatus) Set(st protocol.Status) {
	f.mu.Lock()
	f.st = st
	f.mu.Unlock()
}

type fakeSecret struct {
	mu sync.Mutex
	s  pairing.Secret
}

func (f *fakeSecret) Current() pairing.Secret {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.s
}

func (f *fakeSecret) Set(s pairing.Secret) {
	f.mu.Lock()
	f.s = s
	f.mu.Unlock()
}

// ---- harness ----

var testNow = time.Date(2026, 11, 14, 18, 2, 11, 0, time.UTC)

type harness struct {
	srv    *Server
	ts     *httptest.Server
	feed   *fakeFeed
	sender *fakeSender
	status *fakeStatus
	secret *fakeSecret
	logs   *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func mustSecret(t *testing.T, h string) pairing.Secret {
	t.Helper()
	s, err := pairing.ParseHex(h)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newHarness(t *testing.T, mod func(*Options)) *harness {
	t.Helper()
	h := &harness{
		feed:   newFakeFeed(),
		sender: &fakeSender{},
		status: &fakeStatus{st: protocol.Status{WoWFocused: true, LogFresh: true, InputAvailable: true}},
		secret: &fakeSecret{s: mustSecret(t, "00112233445566778899aabbccddeeff")},
		logs:   &syncBuffer{},
	}
	opts := Options{
		Feed:           h.feed,
		Sender:         h.sender,
		Status:         h.status,
		Secret:         h.secret,
		Static:         fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>deckchat</title>")}, "app.js": {Data: []byte("console.log(1)")}},
		Logger:         slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now:            func() time.Time { return testNow },
		StatusInterval: 20 * time.Millisecond,
	}
	if mod != nil {
		mod(&opts)
	}
	h.srv = New(opts)
	h.ts = httptest.NewServer(h.srv.Handler())
	t.Cleanup(h.ts.Close)
	return h
}

func (h *harness) wsURL() string { return "ws" + strings.TrimPrefix(h.ts.URL, "http") + "/ws" }

func (h *harness) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, h.wsURL(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.SetReadLimit(1 << 20)
	t.Cleanup(func() { c.CloseNow() })
	return c
}

type frame struct {
	Type     string             `json:"type"`
	Nonce    string             `json:"nonce"`
	Messages []protocol.Message `json:"messages"`
	Message  *protocol.Message  `json:"message"`
	Status   *protocol.Status   `json:"status"`
	ID       string             `json:"id"`
	Outcome  string             `json:"outcome"`
	Reason   string             `json:"reason"`
}

func readFrame(t *testing.T, c *websocket.Conn) frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var f frame
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("bad frame %s: %v", b, err)
	}
	return f
}

func writeJSON(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// expectClose reads until the socket fails and asserts the close code. Any
// data frame received first is a failure.
func expectClose(t *testing.T, c *websocket.Conn, code websocket.StatusCode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err == nil {
		t.Fatalf("expected close %v, got frame %s", code, b)
	}
	if got := websocket.CloseStatus(err); got != code {
		t.Fatalf("close status = %v (%v), want %v", got, err, code)
	}
}

func readChallenge(t *testing.T, c *websocket.Conn) string {
	t.Helper()
	f := readFrame(t, c)
	if f.Type != "challenge" || len(f.Nonce) != 32 {
		t.Fatalf("want challenge with 32-hex nonce, got %+v", f)
	}
	return f.Nonce
}

// authed dials, authenticates and consumes the initial history and status.
func (h *harness) authed(t *testing.T) (*websocket.Conn, string, chan FeedEvent) {
	t.Helper()
	c := h.dial(t)
	nonce := readChallenge(t, c)
	writeJSON(t, c, map[string]string{"type": "auth", "mac": AuthMAC(h.secret.Current().Bytes(), nonce)})
	if f := readFrame(t, c); f.Type != "history" {
		t.Fatalf("first frame after auth = %+v, want history", f)
	}
	if f := readFrame(t, c); f.Type != "status" {
		t.Fatalf("second frame after auth = %+v, want status", f)
	}
	return c, nonce, h.feed.waitSub(t)
}

func sendFrame(key []byte, nonce, id string, ts int64, text string) map[string]any {
	return map[string]any{"type": "send", "id": id, "ts": ts, "text": text, "mac": SendMAC(key, nonce, id, ts, text)}
}

func msg(i int) protocol.Message {
	return protocol.Message{ID: strconv.Itoa(i), Time: "11/14 18:02:11.204", Channel: protocol.ChannelSay, Sender: "Thrall", Text: fmt.Sprintf("line %d", i)}
}

// ---- auth ----

func TestWrongAuthMACCloses1008NoHistory(t *testing.T) {
	h := newHarness(t, nil)
	h.feed.history = []protocol.Message{msg(1)}
	c := h.dial(t)
	nonce := readChallenge(t, c)
	writeJSON(t, c, map[string]string{"type": "auth", "mac": AuthMAC([]byte("wrong-key-wrong!"), nonce)})
	expectClose(t, c, websocket.StatusPolicyViolation)
}

func TestNonAuthFirstFrameCloses(t *testing.T) {
	h := newHarness(t, nil)
	c := h.dial(t)
	nonce := readChallenge(t, c)
	writeJSON(t, c, sendFrame(h.secret.Current().Bytes(), nonce, "a", testNow.UnixMilli(), "hi"))
	expectClose(t, c, websocket.StatusPolicyViolation)
	if n := len(h.sender.Calls()); n != 0 {
		t.Fatalf("sender calls = %d", n)
	}
}

func TestAuthTimeoutCloses(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.AuthTimeout = 100 * time.Millisecond })
	c := h.dial(t)
	readChallenge(t, c)
	start := time.Now()
	expectClose(t, c, websocket.StatusPolicyViolation)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("auth timeout took %v", d)
	}
}

func TestReplayedAuthMACRejected(t *testing.T) {
	h := newHarness(t, nil)
	c1 := h.dial(t)
	n1 := readChallenge(t, c1)
	mac1 := AuthMAC(h.secret.Current().Bytes(), n1)
	writeJSON(t, c1, map[string]string{"type": "auth", "mac": mac1})
	if f := readFrame(t, c1); f.Type != "history" {
		t.Fatalf("got %+v", f)
	}

	c2 := h.dial(t)
	n2 := readChallenge(t, c2)
	if n1 == n2 {
		t.Fatal("nonce reused across connections")
	}
	writeJSON(t, c2, map[string]string{"type": "auth", "mac": mac1})
	expectClose(t, c2, websocket.StatusPolicyViolation)
}

func TestValidAuthHistoryStatusThenLive(t *testing.T) {
	h := newHarness(t, nil)
	for i := 0; i < 600; i++ {
		h.feed.history = append(h.feed.history, msg(i))
	}
	c := h.dial(t)
	nonce := readChallenge(t, c)
	writeJSON(t, c, map[string]string{"type": "auth", "mac": AuthMAC(h.secret.Current().Bytes(), nonce)})

	f := readFrame(t, c)
	if f.Type != "history" || len(f.Messages) != 500 {
		t.Fatalf("history frame type=%s len=%d", f.Type, len(f.Messages))
	}
	if f.Messages[0].ID != "100" || f.Messages[499].ID != "599" {
		t.Fatalf("history window %s..%s, want 100..599", f.Messages[0].ID, f.Messages[499].ID)
	}
	f = readFrame(t, c)
	if f.Type != "status" || f.Status == nil || !f.Status.WoWFocused {
		t.Fatalf("want status, got %+v", f)
	}

	ch := h.feed.waitSub(t)
	for i := 1000; i < 1005; i++ {
		m := msg(i)
		ch <- FeedEvent{Message: &m}
	}
	for i := 1000; i < 1005; i++ {
		f := readFrame(t, c)
		if f.Type != "message" || f.Message == nil || f.Message.ID != strconv.Itoa(i) {
			t.Fatalf("live frame %d = %+v", i, f)
		}
	}
}

func TestResyncSendsFreshHistory(t *testing.T) {
	h := newHarness(t, nil)
	c, _, ch := h.authed(t)
	h.feed.mu.Lock()
	h.feed.snapshot = []protocol.Message{msg(7), msg(8)}
	h.feed.mu.Unlock()
	ch <- FeedEvent{Resync: true}
	f := readFrame(t, c)
	if f.Type != "history" || len(f.Messages) != 2 || f.Messages[1].ID != "8" {
		t.Fatalf("resync frame = %+v", f)
	}
}

func TestStatusChangePushed(t *testing.T) {
	h := newHarness(t, nil)
	c, _, _ := h.authed(t)
	h.status.Set(protocol.Status{WoWFocused: false, LogFresh: true, InputAvailable: true})
	f := readFrame(t, c)
	if f.Type != "status" || f.Status == nil || f.Status.WoWFocused {
		t.Fatalf("status frame = %+v", f)
	}
}

// ---- send ----

func TestSendMissingOrWrongMACCloses(t *testing.T) {
	cases := map[string]func(key []byte, nonce string) map[string]any{
		"missing": func(key []byte, nonce string) map[string]any {
			m := sendFrame(key, nonce, "abc", testNow.UnixMilli(), "hi")
			delete(m, "mac")
			return m
		},
		"wrong-key": func(_ []byte, nonce string) map[string]any {
			return sendFrame([]byte("0000000000000000"), nonce, "abc", testNow.UnixMilli(), "hi")
		},
		"tampered-text": func(key []byte, nonce string) map[string]any {
			m := sendFrame(key, nonce, "abc", testNow.UnixMilli(), "hi")
			m["text"] = "/dance"
			return m
		},
		"old-nonce": func(key []byte, _ string) map[string]any {
			return sendFrame(key, "ffffffffffffffffffffffffffffffff", "abc", testNow.UnixMilli(), "hi")
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, nil)
			c, nonce, _ := h.authed(t)
			writeJSON(t, c, mk(h.secret.Current().Bytes(), nonce))
			expectClose(t, c, websocket.StatusPolicyViolation)
			if n := len(h.sender.Calls()); n != 0 {
				t.Fatalf("sender calls = %d, want 0", n)
			}
		})
	}
}

func TestSendInvalidIDCloses(t *testing.T) {
	for _, id := range []string{"", "has space", "semi;colon", strings.Repeat("a", 65), "ünï"} {
		t.Run(id, func(t *testing.T) {
			h := newHarness(t, nil)
			c, nonce, _ := h.authed(t)
			writeJSON(t, c, sendFrame(h.secret.Current().Bytes(), nonce, id, testNow.UnixMilli(), "hi"))
			expectClose(t, c, websocket.StatusPolicyViolation)
			if n := len(h.sender.Calls()); n != 0 {
				t.Fatalf("sender calls = %d", n)
			}
		})
	}
}

func TestSendStale(t *testing.T) {
	for name, off := range map[string]time.Duration{"old": -11 * time.Second, "future": 11 * time.Second} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, nil)
			c, nonce, _ := h.authed(t)
			writeJSON(t, c, sendFrame(h.secret.Current().Bytes(), nonce, "s1", testNow.Add(off).UnixMilli(), "hi"))
			f := readFrame(t, c)
			if f.Type != "send_result" || f.ID != "s1" || f.Outcome != "not_sent" || f.Reason != "stale" {
				t.Fatalf("got %+v", f)
			}
			if n := len(h.sender.Calls()); n != 0 {
				t.Fatalf("sender calls = %d", n)
			}
		})
	}
}

func TestSendWithinWindowAccepted(t *testing.T) {
	h := newHarness(t, nil)
	c, nonce, _ := h.authed(t)
	writeJSON(t, c, sendFrame(h.secret.Current().Bytes(), nonce, "edge", testNow.Add(-9*time.Second).UnixMilli(), "hi"))
	if f := readFrame(t, c); f.Outcome != "sent" {
		t.Fatalf("got %+v", f)
	}
}

func TestValidSend(t *testing.T) {
	h := newHarness(t, nil)
	h.sender.result = func(id, text string) protocol.SendResult {
		if text == "/w Thrall nope" {
			return protocol.SendResult{ID: id, Outcome: protocol.OutcomeNotSent, Reason: protocol.ReasonNotFocused}
		}
		return protocol.SendResult{ID: id, Outcome: protocol.OutcomeSent}
	}
	c, nonce, _ := h.authed(t)
	key := h.secret.Current().Bytes()
	writeJSON(t, c, sendFrame(key, nonce, "abc_1-Z", testNow.UnixMilli(), "/w Thrall hi | there"))
	f := readFrame(t, c)
	if f.Type != "send_result" || f.ID != "abc_1-Z" || f.Outcome != "sent" || f.Reason != "" {
		t.Fatalf("got %+v", f)
	}
	calls := h.sender.Calls()
	if len(calls) != 1 || calls[0] != (sendCall{"abc_1-Z", "/w Thrall hi | there"}) {
		t.Fatalf("calls = %+v", calls)
	}

	writeJSON(t, c, sendFrame(key, nonce, "n2", testNow.UnixMilli(), "/w Thrall nope"))
	f = readFrame(t, c)
	if f.Outcome != "not_sent" || f.Reason != "not_focused" || f.ID != "n2" {
		t.Fatalf("got %+v", f)
	}
}

func TestSendsRunConcurrently(t *testing.T) {
	h := newHarness(t, nil)
	release := make(chan struct{})
	h.sender.result = func(id, text string) protocol.SendResult {
		if id == "slow" {
			<-release
		}
		return protocol.SendResult{ID: id, Outcome: protocol.OutcomeSent}
	}
	c, nonce, ch := h.authed(t)
	key := h.secret.Current().Bytes()
	writeJSON(t, c, sendFrame(key, nonce, "slow", testNow.UnixMilli(), "a"))
	writeJSON(t, c, sendFrame(key, nonce, "fast", testNow.UnixMilli(), "b"))
	if f := readFrame(t, c); f.ID != "fast" {
		t.Fatalf("want fast result first, got %+v", f)
	}
	// Live messages still flow while a send is in progress.
	m := msg(42)
	ch <- FeedEvent{Message: &m}
	if f := readFrame(t, c); f.Type != "message" {
		t.Fatalf("got %+v", f)
	}
	close(release)
	if f := readFrame(t, c); f.ID != "slow" {
		t.Fatalf("got %+v", f)
	}
}

func TestOversizeFrameCloses(t *testing.T) {
	h := newHarness(t, nil)
	c, _, _ := h.authed(t)
	big := `{"type":"send","text":"` + strings.Repeat("x", 9000) + `"}`
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.Write(ctx, websocket.MessageText, []byte(big))
	expectClose(t, c, websocket.StatusMessageTooBig)
}

// ---- reset / disconnect ----

func TestDisconnectAllAndReset(t *testing.T) {
	h := newHarness(t, nil)
	c1, _, _ := h.authed(t)
	c2, _, _ := h.authed(t)
	oldKey := h.secret.Current().Bytes()

	h.secret.Set(mustSecret(t, "ffeeddccbbaa99887766554433221100"))
	h.srv.DisconnectAll()
	expectClose(t, c1, websocket.StatusPolicyViolation)
	expectClose(t, c2, websocket.StatusPolicyViolation)

	c3 := h.dial(t)
	nonce := readChallenge(t, c3)
	writeJSON(t, c3, map[string]string{"type": "auth", "mac": AuthMAC(oldKey, nonce)})
	expectClose(t, c3, websocket.StatusPolicyViolation)

	// The new secret works.
	h.authed(t)
}

func TestLogsNeverContainSecret(t *testing.T) {
	h := newHarness(t, nil)
	c, nonce, _ := h.authed(t)
	key := h.secret.Current().Bytes()
	writeJSON(t, c, sendFrame(key, nonce, "x1", testNow.UnixMilli(), "hi"))
	readFrame(t, c)
	writeJSON(t, c, sendFrame([]byte("bad-key-bad-key!"), nonce, "x2", testNow.UnixMilli(), "hi"))
	expectClose(t, c, websocket.StatusPolicyViolation)
	h.srv.DisconnectAll()
	time.Sleep(50 * time.Millisecond)
	logs := h.logs.String()
	if logs == "" {
		t.Fatal("expected some log output")
	}
	if strings.Contains(logs, h.secret.Current().Hex()) {
		t.Fatalf("logs contain the secret:\n%s", logs)
	}
}

// ---- HTTP surface ----

func TestForeignOriginRejected(t *testing.T) {
	h := newHarness(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, h.wsURL(), &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"http://evil.example"}}})
	if err == nil {
		t.Fatal("dial with foreign origin succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("resp = %+v", resp)
	}

	// Same-origin is fine.
	c, _, err := websocket.Dial(ctx, h.wsURL(), &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {h.ts.URL}}})
	if err != nil {
		t.Fatalf("same-origin dial: %v", err)
	}
	c.CloseNow()
}

func TestLANGuardOnHandler(t *testing.T) {
	h := newHarness(t, nil)
	for _, path := range []string{"/", "/ws", "/app.js"} {
		for remote, want := range map[string]int{"8.8.8.8:5555": 403, "[2001:db8::1]:5555": 403, "192.168.1.20:5555": 200} {
			if path == "/ws" && want == 200 {
				continue // a plain GET isn't an upgrade
			}
			r := httptest.NewRequest("GET", path, nil)
			r.RemoteAddr = remote
			r.Host = "192.168.1.5:7480"
			w := httptest.NewRecorder()
			h.srv.Handler().ServeHTTP(w, r)
			if w.Code != want {
				t.Errorf("%s from %s = %d, want %d", path, remote, w.Code, want)
			}
		}
	}
}

func TestDNSRebindingHostRejected(t *testing.T) {
	h := newHarness(t, nil)
	for host, want := range map[string]int{"evil.example": 403, "evil.example:7480": 403, "192.168.1.5:7480": 200, "[fe80::1]:7480": 200, "steamdeck.local:7480": 200, "localhost:7480": 200} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "192.168.1.20:5555"
		r.Host = host
		w := httptest.NewRecorder()
		h.srv.Handler().ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("Host %s = %d, want %d", host, w.Code, want)
		}
		if w.Header().Get("Content-Security-Policy") != CSP {
			t.Errorf("Host %s: missing CSP", host)
		}
	}
}

func TestStaticHeaders(t *testing.T) {
	h := newHarness(t, nil)
	for _, path := range []string{"/", "/app.js"} {
		resp, err := http.Get(h.ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s status %d", path, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Security-Policy"); got != CSP {
			t.Errorf("%s CSP = %q", path, got)
		}
		if !strings.HasPrefix(resp.Header.Get("Content-Security-Policy"), "default-src 'self'") {
			t.Errorf("%s CSP missing default-src 'self'", path)
		}
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s missing hardening headers: %v", path, resp.Header)
		}
		if path == "/" && resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("index Cache-Control = %q", resp.Header.Get("Cache-Control"))
		}
	}
}

func TestEmbeddedWebCompiles(t *testing.T) {
	if WebFS() == nil {
		t.Fatal("WebFS returned nil")
	}
}
