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

package tee

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/alolainc/anet"
)

// startServer returns the address of a server that subscribes every accepted connection to tee t.
func startServer(t *testing.T, tee *Tee) (addr string, stop func()) {
	t.Helper()
	ln, err := anet.CreateListener("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	el, err := anet.NewEventLoop(nil, anet.WithOnConnect(func(ctx context.Context, c anet.Connection) context.Context {
		tee.Add(c)
		return ctx
	}))
	if err != nil {
		t.Fatal(err)
	}
	go el.Serve(ln)
	return ln.Addr().String(), func() { el.Shutdown(context.Background()) }
}

func dial(t *testing.T, addr string) anet.Connection {
	t.Helper()
	c, err := anet.DialConnection("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// dialIdle opens a plain TCP connection that never reads. anet clients read eagerly in the background,
// so a stalled peer has to be a raw socket.
func dialIdle(t *testing.T, addr string) net.Conn { return dialIdleBuf(t, addr, 4096) }

func dialIdleBuf(t *testing.T, addr string, rcvbuf int) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c.(*net.TCPConn).SetReadBuffer(rcvbuf)
	return c
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// frame builds a frame of the given payload size whose first 4 bytes hold its sequence number.
func frame(seq, size int) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = byte(seq + i)
	}
	binary.BigEndian.PutUint32(b, uint32(seq))
	return b
}

