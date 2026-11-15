package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/protocol"
)

// Server -> client frames.

type challengeFrame struct {
	Type  string `json:"type"`
	Nonce string `json:"nonce"`
}

type historyFrame struct {
	Type     string             `json:"type"`
	Messages []protocol.Message `json:"messages"`
}

type messageFrame struct {
	Type    string           `json:"type"`
	Message protocol.Message `json:"message"`
}

type statusFrame struct {
	Type   string          `json:"type"`
	Status protocol.Status `json:"status"`
}

type sendResultFrame struct {
	Type    string           `json:"type"`
	ID      string           `json:"id"`
	Outcome protocol.Outcome `json:"outcome"`
	Reason  protocol.Reason  `json:"reason,omitempty"`
}

// clientFrame covers every client -> server frame type (auth, send, paste).
type clientFrame struct {
	Type string `json:"type"`
	MAC  string `json:"mac"`
	ID   string `json:"id"`
	TS   *int64 `json:"ts"`
	Text string `json:"text"`
}

// StatusRetry closes a session for a protocol problem that isn't a MAC
// failure (slow handshake, malformed frame, bad id). The phone keeps its
// secret and reconnects. 1008 is reserved for a bad MAC and pairing reset,
// which make the phone forget its secret.
const StatusRetry websocket.StatusCode = 4000

// errPolicy carries a close reason for a 1008 close.
type errPolicy string

func (e errPolicy) Error() string { return string(e) }

// errRetry carries a close reason for a StatusRetry close.
type errRetry string

func (e errRetry) Error() string { return string(e) }

type session struct {
	srv    *Server
	conn   *websocket.Conn
	log    *slog.Logger
	killed chan struct{}
	once   sync.Once
}

func (ss *session) kill() { ss.once.Do(func() { close(ss.killed) }) }

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	if !originAllowed(r) {
		s.log.Warn("rejected websocket with foreign origin", "remote", r.RemoteAddr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Warn("websocket accept failed", "remote", r.RemoteAddr, "err", err)
		return
	}
	c.SetReadLimit(MaxAuthFrameBytes)
	ss := &session{
		srv:    s,
		conn:   c,
		log:    s.log.With("remote", r.RemoteAddr),
		killed: make(chan struct{}),
	}
	// Register before auth so DisconnectAll also catches a handshake in
	// flight with the old secret.
	s.register(ss)
	defer s.unregister(ss)
	ss.run(r.Context())
}

