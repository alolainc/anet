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

package socket

import (
	"net"
	"net/netip"
	"testing"
)

func TestRecvFromDecodesSenderWithoutAllocating(t *testing.T) {
	for _, network := range []string{"udp4", "udp6"} {
		t.Run(network, func(t *testing.T) {
			addr := "127.0.0.1:0"
			if network == "udp6" {
				addr = "[::1]:0"
			}
			srv, err := net.ListenPacket(network, addr)
			if err != nil {
				t.Skip(err)
			}
			defer srv.Close()
			cli, err := net.Dial(network, srv.LocalAddr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			rc, _ := srv.(*net.UDPConn).SyscallConn()
			var fd int
			rc.Control(func(f uintptr) { fd = int(f) })

			want := netip.MustParseAddrPort(cli.LocalAddr().String())
			buf := make([]byte, 2048)
			recv := func() {
				cli.Write([]byte("datagram"))
				for {
					n, from, err := RecvFrom(fd, buf)
					if err != nil { // EAGAIN until the datagram is there
						continue
					}
					if string(buf[:n]) != "datagram" || from != want {
						t.Fatalf("got %q from %v, want from %v", buf[:n], from, want)
					}
					return
				}
			}
			recv()
			allocs := testing.AllocsPerRun(100, func() {
				n, from, err := RecvFrom(fd, buf) // nothing queued: EAGAIN path
				_, _, _ = n, from, err
			})
			if allocs != 0 {
				t.Fatalf("RecvFrom allocated %.1f times per call on the EAGAIN path", allocs)
			}
			// AllocsPerRun calls the function once more to warm up: queue a datagram for each call
			cli.Write([]byte("datagram"))
			cli.Write([]byte("datagram"))
			allocs = testing.AllocsPerRun(1, func() {
				for {
					if n, _, err := RecvFrom(fd, buf); err == nil && n > 0 {
						return
					}
				}
			})
			if allocs != 0 {
				t.Fatalf("RecvFrom allocated %.1f times for a datagram", allocs)
			}
		})
	}
}
