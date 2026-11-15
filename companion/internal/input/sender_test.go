package input

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/protocol"
)

// rec is one shared, ordered log of everything the fakes observed, so tests
// can check ordering across the clipboard and the keyboard.
type rec struct {
	mu  sync.Mutex
	log []string
}

func (r *rec) add(s string) {
	r.mu.Lock()
	r.log = append(r.log, s)
	r.mu.Unlock()
}

func (r *rec) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.log...)
}

// keys returns only the key events ("down:X" / "up:X").
func (r *rec) keys() []string {
	var out []string
	for _, e := range r.all() {
		if strings.HasPrefix(e, "down:") || strings.HasPrefix(e, "up:") {
			out = append(out, e)
		}
	}
	return out
}

func (r *rec) sets() []string {
	var out []string
	for _, e := range r.all() {
		if t, ok := strings.CutPrefix(e, "set:"); ok {
			out = append(out, t)
		}
	}
	return out
}

type fakeKeyboard struct {
	r       *rec
	onPress func(keys []Key) // called after the chord's ups are logged
	failOn  func(keys []Key) error
	block   chan struct{} // if non-nil, the first Press waits on it
	started chan struct{}
	once    sync.Once
	closed  atomic.Bool
}

func (k *fakeKeyboard) Press(keys ...Key) error {
	if k.block != nil {
		first := false
		k.once.Do(func() { first = true })
		if first {
			close(k.started)
			<-k.block
		}
	}
	if k.failOn != nil {
		if err := k.failOn(keys); err != nil {
			return err
		}
	}
	for _, key := range keys {
		k.r.add("down:" + key.String())
	}
	for i := len(keys) - 1; i >= 0; i-- {
		k.r.add("up:" + keys[i].String())
	}
	if k.onPress != nil {
		k.onPress(keys)
	}
	return nil
}

func (k *fakeKeyboard) Close() error { k.closed.Store(true); return nil }

type fakeClipboard struct {
	r        *rec
	mu       sync.Mutex
	text     string
	readBack func(stored string) (string, error)
	setErr   error
}

func (c *fakeClipboard) Set(text string) error {
	if c.setErr != nil {
		return c.setErr
	}
	c.mu.Lock()
	c.text = text
	c.mu.Unlock()
	c.r.add("set:" + text)
	return nil
}

func (c *fakeClipboard) ReadBack() (string, error) {
	c.mu.Lock()
	t := c.text
	c.mu.Unlock()
	if c.readBack != nil {
		return c.readBack(t)
	}
	return t, nil
}

type fakeFocus struct {
	focused atomic.Bool
	err     error
	calls   atomic.Int32
}

func (f *fakeFocus) Focused() (bool, error) {
	f.calls.Add(1)
	if f.err != nil {
		return false, f.err
	}
	return f.focused.Load(), nil
}

type rig struct {
	r     *rec
	kb    *fakeKeyboard
	clip  *fakeClipboard
	focus *fakeFocus
	opens atomic.Int32
	openE atomic.Pointer[error]
	s     *Sender
}

var fastOpts = Options{
	ReadBackTimeout: 100 * time.Millisecond,
	ReadBackPoll:    5 * time.Millisecond,
	KeyDelay:        time.Millisecond,
	ChordSettle:     time.Millisecond,
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rec{}
	g := &rig{
		r:     r,
		kb:    &fakeKeyboard{r: r},
		clip:  &fakeClipboard{r: r},
		focus: &fakeFocus{},
	}
	g.focus.focused.Store(true)
	open := func() (Keyboard, error) {
		g.opens.Add(1)
		if e := g.openE.Load(); e != nil {
			return nil, *e
		}
		return g.kb, nil
	}
	g.s = NewSender(open, g.clip, g.focus, fastOpts)
	return g
}

