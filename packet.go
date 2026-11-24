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
)

// OnPacket is called for every datagram a PacketLoop receives, with the socket it arrived on and the sender.
//
// data is only valid during the call: copy it to keep it. Reply with conn.WriteToUDPAddrPort(reply, from).
// On the poller backends the handler runs on the poller goroutine, like OnRequest in the default inline mode,
// so it must not block; hand slow work to another goroutine together with a copy of data.
// ctx is cancelled when the loop shuts down.
type OnPacket func(ctx context.Context, conn *net.UDPConn, data []byte, from netip.AddrPort)

// PacketLoop serves a UDP socket: the pollers wait for datagrams and call OnPacket for each one.
//
// Any *net.UDPConn can be served, so multicast and broadcast work by passing the socket that
// net.ListenMulticastUDP or net.ListenUDP returns.
type PacketLoop interface {
	// Serve serves conn until Shutdown, then returns nil. It returns an error if conn cannot be served.
	// A PacketLoop serves one socket.
	Serve(conn *net.UDPConn) error
	// Shutdown stops serving, waits for a running OnPacket call to finish and closes the socket.
	// Do not call it from OnPacket (it would wait for itself); start it in a goroutine instead.
	Shutdown(ctx context.Context) error
}

// NewPacketLoop creates a PacketLoop that calls onPacket for every datagram.
func NewPacketLoop(onPacket OnPacket) (PacketLoop, error) {
	if onPacket == nil {
		return nil, errors.New("anet: NewPacketLoop needs an OnPacket handler")
	}
	return platform.newPacketLoop(onPacket), nil
}

// maxDatagram is the largest UDP payload; each loop reads into one buffer of this size.
const maxDatagram = 64 << 10

var (
	errPacketServing = errors.New("anet: PacketLoop is already serving a socket")
	errPacketClosed  = errors.New("anet: PacketLoop is shut down")
)
