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

import "github.com/alolainc/anet/internal/netpoll"

// The poller types live in internal/netpoll. They are re-exported because Configure, SetLoadBalance and
// low-level users refer to them.
type (
	// Poll monitors file descriptors and calls their FDOperator when they are ready.
	Poll = netpoll.Poll
	// PollEvent is an operation on a file descriptor registered with a Poll.
	PollEvent = netpoll.PollEvent
	// FDOperator binds a file descriptor to the callbacks a Poll calls.
	FDOperator = netpoll.FDOperator
	// LoadBalance chooses how connections are spread over the pollers.
	LoadBalance = netpoll.LoadBalance
)

// Poll events.
const (
	PollReadable = netpoll.PollReadable // the fd is registered and waits to be readable
	PollWritable = netpoll.PollWritable // the fd is registered and waits to be writable (connect)
	PollDetach   = netpoll.PollDetach   // the fd is removed from the poller
	PollR2RW     = netpoll.PollR2RW     // the fd also waits to be writable
	PollRW2R     = netpoll.PollRW2R     // the fd only waits to be readable again
)

// Load balancing methods.
const (
	RoundRobin = netpoll.RoundRobin
	Random     = netpoll.Random
)
