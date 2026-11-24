// Copyright 2024 Alola Inc.
// Copyright 2022 CloudWeGo Authors
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

package netpoll

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"

	"github.com/alolainc/anet/internal/errs"
	"github.com/alolainc/anet/internal/logging"
	"github.com/alolainc/anet/internal/socket"
)

func openPoll() (Poll, error) {
	return openDefaultPoll()
}

func openDefaultPoll() (*defaultPoll, error) {
	poll := new(defaultPoll)

	poll.buf = make([]byte, 8)
	p, err := EpollCreate(0)
	if err != nil {
		return nil, err
	}
	poll.fd = p

	r0, _, e0 := unix.Syscall(unix.SYS_EVENTFD2, 0, 0, 0)
	if e0 != 0 {
		_ = unix.Close(poll.fd)
		return nil, e0
	}

	poll.Reset = poll.reset
	poll.Handler = poll.handler
	poll.wop = &FDOperator{FD: int(r0)}

	if err = poll.Control(poll.wop, PollReadable); err != nil {
		_ = unix.Close(poll.wop.FD)
		_ = unix.Close(poll.fd)
		return nil, err
	}

	poll.opcache = newOperatorCache()
	return poll, nil
}

type defaultPoll struct {
	pollArgs
	fd      int            // epoll fd
	wop     *FDOperator    // eventfd, wake epoll_wait
	buf     []byte         // read wfd trigger msg
	trigger uint32         // trigger flag
	m       sync.Map       //nolint:unused // only used in go:race
	opcache *operatorCache // operator cache
	// fns for handle events
	Reset   func(size, caps int)
	Handler func(events []EpollEvent) (closed bool)
}

type pollArgs struct {
	size     int
	caps     int
	events   []EpollEvent
	barriers []socket.Barrier
	hups     []func(p Poll) error
}

func (a *pollArgs) reset(size, caps int) {
	a.size, a.caps = size, caps
	a.events, a.barriers = make([]EpollEvent, size), make([]socket.Barrier, size)
	for i := range a.barriers {
		a.barriers[i].Bs = make([][]byte, a.caps)
		a.barriers[i].Ivs = make([]unix.Iovec, a.caps)
	}
}

// Wait implements Poll.
func (p *defaultPoll) Wait() (err error) {
	// init
	caps, msec, n := socket.BarrierCap, -1, 0
	p.Reset(128, caps)
	// wait
	for {
		if n == p.size && p.size < 128*1024 {
			p.Reset(p.size<<1, caps)
		}
		n, err = EpollWait(p.fd, p.events, msec)
		if err != nil && err != unix.EINTR {
			return err
		}
		if n <= 0 {
			msec = -1
			runtime.Gosched()
			continue
		}
		msec = 0
		if p.Handler(p.events[:n]) {
			return nil
		}
		// we can make sure that there is no op remaining if Handler finished
		p.opcache.free()
	}
}

