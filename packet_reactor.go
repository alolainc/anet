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
	"net"
	"net/netip"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"

	"github.com/alolainc/anet/internal/logging"
	"github.com/alolainc/anet/internal/netpoll"
	"github.com/alolainc/anet/internal/socket"
)

// maxPacketsPerEvent bounds the datagrams read per readiness event, so that one busy socket does not starve
// the other descriptors of its poller. The poller is level-triggered and reports the socket again.
const maxPacketsPerEvent = 64

// reactorPacketLoop reads datagrams on a poller with recvfrom and calls OnPacket on the poller goroutine.
type reactorPacketLoop struct {
	onPacket OnPacket
	ctx      context.Context
	cancel   context.CancelFunc
	stop     chan struct{}

	mu sync.Mutex
	// conn, op and buf are set once by Serve before the socket is registered and never change afterwards,
	// so the poller reads them without the lock.
	conn     *net.UDPConn
	op       FDOperator
	buf      []byte
	closed   bool // Shutdown was called
	stopping atomic.Bool
}

func newReactorPacketLoop(onPacket OnPacket) PacketLoop {
	ctx, cancel := context.WithCancel(context.Background())
	return &reactorPacketLoop{onPacket: onPacket, ctx: ctx, cancel: cancel, stop: make(chan struct{})}
}

// Serve implements PacketLoop.
func (l *reactorPacketLoop) Serve(conn *net.UDPConn) error {
	rc, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	fd := -1
	if err = rc.Control(func(f uintptr) { fd = int(f) }); err != nil {
		return err
	}
	l.mu.Lock()
	switch {
	case l.closed:
		l.mu.Unlock()
		return errPacketClosed
	case l.conn != nil:
		l.mu.Unlock()
		return errPacketServing
	}
	// The Go runtime keeps the socket non-blocking and registered with its own poller; that is harmless
	// because only this loop reads it, while writes from OnPacket go through the runtime as usual.
	l.conn, l.buf = conn, make([]byte, maxDatagram)
	l.op = FDOperator{FD: fd, OnRead: l.onRead, OnHup: func(Poll) error { return nil }}
	netpoll.Bind(&l.op, netpoll.Default.Pick())
	if err = l.op.Control(PollReadable); err != nil {
		l.conn, l.buf = nil, nil // never registered, so the poller never saw them
		l.mu.Unlock()
		return err
	}
	l.mu.Unlock()
	<-l.stop
	return nil
}

// onRead runs on the poller goroutine when the socket is readable.
func (l *reactorPacketLoop) onRead(Poll) error {
	for i := 0; i < maxPacketsPerEvent && !l.stopping.Load(); i++ {
		n, from, err := socket.RecvFrom(l.op.FD, l.buf)
		switch err {
		case nil:
			l.call(l.buf[:n], from)
		case unix.EINTR:
		case unix.EAGAIN:
			return nil
		case unix.ECONNREFUSED:
			// an ICMP error for an earlier datagram we sent; it says nothing about the next one
		default:
			logging.Printf("[anet]: recvfrom on udp fd=%d failed: %v", l.op.FD, err)
			return nil
		}
	}
	return nil
}

func (l *reactorPacketLoop) call(data []byte, from netip.AddrPort) {
	defer func() {
		if r := recover(); r != nil { // keep serving the other descriptors of this poller
			logging.Printf("[anet]: panic in OnPacket from %v: %v\n%s", from, r, debug.Stack())
		}
	}()
	l.onPacket(l.ctx, l.conn, data, from)
}

// Shutdown implements PacketLoop.
func (l *reactorPacketLoop) Shutdown(ctx context.Context) error {
	l.mu.Lock()
	conn := l.conn
	first := !l.closed
	l.closed = true
	l.mu.Unlock()
	if !first {
		return nil
	}
	l.cancel()
	defer close(l.stop)
	if conn == nil { // never served
		return nil
	}
	l.stopping.Store(true)
	if err := l.op.Control(PollDetach); err != nil {
		logging.Printf("[anet]: udp detach failed: %v", err)
	}
	// Wait until the poller is not inside onRead, so it never reads a descriptor that is being closed or reused.
	for !netpoll.Do(&l.op) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			runtime.Gosched()
		}
	}
	netpoll.Done(&l.op)
	return conn.Close()
}