func TestFanOutDeliversEveryFrameInOrder(t *testing.T) {
	tee := New(Config{})
	defer tee.Close()
	addr, stop := startServer(t, tee)
	defer stop()

	const subs, frames = 5, 300
	conns := make([]anet.Connection, subs)
	for i := range conns {
		conns[i] = dial(t, addr)
		defer conns[i].Close()
	}
	waitFor(t, "subscribers", func() bool { return tee.Len() == subs })

	sizes := []int{16, 1000, 9000, 70000} // small frames are copied, large ones are shared
	var wg sync.WaitGroup
	errs := make(chan string, subs)
	for _, c := range conns {
		wg.Add(1)
		go func(c anet.Connection) {
			defer wg.Done()
			c.SetReadTimeout(5 * time.Second)
			for seq := 0; seq < frames; seq++ {
				want := frame(seq, sizes[seq%len(sizes)])
				got, err := c.Reader().ReadBinary(len(want))
				if err != nil {
					errs <- err.Error()
					return
				}
				if !bytes.Equal(got, want) {
					errs <- "frame content or order mismatch"
					return
				}
				c.Reader().Release()
			}
		}(c)
	}
	for seq := 0; seq < frames; seq++ {
		if n := tee.Write(frame(seq, sizes[seq%len(sizes)])); n != subs {
			t.Fatalf("frame %d accepted by %d of %d", seq, n, subs)
		}
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

func TestSlowSubscriberDoesNotBlockOthers(t *testing.T) {
	var slowCalls int32
	var mu sync.Mutex
	tee := New(Config{MaxPending: 256 << 10, Policy: DropFrames, OnSlow: func(anet.Connection, bool) {
		mu.Lock()
		slowCalls++
		mu.Unlock()
	}})
	defer tee.Close()
	addr, stop := startServer(t, tee)
	defer stop()

	fast, idle := dial(t, addr), dialIdle(t, addr)
	defer fast.Close()
	defer idle.Close()
	waitFor(t, "subscribers", func() bool { return tee.Len() == 2 })

	const frames, size = 400, 64 << 10 // 25 MiB, far more than the idle peer's socket buffers hold
	got := make(chan int, 1)
	go func() {
		fast.SetReadTimeout(10 * time.Second)
		n := 0
		for seq := 0; seq < frames; seq++ {
			b, err := fast.Reader().ReadBinary(size)
			if err != nil || !bytes.Equal(b, frame(seq, size)) {
				break
			}
			fast.Reader().Release()
			n++
		}
		got <- n
	}()

	start := time.Now()
	for seq := 0; seq < frames; seq++ {
		tee.Write(frame(seq, size))
		time.Sleep(time.Millisecond) // let the fast reader keep up
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Write was held up by the slow subscriber: %v", d)
	}
	if n := <-got; n != frames {
		t.Fatalf("fast subscriber received %d of %d frames", n, frames)
	}
	_, dropped := tee.Stats()
	if dropped == 0 {
		t.Fatal("no frame was dropped for the idle subscriber")
	}
	mu.Lock()
	defer mu.Unlock()
	if slowCalls == 0 {
		t.Fatal("OnSlow not called")
	}
	if tee.Len() != 2 {
		t.Fatalf("DropFrames must keep the slow subscriber, Len=%d", tee.Len())
	}
}

func TestDisconnectPolicyClosesSlowSubscriber(t *testing.T) {
	slowConn := make(chan anet.Connection, 1)
	tee := New(Config{MaxPending: 256 << 10, Policy: Disconnect, OnSlow: func(c anet.Connection, dropped bool) {
		if dropped {
			t.Error("Disconnect policy must not report a drop")
		}
		select {
		case slowConn <- c:
		default:
		}
	}})
	defer tee.Close()
	addr, stop := startServer(t, tee)
	defer stop()

	idle := dialIdle(t, addr)
	defer idle.Close()
	waitFor(t, "subscriber", func() bool { return tee.Len() == 1 })

	for seq := 0; seq < 400 && tee.Len() == 1; seq++ {
		tee.Write(frame(seq, 64<<10))
		time.Sleep(time.Millisecond)
	}
	waitFor(t, "slow subscriber removed", func() bool { return tee.Len() == 0 })
	select {
	case c := <-slowConn:
		waitFor(t, "slow connection closed", func() bool { return !c.IsActive() })
	default:
		t.Fatal("OnSlow not called")
	}
}

func TestClientCloseAndRemove(t *testing.T) {
	tee := New(Config{})
	defer tee.Close()
	addr, stop := startServer(t, tee)
	defer stop()

	a, b := dial(t, addr), dial(t, addr)
	defer b.Close()
	waitFor(t, "subscribers", func() bool { return tee.Len() == 2 })

	a.Close() // a peer that leaves is dropped from the tee
	waitFor(t, "closed peer removed", func() bool {
		tee.Write([]byte("x"))
		return tee.Len() == 1
	})

	// Remove stops delivery but keeps the connection open
	b.SetReadTimeout(time.Second)
	if n := tee.Write([]byte("y")); n != 1 {
		t.Fatalf("accepted %d", n)
	}
	if _, err := b.Reader().Next(1); err != nil {
		t.Fatal(err)
	}
	b.Reader().Release()
	tee.Close()
	if tee.Len() != 0 || tee.Write([]byte("z")) != 0 {
		t.Fatal("closed tee must have no subscribers")
	}
}

func TestForward(t *testing.T) {
	tee := New(Config{})
	defer tee.Close()
	addr, stop := startServer(t, tee)
	defer stop()
	c := dial(t, addr)
	defer c.Close()
	waitFor(t, "subscriber", func() bool { return tee.Len() == 1 })

	src := anet.NewLinkBuffer()
	src.WriteString("hello tee")
	src.Flush()
	if n, err := tee.Forward(src, 9); err != nil || n != 1 {
		t.Fatalf("forward: n=%d err=%v", n, err)
	}
	c.SetReadTimeout(time.Second)
	got, err := c.Reader().ReadString(9)
	if err != nil || got != "hello tee" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

// stalledSetup subscribes a raw peer that does not read yet and fills its socket buffers, so the writer is
// blocked in Flush and further frames wait in the tee queue. It returns a function that drains the peer.
func stalledSetup(t *testing.T, tee *Tee) (drain func(until []byte) bool, stop func()) {
	t.Helper()
	addr, stopServer := startServer(t, tee)
	peer := dialIdleBuf(t, addr, 256<<10) // large enough that draining it is fast
	waitFor(t, "subscriber", func() bool { return tee.Len() == 1 })
	for i := 0; i < 400; i++ { // 25 MiB of filler, more than the socket buffers hold
		tee.Write(frame(i, 64<<10))
		time.Sleep(time.Millisecond)
	}
	return func(until []byte) bool {
			peer.SetReadDeadline(time.Now().Add(15 * time.Second))
			var all []byte
			buf := make([]byte, 256<<10)
			for {
				n, err := peer.Read(buf)
				all = append(all, buf[:n]...)
				if bytes.Contains(all, until) {
					return true
				}
				if err != nil {
					return false
				}
			}
		}, func() {
			peer.Close()
			stopServer()
		}
}

func marked(magic string, size int) []byte {
	b := make([]byte, size)
	copy(b, magic)
	copy(b[size-len(magic):], magic)
	return b
}

func TestDropOldestKeepsNewestFrame(t *testing.T) {
	tee := New(Config{MaxPending: 512 << 10, Policy: DropOldest})
	defer tee.Close()
	drain, stop := stalledSetup(t, tee)
	defer stop()

	for i := 0; i < 30; i++ { // far more than MaxPending while the peer is stalled
		tee.Write(marked("OLDFRAME", 64<<10))
	}
	tee.Write(marked("NEWFRAME", 64<<10))
	if _, dropped := tee.Stats(); dropped == 0 {
		t.Fatal("nothing was dropped")
	}
	if !drain([]byte("NEWFRAME")) {
		t.Fatal("the newest frame must reach the subscriber")
	}
}

func TestMaxAgeDiscardsStaleFrames(t *testing.T) {
	tee := New(Config{MaxPending: 64 << 20, MaxAge: 50 * time.Millisecond})
	defer tee.Close()
	drain, stop := stalledSetup(t, tee)
	defer stop()

	tee.Write(marked("STALEFRM", 1000)) // queued behind the stalled writer
	time.Sleep(150 * time.Millisecond)  // now older than MaxAge
	// Frames are checked for age when the writer picks them up, which happens once the peer reads again.
	done := make(chan bool, 1)
	go func() { done <- drain([]byte("FRESHFRM")) }()
	time.Sleep(50 * time.Millisecond)
	tee.Write(marked("FRESHFRM", 1000))
	if !<-done {
		t.Fatal("the fresh frame must reach the subscriber")
	}
	if _, dropped := tee.Stats(); dropped == 0 {
		t.Fatal("stale frames were not discarded")
	}
}
