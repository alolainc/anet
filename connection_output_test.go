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
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// An inline connection written from another goroutine while the poller sends the rest of an earlier Flush:
// the stream must arrive complete, in order and without duplicates.
func TestInlineFlushFromGoroutineWhilePollerSends(t *testing.T) {
	const chunks, size = 200, 64 << 10
	network, address := "tcp", getTestAddress()
	loop := newTestEventLoop(network, address, func(ctx context.Context, c Connection) error {
		c.Reader().Skip(c.Reader().Len())
		go func() { // e.g. an asynchronous reply or a broadcast
			buf := make([]byte, size)
			for i := 0; i < chunks; i++ {
				for j := 0; j < size; j += 8 {
					binary.BigEndian.PutUint64(buf[j:], uint64(i*size+j))
				}
				if _, err := c.Writer().WriteBinary(append([]byte(nil), buf...)); err != nil {
					return
				}
				if err := c.Writer().Flush(); err != nil {
					return
				}
			}
		}()
		return nil
	}) // inline by default: no OnConnect
	defer loop.Shutdown(context.Background())

	raw, err := net.Dial(network, address)
	MustNil(t, err)
	defer raw.Close()
	raw.(*net.TCPConn).SetReadBuffer(16 << 10) // a slow reader, so Flush leaves data to the poller
	raw.Write([]byte("go"))
	time.Sleep(100 * time.Millisecond)

	raw.SetReadDeadline(time.Now().Add(20 * time.Second))
	got := make([]byte, chunks*size)
	_, err = io.ReadFull(raw, got)
	MustNil(t, err)
	for off := 0; off < len(got); off += 8 {
		if v := binary.BigEndian.Uint64(got[off:]); v != uint64(off) {
			t.Fatalf("byte %d: got sequence %d (stream corrupted or duplicated)", off, v)
		}
	}
	// nothing more may follow
	raw.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, _ := raw.Read(make([]byte, 1)); n != 0 {
		t.Fatal("extra bytes after the stream: data was sent twice")
	}
}
