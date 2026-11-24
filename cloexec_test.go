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
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// assertCloexecNonblock checks that fd would not leak into a child process and does not block.
func assertCloexecNonblock(t *testing.T, what string, fd int) {
	t.Helper()
	fdflags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	MustNil(t, err)
	Assert(t, fdflags&unix.FD_CLOEXEC != 0, what+": FD_CLOEXEC not set, the descriptor leaks into exec'd children")
	flflags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	MustNil(t, err)
	Assert(t, flflags&unix.O_NONBLOCK != 0, what+": O_NONBLOCK not set")
}

func TestDescriptorsAreCloexec(t *testing.T) {
	network, address := "tcp", getTestAddress()
	accepted := make(chan int, 1)
	loop := newTestEventLoop(network, address, nil, WithOnPrepare(func(c Connection) context.Context {
		accepted <- c.(*connection).fd
		return context.Background()
	}))
	defer loop.Shutdown(context.Background())

	conn, err := DialConnection(network, address, time.Second) // created by anet's own socket code
	MustNil(t, err)
	defer conn.Close()
	assertCloexecNonblock(t, "dialed", conn.(*TCPConnection).fd)

	select {
	case fd := <-accepted:
		assertCloexecNonblock(t, "accepted", fd)
	case <-time.After(2 * time.Second):
		t.Fatal("no connection accepted")
	}
}
