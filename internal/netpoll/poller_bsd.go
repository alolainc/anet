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

//go:build darwin || netbsd || freebsd || openbsd || dragonfly

package netpoll

import (
	"errors"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/alolainc/anet/internal/errs"
	"github.com/alolainc/anet/internal/logging"
	"github.com/alolainc/anet/internal/socket"
)

func openPoll() (Poll, error) {
	return openDefaultPoll()
}

func openDefaultPoll() (*defaultPoll, error) {
	l := new(defaultPoll)
	p, err := unix.Kqueue()
	if err != nil {
		return nil, err
	}
	l.fd = p
	if err = l.openWaker(); err != nil {
		unix.Close(l.fd)
		return nil, err
	}
	l.opcache = newOperatorCache()
	return l, nil
}

type defaultPoll struct {
	fd      int
	wakeFd  int // ident of the wake-up event: 0 for EVFILT_USER, else the read end of the wake-up pipe
	wakeW   int // the write end of the wake-up pipe, if any
	trigger uint32
	m       sync.Map       //nolint:unused // only used in go:race
	opcache *operatorCache // operator cache
	hups    []func(p Poll) error
}

// Wait implements Poll.
func (p *defaultPoll) Wait() error {
	// init
	size, caps := 1024, socket.BarrierCap
	events, barriers := make([]unix.Kevent_t, size), make([]socket.Barrier, size)
	for i := range barriers {
		barriers[i].Bs = make([][]byte, caps)
		barriers[i].Ivs = make([]unix.Iovec, caps)
	}
	// wait
	var triggerRead, triggerWrite, triggerHup bool
	for {
		n, err := unix.Kevent(p.fd, nil, events, nil)
		if err != nil && err != unix.EINTR {
			// exit gracefully
			if err == unix.EBADF {
				return nil
			}
			return err
		}
		for i := 0; i < n; i++ {
			fd := int(events[i].Ident)
			// trigger
			if fd == p.wakeFd {
				// clean trigger
				p.drainWaker()
				atomic.StoreUint32(&p.trigger, 0)
				continue
			}
			operator := p.getOperator(fd, unsafe.Pointer(&events[i].Udata))
			if operator == nil || !operator.do() {
				continue
			}

			var totalRead int
			evt := events[i]
			triggerRead = evt.Filter == unix.EVFILT_READ && evt.Flags&unix.EV_ENABLE != 0
			triggerWrite = evt.Filter == unix.EVFILT_WRITE && evt.Flags&unix.EV_ENABLE != 0
			triggerHup = evt.Flags&unix.EV_EOF != 0

			if triggerRead {
				if operator.OnRead != nil {
					// for non-connection
					operator.OnRead(p)
				} else {
					// only for connection
					bs := operator.Inputs(barriers[i].Bs)
					if len(bs) > 0 {
						n, err := socket.IORead(operator.FD, bs, barriers[i].Ivs)
						operator.InputAck(n)
						totalRead += n
						if err != nil {
							p.appendHup(operator)
							continue
						}
					}
				}
			}
			if triggerHup {
				if triggerRead && operator.Inputs != nil {
					var leftRead int
					// read all left data if peer send and close
					if leftRead, err = readall(operator, barriers[i]); err != nil && !errors.Is(err, errs.ErrEOF) {
						logging.Printf("[anet]: readall(fd=%d)=%d before close: %s", operator.FD, leftRead, err.Error())
					}
					totalRead += leftRead
				}
				// only close connection if no further read bytes
				if totalRead == 0 {
					p.appendHup(operator)
					continue
				}
			}
			if triggerWrite {
				if operator.OnWrite != nil {
					// for non-connection
					operator.OnWrite(p)
				} else {
					// only for connection
					bs, supportZeroCopy := operator.Outputs(barriers[i].Bs)
					if len(bs) > 0 {
						// TODO: Let the upper layer pass in whether to use ZeroCopy.
						n, err := socket.IOSend(operator.FD, bs, barriers[i].Ivs, false && supportZeroCopy)
						operator.OutputAck(n)
						if err != nil {
							p.appendHup(operator)
							continue
						}
					}
				}
			}
			operator.done()
		}
		// hup conns together to avoid blocking the poll.
		p.onhups()
		p.opcache.free()
	}
}

// TODO: Close will bad file descriptor here
func (p *defaultPoll) Close() error {
	err := unix.Close(p.fd)
	p.closeWaker()
	return err
}

// Trigger implements Poll.
func (p *defaultPoll) Trigger() error {
	if atomic.AddUint32(&p.trigger, 1) > 1 {
		return nil
	}
	return p.wake()
}

// Control implements Poll.
func (p *defaultPoll) Control(operator *FDOperator, event PollEvent) error {
	evs := make([]unix.Kevent_t, 1)
	evs[0].Ident = uint64(operator.FD)
	p.setOperator(unsafe.Pointer(&evs[0].Udata), operator)
	switch event {
	case PollReadable:
		operator.inuse()
		evs[0].Filter, evs[0].Flags = unix.EVFILT_READ, unix.EV_ADD|unix.EV_ENABLE
	case PollWritable:
		operator.inuse()
		evs[0].Filter, evs[0].Flags = unix.EVFILT_WRITE, unix.EV_ADD|unix.EV_ENABLE
	case PollDetach:
		if operator.OnWrite != nil { // means WaitWrite finished
			evs[0].Filter, evs[0].Flags = unix.EVFILT_WRITE, unix.EV_DELETE
		} else {
			evs[0].Filter, evs[0].Flags = unix.EVFILT_READ, unix.EV_DELETE
		}
		p.delOperator(operator)
	case PollR2RW:
		evs[0].Filter, evs[0].Flags = unix.EVFILT_WRITE, unix.EV_ADD|unix.EV_ENABLE
	case PollRW2R:
		evs[0].Filter, evs[0].Flags = unix.EVFILT_WRITE, unix.EV_DELETE
	}
	_, err := unix.Kevent(p.fd, evs, nil, nil)
	return err
}
