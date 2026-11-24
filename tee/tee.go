// Copyright 2024 Alola Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package tee fans one stream of frames out to many connections, like the GStreamer tee element.
//
// A source calls Tee.Write for every frame. Each subscriber has its own queue and writer goroutine, so a
// slow subscriber never delays the others. Frames larger than anet.BinaryInplaceThreshold are shared by
// all subscribers without copying.
package tee

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/alolainc/anet"
)

// Policy says what happens to a subscriber that cannot keep up.
type Policy int

const (
	// DropFrames skips frames for a slow subscriber until it catches up. Frames are never cut in half,
	// so use it for streams made of independent frames (video, telemetry, ticks).
	DropFrames Policy = iota
	// Disconnect closes a slow subscriber.
	Disconnect
	// DropOldest keeps the newest frames: when a subscriber is behind, the frames that waited longest in its
	// queue are discarded to make room. This keeps latency low for live streams.
	DropOldest
)

// Config configures a Tee. The zero value is usable.
type Config struct {
	// MaxPending is the number of bytes per subscriber that may wait to be sent (queued in the tee
	// plus flushed but unsent in the connection) before the subscriber counts as slow. Default 4 MiB.
	// This is a size limit; the time a frame waits is bounded by MaxAge. For low latency set both, and keep
	// MaxPending near the bytes your stream sends in MaxAge.
	MaxPending int
	// Policy is applied to a slow subscriber. Default DropFrames.
	Policy Policy
	// MaxAge bounds how long a frame may wait in a subscriber's queue. An older frame is discarded
	// instead of sent, whatever the Policy, so a subscriber never receives data that is already stale.
	// 0 means no limit. For live video, set it to a few frame intervals (for example 200ms): it is the
	// direct bound on the extra lag that a slow network can add.
	MaxAge time.Duration
	// OnSlow, if set, is called (without locks held) each time a frame is dropped or a subscriber is
	// disconnected because it is too slow.
	OnSlow func(conn anet.Connection, dropped bool)
}

const defaultMaxPending = 4 << 20

// Tee distributes frames to its subscribers. It is safe for concurrent use.
type Tee struct {
	cfg    Config
	mu     sync.RWMutex
	subs   map[anet.Connection]*subscriber
	closed bool

	frames  uint64
	dropped uint64
}

// New creates a Tee.
func New(cfg Config) *Tee {
	if cfg.MaxPending <= 0 {
		cfg.MaxPending = defaultMaxPending
	}
	return &Tee{cfg: cfg, subs: make(map[anet.Connection]*subscriber)}
}

// Add subscribes conn. The Tee takes over writing to conn: do not write to it elsewhere. A subscriber is removed
// when its connection closes or fails. It returns false if the Tee is closed, conn is nil or already inactive.
func (t *Tee) Add(conn anet.Connection) bool {
	if conn == nil || !conn.IsActive() {
		return false
	}
	s := &subscriber{t: t, conn: conn, wake: make(chan struct{}, 1), done: make(chan struct{})}
	if st, ok := conn.(anet.SocketTuner); ok {
		s.pending = st.PendingWrite
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return false
	}
	if _, dup := t.subs[conn]; dup {
		t.mu.Unlock()
		return true
	}
	t.subs[conn] = s
	t.mu.Unlock()

	conn.AddCloseCallback(func(anet.Connection) error {
		t.Remove(conn)
		return nil
	})
	go s.loop()
	return true
}

// Remove unsubscribes conn without closing it. Frames already queued for it are discarded.
func (t *Tee) Remove(conn anet.Connection) {
	t.mu.Lock()
	s := t.subs[conn]
	delete(t.subs, conn)
	t.mu.Unlock()
	if s != nil {
		s.stop()
	}
}

// Len returns the number of subscribers.
func (t *Tee) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.subs)
}

// Stats returns the number of frames written and the number of per-subscriber frames dropped as too slow.
func (t *Tee) Stats() (frames, dropped uint64) {
	return atomic.LoadUint64(&t.frames), atomic.LoadUint64(&t.dropped)
}

