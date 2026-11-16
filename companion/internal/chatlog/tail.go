package chatlog

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/stevenpetroff/steamdeckforever/companion/internal/protocol"
)

// TailOptions configures Tail. Zero values select the defaults.
type TailOptions struct {
	Poll      time.Duration // how often to check the file size; default 250ms
	SeedLines int           // parsed lines to emit from the end of an existing file; default 200
}

const (
	defaultPoll      = 250 * time.Millisecond
	defaultSeedLines = 200
	seedChunk        = 64 * 1024
	readChunk        = 64 * 1024
)

// Tail follows the chat log at path and calls emit for each parsed line.
//
// If the file exists at start, the last SeedLines parsed lines are emitted,
// then Tail follows from there. It polls the size every Poll (WoW's logging
// toggle reopens the file, so inotify isn't used), emits only complete lines,
// and holds a partial trailing line until its newline arrives. If the file
// shrinks or is replaced, it reads the new file from offset 0. If the file
// doesn't exist, it waits for it to appear and then reads it from the start.
// emit is called from Tail's goroutine. Tail returns ctx.Err() on cancel.
func Tail(ctx context.Context, path string, opts TailOptions, emit func(protocol.Message)) error {
	if opts.Poll <= 0 {
		opts.Poll = defaultPoll
	}
	if opts.SeedLines <= 0 {
		opts.SeedLines = defaultSeedLines
	}
	t := &tailer{path: path, emit: emit}
	defer t.close()

	if err := t.open(); err == nil {
		if err := t.seed(opts.SeedLines); err != nil {
			t.close()
		}
	}

	ticker := time.NewTicker(opts.Poll)
	defer ticker.Stop()
	for {
		t.poll()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type tailer struct {
	path    string
	emit    func(protocol.Message)
	f       *os.File
	fi      os.FileInfo
	offset  int64  // bytes consumed from f, including partial
	partial []byte // bytes after the last newline, awaiting completion
}

func (t *tailer) open() error {
	f, err := os.Open(t.path)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	t.f, t.fi, t.offset, t.partial = f, fi, 0, nil
	return nil
}

func (t *tailer) close() {
	if t.f != nil {
		t.f.Close()
	}
	t.f, t.fi, t.offset, t.partial = nil, nil, 0, nil
}

// seed emits the last n parsed lines of the open file, reading backwards in
// chunks, and leaves offset just past the last complete line.
func (t *tailer) seed(n int) error {
	size := t.fi.Size()
	buf := make([]byte, 0)
	start := size
	for {
		chunk := int64(seedChunk)
		if chunk > start {
			chunk = start
		}
		start -= chunk
		b := make([]byte, chunk, int64(len(buf))+chunk)
		if _, err := t.f.ReadAt(b, start); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		buf = append(b, buf...)
		// Stop once enough complete lines are in hand (one extra newline so the
		// first line isn't a fragment), or at the start of the file.
		if start == 0 || bytes.Count(buf, []byte{'\n'}) > n {
			break
		}
	}
	end := bytes.LastIndexByte(buf, '\n') + 1 // 0 if no complete line
	region := buf[:end]
	if start > 0 {
		// Drop the leading fragment of a line that started before start.
		region = region[bytes.IndexByte(region, '\n')+1:]
	}
	var msgs []protocol.Message
	for _, l := range bytes.Split(region, []byte{'\n'}) {
		if m, ok := Parse(string(l)); ok {
			msgs = append(msgs, m)
		}
	}
	if len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	for _, m := range msgs {
		t.emit(m)
	}
	t.offset = start + int64(end)
	return nil
}

func (t *tailer) poll() {
	fi, err := os.Stat(t.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			t.close() // a file appearing later is read from 0
		}
		return
	}
	if t.f == nil || !os.SameFile(t.fi, fi) || fi.Size() < t.offset {
		t.close()
		if t.open() != nil {
			return
		}
	}
	if fi.Size() == t.offset {
		return // nothing new
	}
	t.readNew()
}

func (t *tailer) readNew() {
	buf := make([]byte, readChunk)
	for {
		n, err := t.f.ReadAt(buf, t.offset)
		if n > 0 {
			t.offset += int64(n)
			t.consume(buf[:n])
		}
		if err != nil || n < len(buf) {
			return
		}
	}
}

func (t *tailer) consume(b []byte) {
	for {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			t.partial = append(t.partial, b...)
			return
		}
		var l []byte
		if len(t.partial) > 0 {
			l = append(t.partial, b[:i]...)
			t.partial = nil
		} else {
			l = b[:i]
		}
		if m, ok := Parse(string(l)); ok {
			t.emit(m)
		}
		b = b[i+1:]
	}
}
