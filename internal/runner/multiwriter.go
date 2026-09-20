package runner

import (
	"bytes"
	"io"
	"sync"
	"time"
)

// LogBroadcaster manages active streaming subscribers for an execution
type LogBroadcaster struct {
	mu          sync.RWMutex
	subscribers map[chan string]struct{}
	buffer      []string
	maxBuffer   int
	isClosed    bool
}

func NewLogBroadcaster(maxBuffer int) *LogBroadcaster {
	if maxBuffer <= 0 {
		maxBuffer = 2000
	}
	return &LogBroadcaster{
		subscribers: make(map[chan string]struct{}),
		buffer:      make([]string, 0, 100),
		maxBuffer:   maxBuffer,
	}
}

// Subscribe returns a channel that receives live log lines, along with past buffered lines
func (b *LogBroadcaster) Subscribe() (<-chan string, []string, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan string, 200)
	if !b.isClosed {
		b.subscribers[ch] = struct{}{}
	}

	// Copy current buffer for subscriber replay
	buffered := make([]string, len(b.buffer))
	copy(buffered, b.buffer)

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, exists := b.subscribers[ch]; exists {
			delete(b.subscribers, ch)
			close(ch)
		}
	}

	return ch, buffered, unsubscribe
}

// BroadcastLine sends a single line to all active subscribers and appends to replay buffer
func (b *LogBroadcaster) BroadcastLine(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.buffer) < b.maxBuffer {
		b.buffer = append(b.buffer, line)
	}

	for ch := range b.subscribers {
		select {
		case ch <- line:
		default:
			// Non-blocking write prevents slow clients from stalling runner
		}
	}
}

// Close closes all subscriber channels
func (b *LogBroadcaster) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.isClosed {
		return
	}
	b.isClosed = true
	for ch := range b.subscribers {
		close(ch)
	}
	b.subscribers = nil
}

// MultiWriter splits output between a destination writer (file) and line broadcaster
type MultiWriter struct {
	dest        io.Writer
	broadcaster *LogBroadcaster
	lineBuf     bytes.Buffer
	mu          sync.Mutex
}

func NewMultiWriter(dest io.Writer, broadcaster *LogBroadcaster) *MultiWriter {
	return &MultiWriter{
		dest:        dest,
		broadcaster: broadcaster,
	}
}

func (mw *MultiWriter) Write(p []byte) (n int, err error) {
	mw.mu.Lock()
	defer mw.mu.Unlock()

	// 1. Write raw bytes to destination (file)
	n, err = mw.dest.Write(p)
	if err != nil {
		return n, err
	}

	// 2. Buffer for line-based broadcasting
	for _, b := range p {
		if b == '\n' {
			line := mw.lineBuf.String()
			mw.lineBuf.Reset()
			if mw.broadcaster != nil {
				mw.broadcaster.BroadcastLine(line)
			}
		} else {
			mw.lineBuf.WriteByte(b)
		}
	}

	return n, nil
}

func (mw *MultiWriter) Flush() {
	mw.mu.Lock()
	defer mw.mu.Unlock()

	if mw.lineBuf.Len() > 0 {
		line := mw.lineBuf.String()
		mw.lineBuf.Reset()
		if mw.broadcaster != nil {
			mw.broadcaster.BroadcastLine(line)
		}
	}
}

// BroadcastBatch batches lines over a short duration (e.g. 50ms) to throttle SSE output
type BatchBroadcaster struct {
	broadcaster *LogBroadcaster
	inCh        chan string
	stopCh      chan struct{}
}

func NewBatchBroadcaster(broadcaster *LogBroadcaster, interval time.Duration) *BatchBroadcaster {
	bb := &BatchBroadcaster{
		broadcaster: broadcaster,
		inCh:        make(chan string, 1000),
		stopCh:      make(chan struct{}),
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		var batch []string
		for {
			select {
			case line, ok := <-bb.inCh:
				if !ok {
					// Flush remaining
					for _, l := range batch {
						bb.broadcaster.BroadcastLine(l)
					}
					return
				}
				batch = append(batch, line)
			case <-ticker.C:
				if len(batch) > 0 {
					for _, l := range batch {
						bb.broadcaster.BroadcastLine(l)
					}
					batch = batch[:0]
				}
			case <-bb.stopCh:
				return
			}
		}
	}()

	return bb
}
