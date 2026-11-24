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

//go:build openbsd || netbsd

package netpoll

import "golang.org/x/sys/unix"

// OpenBSD and NetBSD have no EVFILT_USER, so the poller wakes itself up with a self-pipe: Trigger writes a byte to the pipe,
// whose read end is registered with the kqueue. The Go runtime uses the same technique.

func (p *defaultPoll) openWaker() error {
	var fds [2]int
	if err := unix.Pipe2(fds[:], unix.O_NONBLOCK|unix.O_CLOEXEC); err != nil {
		return err
	}
	p.wakeFd, p.wakeW = fds[0], fds[1]
	_, err := unix.Kevent(p.fd, []unix.Kevent_t{{
		Ident:  uint64(p.wakeFd),
		Filter: unix.EVFILT_READ,
		Flags:  unix.EV_ADD | unix.EV_ENABLE,
	}}, nil, nil)
	if err != nil {
		p.closeWaker()
	}
	return err
}

func (p *defaultPoll) wake() error {
	_, err := unix.Write(p.wakeW, []byte{1})
	if err == unix.EAGAIN { // the pipe is full, so a wake-up is already pending
		return nil
	}
	return err
}

func (p *defaultPoll) drainWaker() {
	var buf [64]byte
	for {
		if n, err := unix.Read(p.wakeFd, buf[:]); n <= 0 || err != nil {
			return
		}
	}
}

func (p *defaultPoll) closeWaker() {
	unix.Close(p.wakeFd)
	unix.Close(p.wakeW)
}
