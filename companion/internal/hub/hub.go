// Package hub keeps the recent chat history and fans new messages out to
// subscribers (the phone connections).
package hub

import (
	"strconv"
	"sync"
	"time"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/protocol"
)

const defaultCapacity = 500

// Event is one delivery to a subscriber: either a new message, or Resync
// when messages were dropped and the consumer should re-read Snapshot.
type Event struct {
	Message *protocol.Message
	Resync  bool
}

// Hub is a fixed-size ring of recent messages with non-blocking fan-out.
// It is safe for concurrent use.
type Hub struct {
	mu     sync.Mutex
	ring   []protocol.Message
	head   int // index of the oldest message when the ring is full
	full   bool
	nextID uint64
	lastAt time.Time
	subs   map[*Subscription]struct{}
}

// New returns a hub keeping the last capacity messages (500 if <= 0).
func New(capacity int) *Hub {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	return &Hub{
		ring: make([]protocol.Message, 0, capacity),
		subs: make(map[*Subscription]struct{}),
	}
}

// Publish assigns the next ID to m, stores it, and offers it to every
// subscriber without blocking. It returns the stored message.
func (h *Hub) Publish(m protocol.Message) protocol.Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	m.ID = strconv.FormatUint(h.nextID, 10)
	if h.full {
		h.ring[h.head] = m
		h.head = (h.head + 1) % cap(h.ring)
	} else {
		h.ring = append(h.ring, m)
		h.full = len(h.ring) == cap(h.ring)
	}
	h.lastAt = time.Now()
	for s := range h.subs {
		s.offer(m)
	}
	return m
}

// Subscribe returns the current history (oldest first) and a subscription
// that receives every later message. buffer is how many undelivered events
// the subscription holds before it drops them and owes a Resync (min 1).
func (h *Hub) Subscribe(buffer int) ([]protocol.Message, *Subscription) {
	if buffer < 1 {
		buffer = 1
	}
	s := &Subscription{
		hub:    h,
		limit:  buffer,
		out:    make(chan Event),
		notify: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	h.mu.Lock()
	hist := h.snapshotLocked()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	go s.pump()
	return hist, s
}

// Snapshot returns a copy of the history, oldest first.
func (h *Hub) Snapshot() []protocol.Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.snapshotLocked()
}

func (h *Hub) snapshotLocked() []protocol.Message {
	out := make([]protocol.Message, 0, len(h.ring))
	out = append(out, h.ring[h.head:]...)
	return append(out, h.ring[:h.head]...)
}

// LastAt is when the last message was published (zero if none yet).
func (h *Hub) LastAt() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastAt
}

// Subscription receives events from a Hub. A per-subscription goroutine
// moves queued events to C, so Publish never blocks on a slow consumer.
type Subscription struct {
	hub    *Hub
	limit  int
	out    chan Event
	notify chan struct{}
	done   chan struct{}
	once   sync.Once

	mu     sync.Mutex
	queue  []protocol.Message
	resync bool // messages were dropped; deliver one Resync next
}

// C delivers events. It is closed after Close.
func (s *Subscription) C() <-chan Event { return s.out }

// Close stops delivery and releases the subscription. It is idempotent.
func (s *Subscription) Close() {
	s.once.Do(func() {
		s.hub.mu.Lock()
		delete(s.hub.subs, s)
		s.hub.mu.Unlock()
		close(s.done)
	})
}

// offer queues m without blocking. On overflow the queue is discarded and a
// Resync is owed; messages arriving while a Resync is pending are skipped,
// since the consumer will re-read them from Snapshot.
func (s *Subscription) offer(m protocol.Message) {
	s.mu.Lock()
	switch {
	case s.resync:
	case len(s.queue) >= s.limit:
		s.queue = nil
		s.resync = true
	default:
		s.queue = append(s.queue, m)
	}
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *Subscription) next() (Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resync {
		// Clearing the flag before the consumer reads Snapshot is safe:
		// anything published from here on is queued, and the consumer
		// de-duplicates by ID.
		s.resync = false
		return Event{Resync: true}, true
	}
	if len(s.queue) == 0 {
		return Event{}, false
	}
	m := s.queue[0]
	s.queue = s.queue[1:]
	return Event{Message: &m}, true
}

func (s *Subscription) pump() {
	defer close(s.out)
	for {
		ev, ok := s.next()
		if !ok {
			select {
			case <-s.notify:
				continue
			case <-s.done:
				return
			}
		}
		select {
		case <-s.done:
			return
		default:
		}
		select {
		case s.out <- ev:
		case <-s.done:
			return
		}
	}
}
