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

// backend is the platform specific half of anet (the Bridge pattern): the public functions in anet.go are
// written once and delegate here. Unix systems use the reactor backend (epoll or kqueue pollers), other systems
// the portable backend built on package net.
type backend interface {
	initialize()
	setNumLoops(n int) error
	setLoadBalance(lb LoadBalance) error
	newEventLoop(opts *options) EventLoop
	dialer() Dialer
	createListener(network, addr string) (Listener, error)
	createReusePortListener(network, addr string) (Listener, error)
	convertListener(l net.Listener) (Listener, error)
	newPacketLoop(onPacket OnPacket) PacketLoop
}
