package hub

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/protocol"
)

func msg(text string) protocol.Message {
	return protocol.Message{Channel: protocol.ChannelSay, Sender: "Bob", Text: text}
}

func recv(t *testing.T, s *Subscription) Event {
	t.Helper()
	select {
	case ev, ok := <-s.C():
		if !ok {
			t.Fatal("subscription channel closed")
		}
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
	return Event{}
}

func TestPublishAssignsIncreasingIDs(t *testing.T) {
	h := New(0)
	before := time.Now()
	for i := 1; i <= 5; i++ {
		m := h.Publish(protocol.Message{ID: "ignored", Text: "x"})
		if m.ID != strconv.Itoa(i) {
			t.Fatalf("message %d got ID %q", i, m.ID)
		}
	}
	if h.LastAt().Before(before) {
		t.Fatalf("LastAt %v before publish time %v", h.LastAt(), before)
	}
	if !New(1).LastAt().IsZero() {
		t.Fatal("LastAt should be zero before any publish")
	}
}

func TestHistoryCapped(t *testing.T) {
	h := New(3)
	for i := 1; i <= 5; i++ {
		h.Publish(msg(strconv.Itoa(i)))
	}
	snap := h.Snapshot()
	if len(snap) != 3 || snap[0].ID != "3" || snap[2].ID != "5" {
		t.Fatalf("snapshot = %+v, want IDs 3..5", snap)
	}
	hist, sub := h.Subscribe(4)
	defer sub.Close()
	if len(hist) != 3 || hist[0].Text != "3" || hist[2].Text != "5" {
		t.Fatalf("history = %+v", hist)
	}
	if len(New(0).Snapshot()) != 0 {
		t.Fatal("empty hub snapshot not empty")
	}
	big := New(0)
	for i := 0; i < 600; i++ {
		big.Publish(msg("x"))
	}
	if s := big.Snapshot(); len(s) != 500 || s[0].ID != "101" {
		t.Fatalf("default capacity: len %d first %q", len(s), s[0].ID)
	}
}

func TestLiveDelivery(t *testing.T) {
	h := New(10)
	h.Publish(msg("old"))
	hist, sub := h.Subscribe(4)
	defer sub.Close()
	if len(hist) != 1 {
		t.Fatalf("history len %d", len(hist))
	}
	h.Publish(msg("a"))
	h.Publish(msg("b"))
	for _, want := range []string{"a", "b"} {
		ev := recv(t, sub)
		if ev.Resync || ev.Message == nil || ev.Message.Text != want {
			t.Fatalf("got %+v, want message %q", ev, want)
		}
	}
}

func TestSlowSubscriberGetsResync(t *testing.T) {
	h := New(100)
	_, sub := h.Subscribe(2)
	defer sub.Close()
	for i := 1; i <= 20; i++ {
		h.Publish(msg(strconv.Itoa(i)))
	}
	// Drain whatever was buffered; a Resync must arrive. A second one is
	// possible if the queue overflows again after the first left the queue.
	resyncs := 0
	deadline := time.After(time.Second)
drain:
	for {
		select {
		case ev := <-sub.C():
			if ev.Resync {
				resyncs++
				if ev.Message != nil {
					t.Fatal("resync event carries a message")
				}
			}
		case <-time.After(100 * time.Millisecond):
			break drain
		case <-deadline:
			t.Fatal("drain did not settle")
		}
	}
	if resyncs < 1 || resyncs > 2 {
		t.Fatalf("got %d resync events, want 1 or 2", resyncs)
	}
	if n := len(h.Snapshot()); n != 20 {
		t.Fatalf("snapshot len %d", n)
	}
	h.Publish(msg("after"))
	ev := recv(t, sub)
	if ev.Resync || ev.Message == nil || ev.Message.Text != "after" {
		t.Fatalf("after resync got %+v", ev)
	}
}

func TestResyncDeliveredWithoutFurtherPublish(t *testing.T) {
	h := New(100)
	_, sub := h.Subscribe(1)
	defer sub.Close()
	for i := 0; i < 10; i++ {
		h.Publish(msg("x"))
	}
	for {
		ev := recv(t, sub)
		if ev.Resync {
			return
		}
	}
}

func TestCloseStopsDelivery(t *testing.T) {
	h := New(10)
	_, sub := h.Subscribe(4)
	h.Publish(msg("before"))
	sub.Close()
	sub.Close() // idempotent
	h.Publish(msg("after"))
	timeout := time.After(time.Second)
	for {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				return
			}
			if ev.Message != nil && ev.Message.Text == "after" {
				t.Fatal("delivered a message published after Close")
			}
		case <-timeout:
			t.Fatal("channel not closed after Close")
		}
	}
}

func TestConcurrentPublishSubscribe(t *testing.T) {
	h := New(50)
	var wg sync.WaitGroup
	for p := 0; p < 4; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				h.Publish(msg("x"))
				_ = h.LastAt()
			}
		}()
	}
	for s := 0; s < 4; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_, sub := h.Subscribe(8)
				for j := 0; j < 5; j++ {
					select {
					case <-sub.C():
					case <-time.After(10 * time.Millisecond):
					}
				}
				_ = h.Snapshot()
				sub.Close()
			}
		}()
	}
	wg.Wait()
	m := h.Publish(msg("last"))
	if m.ID != "2001" {
		t.Fatalf("final ID %q, want 2001", m.ID)
	}
}
