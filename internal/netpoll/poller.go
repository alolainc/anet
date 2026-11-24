// Copyright 2024 Alola Inc.
// Copyright 2023 CloudWeGo Authors
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

//go:build darwin || netbsd || freebsd || openbsd || dragonfly || linux

package netpoll

import (
	"github.com/alolainc/anet/internal/logging"
	"github.com/alolainc/anet/internal/socket"
)

func (p *defaultPoll) Alloc() (operator *FDOperator) {
	op := p.opcache.alloc()
	op.poll = p
	return op
}

func (p *defaultPoll) Free(operator *FDOperator) {
	p.opcache.freeable(operator)
}

func (p *defaultPoll) appendHup(operator *FDOperator) {
	p.hups = append(p.hups, operator.OnHup)
	p.detach(operator)
	operator.done()
}

// Deferrer is implemented by pollers that can run work after the current batch of events.
type Deferrer interface {
	// Later runs f after the current batch of events was handled, on a separate goroutine.
	// It must only be called from the poller goroutine, e.g. from an inline OnRequest handler.
	Later(f func())
}

var _ Deferrer = (*defaultPoll)(nil)

// Later runs f after the current batch of events was handled, on a separate goroutine.
// It must only be called from the poller goroutine, e.g. from an inline OnRequest handler.
func (p *defaultPoll) Later(f func()) {
	p.hups = append(p.hups, func(Poll) error { f(); return nil })
}

func (p *defaultPoll) detach(operator *FDOperator) {
	if err := operator.Control(PollDetach); err != nil {
		logging.Printf("[anet]: poller detach operator failed: %v", err)
	}
}

func (p *defaultPoll) onhups() {
	if len(p.hups) == 0 {
		return
	}
	hups := p.hups
	p.hups = nil
	go func(onhups []func(p Poll) error) {
		for i := range onhups {
			if onhups[i] != nil {
				onhups[i](p)
			}
		}
	}(hups)
}

// readall read all left data before close connection
func readall(op *FDOperator, br socket.Barrier) (total int, err error) {
	ivs := br.Ivs
	var n int
	for {
		bs := op.Inputs(br.Bs)
		if len(bs) == 0 {
			return total, nil
		}

		n, err = socket.IORead(op.FD, bs, ivs)
		op.InputAck(n)
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			// EAGAIN/EINTR: nothing more to drain right now. Do not spin;
			// the level-triggered poller fires again if the fd is still readable/hup.
			return total, nil
		}
	}
}
