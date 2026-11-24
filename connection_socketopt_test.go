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
	"errors"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSocketOptionsAppliedToAcceptedConnections(t *testing.T) {
	network, address := "tcp", getTestAddress()
	got := make(chan *connection, 1)
	loop := newTestEventLoop(network, address, nil,
		WithSendBuffer(64<<10), WithRecvBuffer(32<<10), WithNotSentLowat(8<<10),
		WithOnPrepare(func(c Connection) context.Context {
			got <- c.(*connection)
			return context.Background()
		}))
	defer loop.Shutdown(context.Background())
	conn, err := DialConnection(network, address, time.Second)
	MustNil(t, err)
	defer conn.Close()

	var c *connection
	select {
	case c = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("no connection accepted")
	}
	snd, err := unix.GetsockoptInt(c.fd, unix.SOL_SOCKET, unix.SO_SNDBUF)
	MustNil(t, err)
	// Linux doubles the value for bookkeeping, darwin keeps it; either way it must be near what we asked for
	Assert(t, snd >= 64<<10 && snd <= 256<<10, snd)
	rcv, err := unix.GetsockoptInt(c.fd, unix.SOL_SOCKET, unix.SO_RCVBUF)
	MustNil(t, err)
	Assert(t, rcv >= 32<<10 && rcv <= 128<<10, rcv)
	checkNotSentLowat(t, c.fd, 8<<10)
}

func TestSocketTunerOnDialedConnection(t *testing.T) {
	network, address := "tcp", getTestAddress()
	loop := newTestEventLoop(network, address, nil)
	defer loop.Shutdown(context.Background())
	conn, err := DialConnection(network, address, time.Second)
	MustNil(t, err)
	defer conn.Close()

	st, ok := conn.(SocketTuner)
	Assert(t, ok, "connection must implement SocketTuner")
	MustNil(t, st.SetSendBuffer(48<<10))
	MustNil(t, st.SetRecvBuffer(48<<10))
	err = st.SetNotSentLowat(16 << 10)
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		MustNil(t, err)
	} else {
		Assert(t, err != nil, "expected ErrUnsupported")
	}
	Equal(t, st.PendingWrite(), 0)
}

// A unix socket is not TCP, so TCP_NOTSENT_LOWAT is refused instead of failing in the kernel.
func TestNotSentLowatRejectedOnUnixSocket(t *testing.T) {
	c := &connection{}
	c.network = "unix"
	err := c.SetNotSentLowat(1024)
	Assert(t, errors.Is(err, ErrUnsupported), err)
}
