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

import "net"

// portableBackend serves connections on top of package net: one read goroutine per connection, which the Go
// runtime drives with its own poller (IOCP on Windows). It has the same API and semantics as the reactor backend
// but not its goroutine savings for idle connections, and it ignores the poller settings. Handlers always run
// on the connection's goroutine. It is compiled everywhere so that it can be tested on any platform.
type portableBackend struct{}

func (portableBackend) initialize() {}

func (portableBackend) setNumLoops(int) error { return nil }

func (portableBackend) setLoadBalance(LoadBalance) error { return nil }

func (portableBackend) newEventLoop(opts *options) EventLoop { return newStdEventLoopWith(opts) }

func (portableBackend) dialer() Dialer { return newStdDialer() }

func (portableBackend) createListener(network, addr string) (Listener, error) {
	return newStdListener(network, addr)
}

// createReusePortListener returns a plain listener: the portable backend accepts on one goroutine driven by the
// Go runtime, so WithReusePort has nothing to spread and is ignored.
func (portableBackend) createReusePortListener(network, addr string) (Listener, error) {
	return newStdListener(network, addr)
}

func (portableBackend) convertListener(l net.Listener) (Listener, error) {
	return convertStdListener(l), nil
}

func (portableBackend) newPacketLoop(onPacket OnPacket) PacketLoop {
	return newPortablePacketLoop(onPacket)
}
