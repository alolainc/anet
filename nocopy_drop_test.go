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

package anet

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// fill simulates the poller: book memory, write data into it, ack it.
func fill(b *LinkBuffer, data []byte) {
	p := b.book(len(data), len(data))
	copy(p, data)
	b.bookAck(len(data))
}

func TestDropIfDrainedAfterCopyingReads(t *testing.T) {
	b := NewLinkBuffer()
	fill(b, []byte("hello"))
	Assert(t, !b.dropIfDrained(), "data left: must keep")
	s, err := b.ReadString(5)
	MustNil(t, err)
	Equal(t, s, "hello")
	Assert(t, b.dropIfDrained(), "drained by a copying read: must drop")
	Equal(t, cap(b.write.buf), 0)
	// the buffer keeps working: the next book takes memory again
	fill(b, []byte("again"))
	s, _ = b.ReadString(5)
	Equal(t, s, "again")
}

func TestDropIfDrainedWaitsForRelease(t *testing.T) {
	b := NewLinkBuffer()
	fill(b, []byte("abcd"))
	p, err := b.Next(4) // points into the node
	MustNil(t, err)
	Assert(t, !b.dropIfDrained(), "Next without Release: the slice is still valid, must keep")
	Equal(t, string(p), "abcd")
	MustNil(t, b.Release())
	Assert(t, b.dropIfDrained(), "after Release: must drop")

	fill(b, []byte("wxyz"))
	_, err = b.Peek(2)
	MustNil(t, err)
	MustNil(t, b.Skip(4))
	Assert(t, !b.dropIfDrained(), "Peek without Release: must keep")
	b.Release()
	Assert(t, b.dropIfDrained(), "after Release: must drop")
}

func TestDropIfDrainedKeepsSlicedData(t *testing.T) {
	b := NewLinkBuffer()
	msg := bytes.Repeat([]byte("sliced-data-"), 100)
	fill(b, msg)
	r, err := b.Slice(len(msg)) // a reader kept by the application, e.g. handed to another goroutine
	MustNil(t, err)
	MustNil(t, b.Release())
	Assert(t, b.dropIfDrained(), "slice is reference counted: the buffer may drop its own reference")

	// churn the pool so that freed memory would be reused and overwritten
	for i := 0; i < 64; i++ {
		x := NewLinkBuffer()
		fill(x, bytes.Repeat([]byte{0xEE}, len(msg)))
		x.Skip(len(msg))
		x.Release()
		x.dropIfDrained()
	}
	got, err := r.Next(len(msg))
	MustNil(t, err)
	Assert(t, bytes.Equal(got, msg), "sliced data was overwritten after the drop")
	r.Release()
}

func TestDropIfDrainedRefusesPendingWrite(t *testing.T) {
	b := NewLinkBuffer()
	_, err := b.Malloc(8) // written but not flushed
	MustNil(t, err)
	Assert(t, !b.dropIfDrained(), "pending malloc: must keep")
}

// After an inline request is handled, a connection waiting for the next one holds no read buffer.
func TestInlineConnectionDropsDrainedBuffer(t *testing.T) {
	network, address := "tcp", getTestAddress()
	conns := make(chan *connection, 1)
	loop := newTestEventLoop(network, address, func(ctx context.Context, c Connection) error {
		select {
		case conns <- c.(*connection):
		default:
		}
		msg, err := c.Reader().ReadString(c.Reader().Len())
		if err != nil {
			return err
		}
		c.Writer().WriteString(msg)
		return c.Writer().Flush()
	})
	defer loop.Shutdown(context.Background())

	cli, err := DialConnection(network, address, time.Second)
	MustNil(t, err)
	defer cli.Close()
	for i := 0; i < 3; i++ { // the buffer is taken again for each request and given back after it
		cli.Writer().WriteString("ping")
		MustNil(t, cli.Writer().Flush())
		cli.SetReadTimeout(time.Second)
		got, err := cli.Reader().ReadString(4)
		MustNil(t, err)
		Equal(t, got, "ping")
	}
	srv := <-conns
	// the handler flushes the reply before it returns, so the buffer is given back just after the reply
	deadline := time.Now().Add(2 * time.Second)
	for srv.inputBuffer.heldBytes() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("idle connection still holds %d bytes of read buffer", srv.inputBuffer.heldBytes())
		}
		time.Sleep(time.Millisecond)
	}
}
