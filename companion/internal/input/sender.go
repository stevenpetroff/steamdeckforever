package input

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/protocol"
)

// dedupeSize is how many final results the sender remembers (KTD7).
const dedupeSize = 100

// Options tunes the send sequence. Zero values take the defaults.
type Options struct {
	// ReadBackTimeout bounds how long the sender waits for the clipboard
	// read-back to match the message before giving up with
	// input_unavailable (default 500ms).
	ReadBackTimeout time.Duration
	// ReadBackPoll is the pause between read-back attempts (default 20ms).
	ReadBackPoll time.Duration
	// KeyDelay is the pause between the Ctrl+V and Enter presses
	// (default 15ms).
	KeyDelay time.Duration
	// ChordSettle is the pause after the open-chat chord, before the paste,
	// so WoW has opened the chat box (default 60ms).
	ChordSettle time.Duration
}

func (o Options) withDefaults() Options {
	if o.ReadBackTimeout <= 0 {
		o.ReadBackTimeout = 500 * time.Millisecond
	}
	if o.ReadBackPoll <= 0 {
		o.ReadBackPoll = 20 * time.Millisecond
	}
	if o.KeyDelay <= 0 {
		o.KeyDelay = 15 * time.Millisecond
	}
	if o.ChordSettle <= 0 {
		o.ChordSettle = 60 * time.Millisecond
	}
	return o
}

// Sender delivers phone messages into WoW one at a time.
type Sender struct {
	open  func() (Keyboard, error)
	clip  Clipboard
	focus FocusChecker
	opts  Options

	// sendLock serializes sends. It is a channel so waiting can honor ctx.
	sendLock chan struct{}

	mu       sync.Mutex
	kb       Keyboard // nil until opened; reset to nil after an error
	tried    bool     // open has been attempted at least once
	inflight map[string]*call
	done     map[string]protocol.SendResult
	order    []string // ids in done, oldest first
}

type call struct {
	finished chan struct{}
	result   protocol.SendResult
}

// NewSender returns a sender. open is called lazily to get the virtual
// keyboard (OpenUinput in production) and again on later sends after it
// fails, so access that appears later is picked up without a restart.
func NewSender(open func() (Keyboard, error), clip Clipboard, focus FocusChecker, opts Options) *Sender {
	return &Sender{
		open:     open,
		clip:     clip,
		focus:    focus,
		opts:     opts.withDefaults(),
		sendLock: make(chan struct{}, 1),
		inflight: map[string]*call{},
		done:     map[string]protocol.SendResult{},
	}
}

// Send delivers text into WoW's chat box and returns the final result.
//
// A repeated id returns the stored result of the first attempt (or waits for
// it if it is still running) and is never sent twice. A ctx canceled before
// the attempt starts returns not_sent/input_unavailable without storing it,
// so a retry with the same id runs normally. Once started, an attempt always
// runs to completion (it takes well under a second).
func (s *Sender) Send(ctx context.Context, id, text string) protocol.SendResult {
	s.mu.Lock()
	if r, ok := s.done[id]; ok {
		s.mu.Unlock()
		return r
	}
	if c, ok := s.inflight[id]; ok {
		s.mu.Unlock()
		<-c.finished
		return c.result
	}
	c := &call{finished: make(chan struct{})}
	s.inflight[id] = c
	s.mu.Unlock()

	result, store := s.run(ctx, id, text)

	s.mu.Lock()
	delete(s.inflight, id)
	if store {
		s.remember(id, result)
	}
	c.result = result
	close(c.finished)
	s.mu.Unlock()
	return result
}

// remember stores a final result, evicting the oldest past dedupeSize.
// Caller holds s.mu.
func (s *Sender) remember(id string, r protocol.SendResult) {
	s.done[id] = r
	s.order = append(s.order, id)
	for len(s.order) > dedupeSize {
		delete(s.done, s.order[0])
		s.order = s.order[1:]
	}
}

