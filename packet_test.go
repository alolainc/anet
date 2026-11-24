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
	"bytes"
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// packetBackends returns every backend that can serve UDP on this platform.
func packetBackends() map[string]backend {
	b := map[string]backend{"portable": portableBackend{}}
	if _, ok := platform.(portableBackend); !ok {
		b["native"] = platform
	}
	return b
}

func udpEcho(ctx context.Context, conn *net.UDPConn, data []byte, from netip.AddrPort) {
	conn.WriteToUDPAddrPort(data, from)
}

// servePackets starts a PacketLoop of backend b on network/addr and returns the socket's address.
func servePackets(t *testing.T, b backend, network, addr string, h OnPacket) (*net.UDPAddr, PacketLoop) {
	t.Helper()
	conn, err := net.ListenUDP(network, mustUDPAddr(t, network, addr))
	if err != nil {
		t.Skipf("cannot listen on %s %s: %v", network, addr, err)
	}
	pl := b.newPacketLoop(h)
	served := make(chan error, 1)
	go func() { served <- pl.Serve(conn) }()
	t.Cleanup(func() {
		if err := pl.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("Serve returned %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("Serve did not return after Shutdown")
		}
	})
	return conn.LocalAddr().(*net.UDPAddr), pl
}

func mustUDPAddr(t *testing.T, network, addr string) *net.UDPAddr {
	a, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// roundTrip sends msg from a new client socket and waits for the echo.
func roundTrip(t *testing.T, network string, to *net.UDPAddr, msg []byte) {
	t.Helper()
	c, err := net.DialUDP(network, nil, to)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	buf := make([]byte, maxDatagram)
	for attempt := 0; attempt < 5; attempt++ { // UDP may drop; loopback rarely does
		c.Write(msg)
		c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, err := c.Read(buf)
		if err == nil {
			if !bytes.Equal(buf[:n], msg) {
				t.Fatalf("echo mismatch: %d bytes", n)
			}
			return
		}
	}
	t.Fatalf("no echo from %v", to)
}

func TestPacketLoopEcho(t *testing.T) {
	for name, b := range packetBackends() {
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct{ network, addr string }{
				{"udp4", "127.0.0.1:0"},
				{"udp6", "[::1]:0"},
			} {
				to, _ := servePackets(t, b, tc.network, tc.addr, udpEcho)
				roundTrip(t, tc.network, to, []byte("hello"))
				roundTrip(t, tc.network, to, bytes.Repeat([]byte{7}, 60000)) // near the largest datagram
			}
		})
	}
}

func TestPacketLoopSenderAddress(t *testing.T) {
	for name, b := range packetBackends() {
		t.Run(name, func(t *testing.T) {
			got := make(chan netip.AddrPort, 1)
			// "udp" on an unspecified address is a dual-stack IPv6 socket: IPv4 senders must still look like IPv4
			to, _ := servePackets(t, b, "udp", ":0", func(ctx context.Context, conn *net.UDPConn, data []byte, from netip.AddrPort) {
				select {
				case got <- from:
				default:
				}
			})
			c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: to.Port})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			c.Write([]byte("x"))
			select {
			case from := <-got:
				want := c.LocalAddr().(*net.UDPAddr).AddrPort()
				if from != want {
					t.Fatalf("sender %v, want %v", from, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("no datagram")
			}
		})
	}
}

func TestPacketLoopManySenders(t *testing.T) {
	for name, b := range packetBackends() {
		t.Run(name, func(t *testing.T) {
			to, _ := servePackets(t, b, "udp4", "127.0.0.1:0", udpEcho)
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					for j := 0; j < 20; j++ {
						roundTrip(t, "udp4", to, []byte(fmt.Sprintf("sender %d packet %d", i, j)))
					}
				}(i)
			}
			wg.Wait()
		})
	}
}

func TestPacketLoopSurvivesPanic(t *testing.T) {
	for name, b := range packetBackends() {
		t.Run(name, func(t *testing.T) {
			var calls int32
			to, _ := servePackets(t, b, "udp4", "127.0.0.1:0", func(ctx context.Context, conn *net.UDPConn, data []byte, from netip.AddrPort) {
				if atomic.AddInt32(&calls, 1) == 1 {
					panic("first datagram")
				}
				conn.WriteToUDPAddrPort(data, from)
			})
			c, err := net.DialUDP("udp4", nil, to)
			if err != nil {
				t.Fatal(err)
			}
			c.Write([]byte("boom")) // panics, must be recovered
			c.Close()
			roundTrip(t, "udp4", to, []byte("still serving"))
		})
	}
}

func TestPacketLoopLifecycle(t *testing.T) {
	for name, b := range packetBackends() {
		t.Run(name, func(t *testing.T) {
			conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			ctxDone := make(chan struct{})
			pl := b.newPacketLoop(func(ctx context.Context, conn *net.UDPConn, data []byte, from netip.AddrPort) {
				go func() { <-ctx.Done(); close(ctxDone) }()
			})
			served := make(chan error, 1)
			go func() { served <- pl.Serve(conn) }()

			// a second socket is refused while the first one is served
			time.Sleep(20 * time.Millisecond)
			other, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			defer other.Close()
			if err := pl.Serve(other); err != errPacketServing {
				t.Fatalf("second Serve: %v", err)
			}

			c, _ := net.DialUDP("udp4", nil, conn.LocalAddr().(*net.UDPAddr))
			c.Write([]byte("x"))
			c.Close()
			time.Sleep(50 * time.Millisecond)

			if err := pl.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := <-served; err != nil {
				t.Fatalf("Serve returned %v", err)
			}
			select {
			case <-ctxDone:
			case <-time.After(time.Second):
				t.Fatal("handler context not cancelled by Shutdown")
			}
			if _, err := conn.WriteToUDPAddrPort([]byte("x"), netip.MustParseAddrPort("127.0.0.1:9")); err == nil {
				t.Fatal("Shutdown must close the socket")
			}
			if err := pl.Shutdown(context.Background()); err != nil {
				t.Fatalf("second Shutdown: %v", err)
			}
			if err := pl.Serve(other); err != errPacketClosed {
				t.Fatalf("Serve after Shutdown: %v", err)
			}
		})
	}
}

func TestNewPacketLoopNeedsHandler(t *testing.T) {
	if _, err := NewPacketLoop(nil); err == nil {
		t.Fatal("want an error for a nil handler")
	}
}
