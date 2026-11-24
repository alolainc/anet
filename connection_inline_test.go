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

//go:build !windows
// +build !windows

package anet

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// frameEcho is a non-blocking handler for inline mode: it echoes complete [4-byte length][payload] frames
// and returns without consuming anything while a frame is still incomplete.
func frameEcho(ctx context.Context, c Connection) error {
	r := c.Reader()
	for {
		if r.Len() < 4 {
			return nil
		}
		hdr, err := r.Peek(4)
		if err != nil {
			return err
		}
		l := int(binary.BigEndian.Uint32(hdr))
		if r.Len() < 4+l {
			return nil
		}
		frame, err := r.ReadBinary(4 + l)
		if err != nil {
			return err
		}
		r.Release()
		w := c.Writer()
		if _, err = w.WriteBinary(frame); err != nil {
			return err
		}
		if err = w.Flush(); err != nil {
			return err
		}
	}
}

func inlineServe(t *testing.T, onRequest OnRequest) (addr string, stop func()) {
	t.Helper()
	ln, err := CreateListener("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	el, err := NewEventLoop(onRequest, WithInlineRequest())
	if err != nil {
		t.Fatal(err)
	}
	go el.Serve(ln)
	return ln.Addr().String(), func() { el.Shutdown(context.Background()) }
}

func frame(payload []byte) []byte {
	b := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(b, uint32(len(payload)))
	copy(b[4:], payload)
	return b
}

func TestInlinePartialFrames(t *testing.T) {
	addr, stop := inlineServe(t, frameEcho)
	defer stop()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	f := frame(bytes.Repeat([]byte("ab"), 500))
	for _, part := range [][]byte{f[:2], f[2:7], f[7:600], f[600:]} { // split inside the header and the body
		if _, err := c.Write(part); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
	}
	got := make([]byte, len(f))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if !bytes.Equal(got, f) {
		t.Fatal("echo mismatch")
	}
}

func TestInlinePipelined(t *testing.T) {
	addr, stop := inlineServe(t, frameEcho)
	defer stop()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var req bytes.Buffer
	const n = 2000
	for i := 0; i < n; i++ {
		req.Write(frame(bytes.Repeat([]byte{byte(i)}, 1+i%300)))
	}
	go c.Write(req.Bytes())
	got := make([]byte, req.Len())
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if !bytes.Equal(got, req.Bytes()) {
		t.Fatal("echo mismatch")
	}
}

// A slow reader makes the server's send hit EAGAIN. The poller must keep serving other connections
// and still deliver every byte once the slow client reads.
func TestInlineSlowReaderDoesNotBlockPoller(t *testing.T) {
	addr, stop := inlineServe(t, frameEcho)
	defer stop()

	slow, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	big := frame(bytes.Repeat([]byte("x"), 8<<20))
	writeDone := make(chan error, 1)
	go func() { _, err := slow.Write(big); writeDone <- err }()
	time.Sleep(300 * time.Millisecond) // the server echo cannot be fully sent while we do not read

	// another connection must still be served promptly
	fast, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer fast.Close()
	small := frame([]byte("ping"))
	fast.Write(small)
	got := make([]byte, len(small))
	fast.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(fast, got); err != nil {
		t.Fatalf("poller blocked by slow reader: %v", err)
	}

	// now drain the slow connection: all data must arrive intact
	echo := make([]byte, len(big))
	slow.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.ReadFull(slow, echo); err != nil {
		t.Fatalf("slow read: %v", err)
	}
	if !bytes.Equal(echo, big) {
		t.Fatal("slow echo mismatch")
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
}

func TestInlineReaderNeverBlocks(t *testing.T) {
	var calls, sawNotEnough int32
	addr, stop := inlineServe(t, func(ctx context.Context, c Connection) error {
		atomic.AddInt32(&calls, 1)
		start := time.Now()
		_, err := c.Reader().Next(10) // only 5 bytes are available
		if errors.Is(err, ErrNotEnoughData) {
			atomic.AddInt32(&sawNotEnough, 1)
		}
		if time.Since(start) > 500*time.Millisecond {
			t.Errorf("Reader blocked in inline mode")
		}
		if c.Reader().Len() != 5 {
			t.Errorf("Next must not consume on error, Len=%d", c.Reader().Len())
		}
		return nil
	})
	defer stop()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("12345"))
	time.Sleep(200 * time.Millisecond)
	if atomic.LoadInt32(&sawNotEnough) == 0 {
		t.Fatalf("expected ErrNotEnoughData, calls=%d", atomic.LoadInt32(&calls))
	}
	// A handler that consumes nothing must not be called in a loop.
	before := atomic.LoadInt32(&calls)
	time.Sleep(200 * time.Millisecond)
	if after := atomic.LoadInt32(&calls); after != before {
		t.Fatalf("handler spun: %d -> %d calls with no new data", before, after)
	}
}

func TestInlineManyConnections(t *testing.T) {
	addr, stop := inlineServe(t, frameEcho)
	defer stop()
	const conns, rounds = 50, 100
	errc := make(chan error, conns)
	for i := 0; i < conns; i++ {
		go func(i int) {
			c, err := net.Dial("tcp", addr)
			if err != nil {
				errc <- err
				return
			}
			defer c.Close()
			for r := 0; r < rounds; r++ {
				f := frame(bytes.Repeat([]byte{byte(i + r)}, 10+r*7))
				if _, err := c.Write(f); err != nil {
					errc <- err
					return
				}
				got := make([]byte, len(f))
				c.SetReadDeadline(time.Now().Add(5 * time.Second))
				if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, f) {
					errc <- errors.New("echo mismatch or read error")
					return
				}
			}
			errc <- nil
		}(i)
	}
	for i := 0; i < conns; i++ {
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	}
}
