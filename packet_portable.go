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

package anet

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"runtime/debug"
	"sync"
	"syscall"

	"github.com/alolainc/anet/internal/logging"
)

// portablePacketLoop reads datagrams on one goroutine with net.UDPConn. It is used where anet has no poller.
type portablePacketLoop struct {
	onPacket OnPacket
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{} // closed when Serve's read loop returned

	mu     sync.Mutex
	conn   *net.UDPConn
	closed bool
}

func newPortablePacketLoop(onPacket OnPacket) PacketLoop {
	ctx, cancel := context.WithCancel(context.Background())
	return &portablePacketLoop{onPacket: onPacket, ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

// Serve implements PacketLoop.
func (l *portablePacketLoop) Serve(conn *net.UDPConn) error {
	l.mu.Lock()
	switch {
	case l.closed:
		l.mu.Unlock()
		return errPacketClosed
	case l.conn != nil:
		l.mu.Unlock()
		return errPacketServing
	}
	l.conn = conn
	l.mu.Unlock()
	defer close(l.done)

	buf := make([]byte, maxDatagram)
	for {
		n, from, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if l.ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			if isICMPError(err) {
				continue // an ICMP error for an earlier datagram we sent; it says nothing about the next one
			}
			return err
		}
		// a dual-stack socket reports IPv4 senders as IPv4-mapped IPv6; report them as IPv4 like the pollers do
		l.call(conn, buf[:n], netip.AddrPortFrom(from.Addr().Unmap(), from.Port()))
	}
}

func (l *portablePacketLoop) call(conn *net.UDPConn, data []byte, from netip.AddrPort) {
	defer func() {
		if r := recover(); r != nil {
			logging.Printf("[anet]: panic in OnPacket from %v: %v\n%s", from, r, debug.Stack())
		}
	}()
	l.onPacket(l.ctx, conn, data, from)
}

// Shutdown implements PacketLoop.
func (l *portablePacketLoop) Shutdown(ctx context.Context) error {
	l.mu.Lock()
	conn := l.conn
	first := !l.closed
	l.closed = true
	l.mu.Unlock()
	if !first {
		return nil
	}
	l.cancel()
	if conn == nil {
		return nil
	}
	err := conn.Close() // unblocks ReadFromUDPAddrPort
	select {
	case <-l.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// wsaeconnreset is what Windows reports on a UDP socket after an ICMP port unreachable.
const wsaeconnreset = syscall.Errno(10054)

func isICMPError(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, wsaeconnreset)
}