func (ss *session) run(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer ss.conn.CloseNow()

	opts := &ss.srv.opts
	key := opts.Secret.Current()
	nonce, err := newNonce()
	if err != nil {
		ss.log.Error("nonce generation failed", "err", err)
		ss.conn.Close(websocket.StatusInternalError, "internal error")
		return
	}
	if err := ss.write(ctx, challengeFrame{Type: "challenge", Nonce: nonce}); err != nil {
		return
	}

	// One reader goroutine for the whole session. It uses ctx (not the kill
	// channel) so a DisconnectAll can still complete a clean close handshake.
	frames := make(chan []byte)
	go func() {
		defer close(frames)
		for first := true; ; first = false {
			if !first {
				// Only frames after the auth frame may be paste-sized. A bad
				// auth closes the session before this frame is acted on.
				ss.conn.SetReadLimit(MaxFrameBytes)
			}
			typ, b, err := ss.conn.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				b = nil // signals malformed
			}
			select {
			case frames <- b:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Handshake: a valid auth within AuthTimeout, or 1008 and nothing else.
	timer := time.NewTimer(opts.AuthTimeout)
	defer timer.Stop()
	select {
	case b, ok := <-frames:
		if !ok {
			return
		}
		var f clientFrame
		if b == nil || json.Unmarshal(b, &f) != nil || f.Type != "auth" {
			ss.log.Warn("phone sent no auth frame")
			ss.conn.Close(StatusRetry, "expected auth")
			return
		}
		if !key.Valid() || !verifyMAC(f.MAC, key.Bytes(), authInput(nonce)) {
			ss.log.Warn("phone auth failed")
			ss.conn.Close(websocket.StatusPolicyViolation, "auth failed")
			return
		}
	case <-timer.C:
		ss.log.Warn("phone auth timed out")
		ss.conn.Close(StatusRetry, "auth timeout")
		return
	case <-ss.killed:
		ss.conn.Close(websocket.StatusPolicyViolation, "pairing reset")
		return
	case <-ctx.Done():
		return
	}
	timer.Stop()
	ss.log.Info("phone authenticated")

	history, events, unsubscribe := opts.Feed.Subscribe()
	defer unsubscribe()
	if err := ss.writeHistory(ctx, history); err != nil {
		return
	}
	last := opts.Status.Status()
	if err := ss.write(ctx, statusFrame{Type: "status", Status: last}); err != nil {
		return
	}

	ticker := time.NewTicker(opts.StatusInterval)
	defer ticker.Stop()
	results := make(chan protocol.SendResult)
	// A send that has started runs to completion even if the socket drops:
	// the Sender stores the result for a resend with the same id.
	sendCtx := context.WithoutCancel(ctx)

	for {
		var err error
		select {
		case ev, ok := <-events:
			if !ok {
				ss.conn.Close(websocket.StatusGoingAway, "shutting down")
				return
			}
			switch {
			case ev.Resync:
				err = ss.writeHistory(ctx, opts.Feed.Snapshot())
			case ev.Message != nil:
				err = ss.write(ctx, messageFrame{Type: "message", Message: *ev.Message})
			}
		case <-ticker.C:
			if st := opts.Status.Status(); st != last {
				last = st
				err = ss.write(ctx, statusFrame{Type: "status", Status: st})
			}
		case b, ok := <-frames:
			if !ok {
				return
			}
			err = ss.handleFrame(ctx, sendCtx, key.Bytes(), nonce, b, results)
		case res := <-results:
			err = ss.write(ctx, sendResultFrame{Type: "send_result", ID: res.ID, Outcome: res.Outcome, Reason: res.Reason})
		case <-ss.killed:
			ss.conn.Close(websocket.StatusPolicyViolation, "pairing reset")
			return
		case <-ctx.Done():
			return
		}
		if err != nil {
			var pe errPolicy
			var re errRetry
			switch {
			case errors.As(err, &pe):
				ss.log.Warn("closing phone session", "reason", string(pe))
				ss.conn.Close(websocket.StatusPolicyViolation, string(pe))
			case errors.As(err, &re):
				ss.log.Warn("closing phone session", "reason", string(re))
				ss.conn.Close(StatusRetry, string(re))
			}
			return
		}
	}
}

// handleFrame processes one post-auth client frame. It returns errPolicy to
// close the socket with 1008 (bad MAC) or errRetry for StatusRetry.
func (ss *session) handleFrame(ctx, sendCtx context.Context, key []byte, nonce string, b []byte, results chan<- protocol.SendResult) error {
	var f clientFrame
	if b == nil || json.Unmarshal(b, &f) != nil {
		return errRetry("malformed frame")
	}
	var macInput func(nonce, id string, ts int64, text string) string
	var deliver func(ctx context.Context, id, text string) protocol.SendResult
	switch f.Type {
	case "send":
		macInput, deliver = sendInput, ss.srv.opts.Sender.Send
	case "paste":
		macInput, deliver = pasteInput, ss.srv.opts.Sender.Paste
	default:
		ss.log.Debug("ignoring client frame", "type", f.Type)
		return nil
	}
	if f.MAC == "" || f.TS == nil {
		return errPolicy("bad mac")
	}
	if !verifyMAC(f.MAC, key, macInput(nonce, f.ID, *f.TS, f.Text)) {
		return errPolicy("bad mac")
	}
	if !validID(f.ID) {
		return errRetry("bad id")
	}
	now := ss.srv.opts.Now()
	skew := now.Sub(time.UnixMilli(*f.TS))
	if skew > StaleAfter || skew < -StaleAfter {
		ss.log.Info(f.Type+" rejected as stale", "id", f.ID)
		return ss.write(ctx, sendResultFrame{Type: "send_result", ID: f.ID, Outcome: protocol.OutcomeNotSent, Reason: protocol.ReasonStale})
	}
	id, text := f.ID, f.Text
	ss.log.Info(f.Type+" accepted", "id", id, "bytes", len(text))
	go func() {
		res := deliver(sendCtx, id, text)
		res.ID = id
		select {
		case results <- res:
		case <-ctx.Done():
		}
	}()
	return nil
}

func (ss *session) writeHistory(ctx context.Context, msgs []protocol.Message) error {
	if len(msgs) > MaxHistory {
		msgs = msgs[len(msgs)-MaxHistory:]
	}
	if msgs == nil {
		msgs = []protocol.Message{}
	}
	return ss.write(ctx, historyFrame{Type: "history", Messages: msgs})
}

// write is only called from the session's run goroutine, which makes it the
// single writer for the connection.
func (ss *session) write(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return ss.conn.Write(wctx, websocket.MessageText, b)
}

func newNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// validID is 1-64 characters of [A-Za-z0-9_-].
func validID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}