// Write sends frame to every subscriber and returns how many accepted it. It never blocks on a connection.
// The Tee keeps a reference to frame until it is sent, so the caller must not modify it afterwards.
func (t *Tee) Write(frame []byte) (accepted int) {
	if len(frame) == 0 {
		return 0
	}
	atomic.AddUint64(&t.frames, 1)
	t.mu.RLock()
	subs := make([]*subscriber, 0, len(t.subs))
	for _, s := range t.subs {
		subs = append(subs, s)
	}
	t.mu.RUnlock()

	for _, s := range subs {
		switch s.enqueue(frame) {
		case ok:
			accepted++
		case evicted:
			accepted++
			if t.cfg.OnSlow != nil {
				t.cfg.OnSlow(s.conn, true)
			}
		case slow:
			atomic.AddUint64(&t.dropped, 1)
			if t.cfg.Policy == Disconnect {
				if t.cfg.OnSlow != nil {
					t.cfg.OnSlow(s.conn, false)
				}
				t.Remove(s.conn)
				s.conn.Close()
			} else if t.cfg.OnSlow != nil {
				t.cfg.OnSlow(s.conn, true)
			}
		}
	}
	return accepted
}

// Forward reads n bytes from r (copying them, then releasing them from r) and writes them as one frame.
func (t *Tee) Forward(r anet.Reader, n int) (accepted int, err error) {
	frame, err := r.ReadBinary(n)
	if err != nil {
		return 0, err
	}
	if err = r.Release(); err != nil {
		return 0, err
	}
	return t.Write(frame), nil
}

// Close removes all subscribers (without closing their connections) and rejects new ones.
func (t *Tee) Close() {
	t.mu.Lock()
	t.closed = true
	subs := t.subs
	t.subs = make(map[anet.Connection]*subscriber)
	t.mu.Unlock()
	for _, s := range subs {
		s.stop()
	}
}

type result int

const (
	ok      result = iota
	evicted        // accepted, after discarding older frames (DropOldest)
	slow
	gone
)

type entry struct {
	frame []byte
	at    time.Time
}

type subscriber struct {
	t       *Tee
	conn    anet.Connection
	pending func() int // bytes flushed into the connection but not sent yet; nil if unknown

	mu      sync.Mutex
	queue   []entry
	queued  int
	stopped bool
	wake    chan struct{}
	done    chan struct{}
}

func (s *subscriber) enqueue(frame []byte) result {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return gone
	}
	pending := 0
	if s.pending != nil {
		pending = s.pending()
	}
	res := ok
	if s.queued+pending+len(frame) > s.t.cfg.MaxPending && s.queued+pending > 0 {
		if s.t.cfg.Policy != DropOldest {
			s.mu.Unlock()
			return slow
		}
		// make room by discarding the oldest queued frames; whatever is already in the connection stays
		for len(s.queue) > 0 && s.queued+pending+len(frame) > s.t.cfg.MaxPending {
			s.queued -= len(s.queue[0].frame)
			s.queue[0] = entry{}
			s.queue = s.queue[1:]
			atomic.AddUint64(&s.t.dropped, 1)
			res = evicted
		}
	}
	s.queue = append(s.queue, entry{frame: frame, at: time.Now()})
	s.queued += len(frame)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return res
}

func (s *subscriber) stop() {
	s.mu.Lock()
	if !s.stopped {
		s.stopped = true
		s.queue, s.queued = nil, 0
		close(s.done)
	}
	s.mu.Unlock()
}

// loop writes queued frames in batches, one Flush per batch.
func (s *subscriber) loop() {
	for {
		select {
		case <-s.done:
			return
		case <-s.wake:
		}
		for {
			s.mu.Lock()
			batch := s.queue
			s.queue = nil
			s.mu.Unlock()
			if len(batch) == 0 {
				break
			}
			total := 0
			stale := 0
			w := s.conn.Writer()
			var err error
			now := time.Now()
			for _, e := range batch {
				total += len(e.frame)
				if maxAge := s.t.cfg.MaxAge; maxAge > 0 && now.Sub(e.at) > maxAge {
					stale++ // waited too long, sending it would only add lag
					continue
				}
				if err == nil {
					_, err = w.WriteBinary(e.frame)
				}
			}
			if stale > 0 {
				atomic.AddUint64(&s.t.dropped, uint64(stale))
				if s.t.cfg.OnSlow != nil {
					s.t.cfg.OnSlow(s.conn, true)
				}
			}
			if err == nil {
				err = w.Flush()
			}
			// Once flushed, the bytes count as pending in the connection (when it reports that), not as queued here.
			s.mu.Lock()
			if !s.stopped {
				s.queued -= total
			}
			s.mu.Unlock()
			if err != nil {
				s.t.Remove(s.conn)
				s.conn.Close()
				return
			}
		}
	}
}