func (p *defaultPoll) handler(events []EpollEvent) (closed bool) {
	var triggerRead, triggerWrite, triggerHup, triggerError bool
	var err error
	for i := range events {
		operator := p.getOperator(0, events[i].GetDataPtr())
		if operator == nil || !operator.do() {
			continue
		}

		var totalRead int
		evt := events[i].Events
		triggerRead = evt&unix.EPOLLIN != 0
		triggerWrite = evt&unix.EPOLLOUT != 0
		triggerHup = evt&(unix.EPOLLHUP|unix.EPOLLRDHUP) != 0
		triggerError = evt&unix.EPOLLERR != 0

		// trigger or exit gracefully
		if operator.FD == p.wop.FD {
			// must clean trigger first
			unix.Read(p.wop.FD, p.buf)
			atomic.StoreUint32(&p.trigger, 0)
			// if closed & exit
			if p.buf[0] > 0 {
				unix.Close(p.wop.FD)
				unix.Close(p.fd)
				operator.done()
				return true
			}
			operator.done()
			continue
		}

		if triggerRead {
			if operator.OnRead != nil {
				// for non-connection
				operator.OnRead(p)
			} else if operator.Inputs != nil {
				// for connection
				bs := operator.Inputs(p.barriers[i].Bs)
				if len(bs) > 0 {
					n, err := socket.IORead(operator.FD, bs, p.barriers[i].Ivs)
					operator.InputAck(n)
					totalRead += n
					if err != nil {
						p.appendHup(operator)
						continue
					}
				}
			} else {
				logging.Printf("[anet]: operator has critical problem! event=%d operator=%v", evt, operator)
			}
		}
		if triggerHup {
			if triggerRead && operator.Inputs != nil {
				// read all left data if peer send and close
				var leftRead int
				// read all left data if peer send and close
				if leftRead, err = readall(operator, p.barriers[i]); err != nil && !errors.Is(err, errs.ErrEOF) {
					logging.Printf("[anet]: readall(fd=%d)=%d before close: %s", operator.FD, totalRead, err.Error())
				}
				totalRead += leftRead
			}
			// only close connection if no further read bytes
			if totalRead == 0 {
				p.appendHup(operator)
				continue
			}
		}
		if triggerError {
			// Under block-zerocopy, the kernel may give an error callback, which is not a real error, just an EAGAIN.
			// So here we need to check this error, if it is EAGAIN then do nothing, otherwise still mark as hup.
			if _, _, _, _, err := unix.Recvmsg(operator.FD, nil, nil, unix.MSG_ERRQUEUE); err != unix.EAGAIN {
				p.appendHup(operator)
			} else {
				operator.done()
			}
			continue
		}
		if triggerWrite {
			if operator.OnWrite != nil {
				// for non-connection
				operator.OnWrite(p)
			} else if operator.Outputs != nil {
				// for connection
				bs, _ := operator.Outputs(p.barriers[i].Bs)
				if len(bs) > 0 {
					n, err := socket.IOSend(operator.FD, bs, p.barriers[i].Ivs, false)
					operator.OutputAck(n)
					if err != nil {
						p.appendHup(operator)
						continue
					}
				}
			} else {
				logging.Printf("[anet]: operator has critical problem! event=%d operator=%v", evt, operator)
			}
		}
		operator.done()
	}
	// hup conns together to avoid blocking the poll.
	p.onhups()
	return false
}

// Close will write 10000000
func (p *defaultPoll) Close() error {
	_, err := unix.Write(p.wop.FD, []byte{1, 0, 0, 0, 0, 0, 0, 0})
	return err
}

// Trigger implements Poll.
func (p *defaultPoll) Trigger() error {
	if atomic.AddUint32(&p.trigger, 1) > 1 {
		return nil
	}
	// MAX(eventfd) = 0xfffffffffffffffe
	_, err := unix.Write(p.wop.FD, []byte{0, 0, 0, 0, 0, 0, 0, 1})
	return err
}

// Control implements Poll.
func (p *defaultPoll) Control(operator *FDOperator, event PollEvent) error {
	// DON'T move `fd=operator.FD` behind inuse() call, we can only access operator before op.inuse() for avoid race
	// G1:              G2:
	// op.inuse()       op.unused()
	// op.FD  -- T1     op.FD = 0  -- T2
	// T1 and T2 may happen together
	fd := operator.FD
	var op int
	var evt EpollEvent
	p.setOperator(evt.GetDataPtr(), operator)
	switch event {
	case PollReadable: // server accept a new connection and wait read
		operator.inuse()
		op, evt.Events = unix.EPOLL_CTL_ADD, unix.EPOLLIN|unix.EPOLLRDHUP|unix.EPOLLERR
	case PollWritable: // client create a new connection and wait connect finished
		operator.inuse()
		op, evt.Events = unix.EPOLL_CTL_ADD, EPOLLET|unix.EPOLLOUT|unix.EPOLLRDHUP|unix.EPOLLERR
	case PollDetach: // deregister
		p.delOperator(operator)
		op, evt.Events = unix.EPOLL_CTL_DEL, unix.EPOLLIN|unix.EPOLLOUT|unix.EPOLLRDHUP|unix.EPOLLERR
	case PollR2RW: // connection wait read/write
		op, evt.Events = unix.EPOLL_CTL_MOD, unix.EPOLLIN|unix.EPOLLOUT|unix.EPOLLRDHUP|unix.EPOLLERR
	case PollRW2R: // connection wait read
		op, evt.Events = unix.EPOLL_CTL_MOD, unix.EPOLLIN|unix.EPOLLRDHUP|unix.EPOLLERR
	}
	return EpollCtl(p.fd, op, fd, &evt)
}