var fullSequence = []string{
	"down:LEFTCTRL", "down:LEFTALT", "down:LEFTSHIFT", "down:F12",
	"up:F12", "up:LEFTSHIFT", "up:LEFTALT", "up:LEFTCTRL",
	"down:LEFTCTRL", "down:V", "up:V", "up:LEFTCTRL",
	"down:ENTER", "up:ENTER",
}

func sent(id string) protocol.SendResult {
	return protocol.SendResult{ID: id, Outcome: protocol.OutcomeSent}
}

func notSent(id string, r protocol.Reason) protocol.SendResult {
	return protocol.SendResult{ID: id, Outcome: protocol.OutcomeNotSent, Reason: r}
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkBalanced asserts every key-down has exactly one later key-up and no
// key goes up without having gone down (AE5).
func checkBalanced(t *testing.T, events []string) {
	t.Helper()
	held := map[string]int{}
	for _, e := range events {
		if k, ok := strings.CutPrefix(e, "down:"); ok {
			held[k]++
		} else if k, ok := strings.CutPrefix(e, "up:"); ok {
			if held[k] == 0 {
				t.Fatalf("key-up for %s that was never pressed: %v", k, events)
			}
			held[k]--
		}
	}
	for k, n := range held {
		if n != 0 {
			t.Fatalf("key %s left down: %v", k, events)
		}
	}
}

// Covers AE1 and R7: exactly the text on the clipboard, then exactly chord,
// Ctrl+V, Enter.
func TestSendPastesExactlyAndPressesOnlyTheSequence(t *testing.T) {
	g := newRig(t)
	got := g.s.Send(context.Background(), "a1", "/p pulling")
	if got != sent("a1") {
		t.Fatalf("result = %+v", got)
	}
	if s := g.clip.sets(); !eqStrings(s, []string{"/p pulling"}) {
		t.Fatalf("clipboard sets = %q", s)
	}
	if k := g.r.keys(); !eqStrings(k, fullSequence) {
		t.Fatalf("keys =\n%v\nwant\n%v", k, fullSequence)
	}
	if all := g.r.all(); all[0] != "set:/p pulling" {
		t.Fatalf("clipboard must be set before any key: %v", all)
	}
	checkBalanced(t, g.r.keys())
}

func (c *fakeClipboard) sets() []string { return c.r.sets() }

// Covers AE4 and R9.
func TestNotFocusedTouchesNothing(t *testing.T) {
	g := newRig(t)
	g.focus.focused.Store(false)
	got := g.s.Send(context.Background(), "a1", "brb")
	if got != notSent("a1", protocol.ReasonNotFocused) {
		t.Fatalf("result = %+v", got)
	}
	if n := len(g.r.all()); n != 0 {
		t.Fatalf("expected no clipboard or key activity, got %v", g.r.all())
	}
}

func TestFocusErrorAtGateIsNotFocused(t *testing.T) {
	g := newRig(t)
	g.focus.err = errors.New("display gone")
	got := g.s.Send(context.Background(), "a1", "brb")
	if got != notSent("a1", protocol.ReasonNotFocused) {
		t.Fatalf("result = %+v", got)
	}
	if n := len(g.r.all()); n != 0 {
		t.Fatalf("activity: %v", g.r.all())
	}
}

// KTD10: the limit is 255 UTF-8 bytes, not characters.
func TestByteLimit(t *testing.T) {
	g := newRig(t)
	long := strings.Repeat("é", 128) // 256 bytes, 128 runes
	if got := g.s.Send(context.Background(), "long", long); got != notSent("long", protocol.ReasonTooLong) {
		t.Fatalf("256 bytes: %+v", got)
	}
	if n := len(g.r.all()); n != 0 {
		t.Fatalf("too_long must not touch anything: %v", g.r.all())
	}
	ok := strings.Repeat("é", 127) + "a" // 255 bytes
	if got := g.s.Send(context.Background(), "ok", ok); got != sent("ok") {
		t.Fatalf("255 bytes: %+v", got)
	}
	if s := g.clip.sets(); !eqStrings(s, []string{ok}) {
		t.Fatalf("sets = %q", s)
	}
}

func TestInvalidText(t *testing.T) {
	for i, text := range []string{"", "a\nb", "a\rb", "\n", "trailing\r\n"} {
		g := newRig(t)
		id := fmt.Sprintf("i%d", i)
		if got := g.s.Send(context.Background(), id, text); got != notSent(id, protocol.ReasonInvalidText) {
			t.Fatalf("%q: %+v", text, got)
		}
		if n := len(g.r.all()); n != 0 {
			t.Fatalf("%q touched: %v", text, g.r.all())
		}
	}
}

func TestDuplicateIDReplaysResult(t *testing.T) {
	g := newRig(t)
	first := g.s.Send(context.Background(), "dup", "hello")
	second := g.s.Send(context.Background(), "dup", "hello")
	if first != sent("dup") || second != first {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if k := g.r.keys(); !eqStrings(k, fullSequence) {
		t.Fatalf("expected one sequence, got %v", k)
	}
	if s := g.clip.sets(); len(s) != 1 {
		t.Fatalf("clipboard set %d times", len(s))
	}
}

func TestNotFocusedRetrySameIDStaysNotFocused(t *testing.T) {
	g := newRig(t)
	g.focus.focused.Store(false)
	first := g.s.Send(context.Background(), "x", "hello")
	g.focus.focused.Store(true)
	second := g.s.Send(context.Background(), "x", "hello")
	want := notSent("x", protocol.ReasonNotFocused)
	if first != want || second != want {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if n := len(g.r.all()); n != 0 {
		t.Fatalf("activity: %v", g.r.all())
	}
}

func TestDuplicateWhileInProgressWaits(t *testing.T) {
	g := newRig(t)
	g.kb.block = make(chan struct{})
	g.kb.started = make(chan struct{})

	results := make(chan protocol.SendResult, 2)
	go func() { results <- g.s.Send(context.Background(), "p", "hello") }()
	<-g.kb.started // first attempt is mid-sequence
	go func() { results <- g.s.Send(context.Background(), "p", "hello") }()

	select {
	case r := <-results:
		t.Fatalf("a send returned while the first was blocked: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
	close(g.kb.block)
	a, b := <-results, <-results
	if a != sent("p") || b != a {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
	if k := g.r.keys(); !eqStrings(k, fullSequence) {
		t.Fatalf("expected one sequence, got %v", k)
	}
}

func TestFocusLostAfterChord(t *testing.T) {
	g := newRig(t)
	g.kb.onPress = func(keys []Key) {
		if len(keys) == 4 {
			g.focus.focused.Store(false)
		}
	}
	got := g.s.Send(context.Background(), "f", "hello")
	if got != notSent("f", protocol.ReasonFocusLost) {
		t.Fatalf("result = %+v", got)
	}
	if k := g.r.keys(); !eqStrings(k, fullSequence[:8]) {
		t.Fatalf("keys = %v, want only the chord", k)
	}
	checkBalanced(t, g.r.keys())
}

func TestFocusLostAfterPasteSkipsEnter(t *testing.T) {
	g := newRig(t)
	g.kb.onPress = func(keys []Key) {
		if len(keys) == 2 {
			g.focus.focused.Store(false)
		}
	}
	got := g.s.Send(context.Background(), "f", "hello")
	if got != notSent("f", protocol.ReasonFocusLost) {
		t.Fatalf("result = %+v", got)
	}
	if k := g.r.keys(); !eqStrings(k, fullSequence[:12]) {
		t.Fatalf("keys = %v, want no Enter", k)
	}
	checkBalanced(t, g.r.keys())
}

func TestFocusLostBeforeChord(t *testing.T) {
	g := newRig(t)
	// Focus is fine at the gate, gone by the time the clipboard is verified.
	g.clip.readBack = func(s string) (string, error) {
		g.focus.focused.Store(false)
		return s, nil
	}
	got := g.s.Send(context.Background(), "f", "hello")
	if got != notSent("f", protocol.ReasonFocusLost) {
		t.Fatalf("result = %+v", got)
	}
	if k := g.r.keys(); len(k) != 0 {
		t.Fatalf("keys = %v", k)
	}
}

func TestReadBackNeverMatches(t *testing.T) {
	g := newRig(t)
	g.clip.readBack = func(string) (string, error) { return "something stale", nil }
	start := time.Now()
	got := g.s.Send(context.Background(), "r", "hello")
	if got != notSent("r", protocol.ReasonInputUnavailable) {
		t.Fatalf("result = %+v", got)
	}
	if k := g.r.keys(); len(k) != 0 {
		t.Fatalf("keys = %v", k)
	}
	if d := time.Since(start); d < fastOpts.ReadBackTimeout {
		t.Fatalf("gave up after %v, before the %v timeout", d, fastOpts.ReadBackTimeout)
	}
}

func TestReadBackErrorsUntilTimeout(t *testing.T) {
	g := newRig(t)
	g.clip.readBack = func(string) (string, error) { return "", errors.New("no owner") }
	got := g.s.Send(context.Background(), "r", "hello")
	if got != notSent("r", protocol.ReasonInputUnavailable) || len(g.r.keys()) != 0 {
		t.Fatalf("result = %+v keys=%v", got, g.r.keys())
	}
}

func TestReadBackMatchesAfterAFewPolls(t *testing.T) {
	g := newRig(t)
	var n atomic.Int32
	g.clip.readBack = func(s string) (string, error) {
		if n.Add(1) < 3 {
			return "old", nil
		}
		return s, nil
	}
	if got := g.s.Send(context.Background(), "r", "hello"); got != sent("r") {
		t.Fatalf("result = %+v", got)
	}
	if n.Load() < 3 {
		t.Fatalf("read back %d times", n.Load())
	}
}

func TestClipboardSetErrorIsInputUnavailable(t *testing.T) {
	g := newRig(t)
	g.clip.setErr = errors.New("x connection closed")
	if got := g.s.Send(context.Background(), "c", "hello"); got != notSent("c", protocol.ReasonInputUnavailable) {
		t.Fatalf("result = %+v", got)
	}
	if k := g.r.keys(); len(k) != 0 {
		t.Fatalf("keys = %v", k)
	}
}

func TestKeyboardOpenRetriedOnLaterSend(t *testing.T) {
	g := newRig(t)
	e := errors.New("open /dev/uinput: permission denied")
	g.openE.Store(&e)

	if g.s.Available() {
		t.Fatal("Available() true while open fails")
	}
	got := g.s.Send(context.Background(), "k1", "hello")
	if got != notSent("k1", protocol.ReasonInputUnavailable) {
		t.Fatalf("result = %+v", got)
	}
	if n := len(g.r.all()); n != 0 {
		t.Fatalf("activity with no keyboard: %v", g.r.all())
	}

	g.openE.Store(nil) // access appears (installer re-run)
	if got := g.s.Send(context.Background(), "k2", "hello"); got != sent("k2") {
		t.Fatalf("after access appears: %+v", got)
	}
	if !g.s.Available() {
		t.Fatal("Available() false after a successful open")
	}
	if k := g.r.keys(); !eqStrings(k, fullSequence) {
		t.Fatalf("keys = %v", k)
	}
	opens := g.opens.Load()
	g.s.Send(context.Background(), "k3", "again")
	if g.opens.Load() != opens {
		t.Fatal("keyboard reopened although it was working")
	}
}

func TestAvailableOpensWhenNeverTried(t *testing.T) {
	g := newRig(t)
	if !g.s.Available() {
		t.Fatal("Available() = false")
	}
	if g.opens.Load() != 1 {
		t.Fatalf("opens = %d", g.opens.Load())
	}
	g.s.Send(context.Background(), "a", "hi")
	if g.opens.Load() != 1 {
		t.Fatalf("opens = %d after send", g.opens.Load())
	}
}

func TestKeyboardWriteErrorStopsAndReopens(t *testing.T) {
	g := newRig(t)
	g.kb.failOn = func(keys []Key) error {
		if len(keys) == 2 {
			return errors.New("write /dev/uinput: no such device")
		}
		return nil
	}
	got := g.s.Send(context.Background(), "w", "hello")
	if got != notSent("w", protocol.ReasonInputUnavailable) {
		t.Fatalf("result = %+v", got)
	}
	for _, k := range g.r.keys() {
		if k == "down:ENTER" {
			t.Fatal("Enter pressed after a keyboard error")
		}
	}
	if !g.kb.closed.Load() {
		t.Fatal("broken keyboard not closed")
	}
	g.kb.failOn = nil
	g.s.Send(context.Background(), "w2", "hello")
	if g.opens.Load() != 2 {
		t.Fatalf("opens = %d, want reopen after error", g.opens.Load())
	}
}

func TestUnicodeByteForByte(t *testing.T) {
	g := newRig(t)
	text := "héllo 🙂"
	if got := g.s.Send(context.Background(), "u", text); got != sent("u") {
		t.Fatalf("result = %+v", got)
	}
	s := g.clip.sets()
	if len(s) != 1 || s[0] != text || []byte(s[0])[1] != 0xc3 {
		t.Fatalf("sets = %q", s)
	}
	if k := g.r.keys(); !eqStrings(k, fullSequence) {
		t.Fatalf("keys = %v", k)
	}
}

func TestConcurrentSendsAreSerialized(t *testing.T) {
	g := newRig(t)
	const n = 6
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("c%d", i)
			if got := g.s.Send(context.Background(), id, "msg "+id); got != sent(id) {
				t.Errorf("%s: %+v", id, got)
			}
		}()
	}
	wg.Wait()
	all := g.r.all()
	per := 1 + len(fullSequence)
	if len(all) != n*per {
		t.Fatalf("log has %d entries, want %d", len(all), n*per)
	}
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		block := all[i*per : (i+1)*per]
		if !strings.HasPrefix(block[0], "set:msg c") {
			t.Fatalf("block %d does not start with a clipboard set: %v", i, block)
		}
		seen[block[0]] = true
		if !eqStrings(block[1:], fullSequence) {
			t.Fatalf("block %d interleaved: %v", i, block)
		}
	}
	if len(seen) != n {
		t.Fatalf("distinct sends = %d", len(seen))
	}
}

func TestDedupeRemembersLast100(t *testing.T) {
	g := newRig(t)
	g.focus.focused.Store(false)
	g.s.Send(context.Background(), "old", "x") // stored as not_focused
	for i := range 100 {
		g.s.Send(context.Background(), fmt.Sprintf("n%d", i), "x")
	}
	g.focus.focused.Store(true)
	// "old" was evicted, so it is a fresh send now.
	if got := g.s.Send(context.Background(), "old", "x"); got != sent("old") {
		t.Fatalf("evicted id: %+v", got)
	}
	// n99 is still remembered as not_focused.
	if got := g.s.Send(context.Background(), "n99", "x"); got != notSent("n99", protocol.ReasonNotFocused) {
		t.Fatalf("remembered id: %+v", got)
	}
}

func TestCanceledContextBeforeStartIsNotStored(t *testing.T) {
	g := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := g.s.Send(ctx, "ctx", "hello")
	if got.Outcome != protocol.OutcomeNotSent || len(g.r.all()) != 0 {
		t.Fatalf("result = %+v, activity %v", got, g.r.all())
	}
	if got := g.s.Send(context.Background(), "ctx", "hello"); got != sent("ctx") {
		t.Fatalf("retry after cancel: %+v", got)
	}
}