func (s *Sender) run(ctx context.Context, id, text string) (protocol.SendResult, bool) {
	notSent := func(r protocol.Reason) protocol.SendResult {
		return protocol.SendResult{ID: id, Outcome: protocol.OutcomeNotSent, Reason: r}
	}

	if ctx.Err() != nil {
		return notSent(protocol.ReasonInputUnavailable), false
	}
	select {
	case s.sendLock <- struct{}{}:
	case <-ctx.Done():
		return notSent(protocol.ReasonInputUnavailable), false
	}
	defer func() { <-s.sendLock }()

	if text == "" || strings.ContainsAny(text, "\r\n") {
		return notSent(protocol.ReasonInvalidText), true
	}
	if len(text) > protocol.MaxMessageBytes {
		return notSent(protocol.ReasonTooLong), true
	}
	if !s.focused() {
		return notSent(protocol.ReasonNotFocused), true
	}
	kb := s.keyboard()
	if kb == nil {
		return notSent(protocol.ReasonInputUnavailable), true
	}
	if !s.setAndVerify(text) {
		return notSent(protocol.ReasonInputUnavailable), true
	}

	steps := []struct {
		keys  []Key
		pause time.Duration
	}{
		{Chord, s.opts.ChordSettle},
		{[]Key{KeyLeftCtrl, KeyV}, s.opts.KeyDelay},
		{[]Key{KeyEnter}, 0},
	}
	for _, st := range steps {
		if !s.focused() {
			return notSent(protocol.ReasonFocusLost), true
		}
		if err := kb.Press(st.keys...); err != nil {
			s.dropKeyboard(kb)
			return notSent(protocol.ReasonInputUnavailable), true
		}
		if st.pause > 0 {
			time.Sleep(st.pause)
		}
	}
	return protocol.SendResult{ID: id, Outcome: protocol.OutcomeSent}, true
}

// focused treats an error as not focused: the sender only types when it
// positively knows WoW has focus.
func (s *Sender) focused() bool {
	ok, err := s.focus.Focused()
	return err == nil && ok
}

// setAndVerify puts text on the clipboard and polls the read-back until it
// matches exactly (KTD3) or the timeout passes.
func (s *Sender) setAndVerify(text string) bool {
	if err := s.clip.Set(text); err != nil {
		return false
	}
	deadline := time.Now().Add(s.opts.ReadBackTimeout)
	for {
		got, err := s.clip.ReadBack()
		if err == nil && got == text {
			return true
		}
		if !time.Now().Add(s.opts.ReadBackPoll).Before(deadline) {
			// One last try exactly at the deadline.
			time.Sleep(time.Until(deadline))
			got, err = s.clip.ReadBack()
			return err == nil && got == text
		}
		time.Sleep(s.opts.ReadBackPoll)
	}
}

// keyboard returns the open keyboard, opening it if needed. nil means
// unavailable; the next call tries again.
func (s *Sender) keyboard() Keyboard {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kb != nil {
		return s.kb
	}
	s.tried = true
	kb, err := s.open()
	if err != nil || kb == nil {
		return nil
	}
	s.kb = kb
	return kb
}

func (s *Sender) dropKeyboard(kb Keyboard) {
	s.mu.Lock()
	if s.kb == kb {
		s.kb = nil
	}
	s.mu.Unlock()
	_ = kb.Close()
}

// Available reports whether the virtual keyboard opened on the last attempt.
// If it has never been tried, it tries now.
func (s *Sender) Available() bool {
	s.mu.Lock()
	tried, kb := s.tried, s.kb
	s.mu.Unlock()
	if kb != nil {
		return true
	}
	if tried {
		return false
	}
	return s.keyboard() != nil
}

// Close releases the virtual keyboard, if open.
func (s *Sender) Close() error {
	s.mu.Lock()
	kb := s.kb
	s.kb = nil
	s.mu.Unlock()
	if kb != nil {
		return kb.Close()
	}
	return nil
}
