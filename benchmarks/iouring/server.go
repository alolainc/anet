//go:build iouring

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

package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/pawelgaczynski/giouring"
	"golang.org/x/sys/unix"

	"github.com/alolainc/anet/benchmarks/runner"
)

const (
	opAccept uint64 = 1 << 56
	opRecv   uint64 = 2 << 56
	opSend   uint64 = 3 << 56
	opMask   uint64 = 0xff << 56

	ringEntries = 4096
	initialBuf  = 16 << 10
)

type uringServer struct{}

// Run starts one io_uring event loop per CPU. Each loop owns an SO_REUSEPORT listener,
// so the kernel spreads incoming connections across loops without any cross-thread handoff.
func (s *uringServer) Run(network, address string) error {
	addr, err := net.ResolveTCPAddr(network, address)
	if err != nil {
		return err
	}
	n := runtime.NumCPU()
	errc := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() { errc <- runLoop(addr) }()
	}
	return <-errc
}

type conn struct {
	fd   int
	rbuf []byte
	rlen int
	wbuf []byte
	sent int
}

type loop struct {
	ring  *giouring.Ring
	lfd   int
	conns []*conn // indexed by fd
}

func listenReusePort(addr *net.TCPAddr) (int, error) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, opt := range []int{syscall.SO_REUSEADDR, unix.SO_REUSEPORT} {
		if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, opt, 1); err != nil {
			syscall.Close(fd)
			return -1, err
		}
	}
	sa := &syscall.SockaddrInet4{Port: addr.Port}
	copy(sa.Addr[:], addr.IP.To4())
	if err := syscall.Bind(fd, sa); err != nil {
		syscall.Close(fd)
		return -1, err
	}
	if err := syscall.Listen(fd, 4096); err != nil {
		syscall.Close(fd)
		return -1, err
	}
	return fd, nil
}

func runLoop(addr *net.TCPAddr) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	lfd, err := listenReusePort(addr)
	if err != nil {
		return err
	}
	ring, err := giouring.CreateRing(ringEntries)
	if err != nil {
		return fmt.Errorf("io_uring setup: %w", err)
	}
	defer ring.QueueExit()

	l := &loop{ring: ring, lfd: lfd}
	l.armAccept()
	for {
		if _, err := ring.SubmitAndWait(1); err != nil && err != syscall.EINTR {
			return err
		}
		for {
			cqe, err := ring.PeekCQE()
			if err != nil {
				break // nothing left in the completion queue
			}
			ud, res := cqe.UserData, cqe.Res
			ring.CQESeen(cqe)
			l.handle(ud, int(res))
		}
	}
}

func (l *loop) sqe() *giouring.SubmissionQueueEntry {
	for {
		if sqe := l.ring.GetSQE(); sqe != nil {
			return sqe
		}
		l.ring.Submit() // submission queue is full: flush it and retry
	}
}

func (l *loop) armAccept() {
	sqe := l.sqe()
	sqe.PrepareAccept(l.lfd, 0, 0, 0)
	sqe.SetData64(opAccept)
}

func (l *loop) armRecv(c *conn) {
	if cap(c.rbuf)-c.rlen == 0 {
		nb := make([]byte, len(c.rbuf)*2)
		copy(nb, c.rbuf[:c.rlen])
		c.rbuf = nb
	}
	sqe := l.sqe()
	sqe.PrepareRecv(c.fd, uintptr(unsafe.Pointer(&c.rbuf[c.rlen])), uint32(len(c.rbuf)-c.rlen), 0)
	sqe.SetData64(opRecv | uint64(c.fd))
}

func (l *loop) armSend(c *conn) {
	sqe := l.sqe()
	sqe.PrepareSend(c.fd, uintptr(unsafe.Pointer(&c.wbuf[c.sent])), uint32(len(c.wbuf)-c.sent), 0)
	sqe.SetData64(opSend | uint64(c.fd))
}

func (l *loop) closeConn(c *conn) {
	syscall.Close(c.fd)
	l.conns[c.fd] = nil
}

func (l *loop) handle(ud uint64, res int) {
	switch ud & opMask {
	case opAccept:
		l.armAccept()
		if res < 0 {
			return
		}
		fd := res
		syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 1)
		for fd >= len(l.conns) {
			l.conns = append(l.conns, make([]*conn, len(l.conns)+1024)...)
		}
		c := &conn{fd: fd, rbuf: make([]byte, initialBuf)}
		l.conns[fd] = c
		l.armRecv(c)

	case opRecv:
		c := l.conns[int(ud&^opMask)]
		if c == nil {
			return
		}
		if res <= 0 {
			l.closeConn(c)
			return
		}
		c.rlen += res
		if !l.process(c) {
			l.closeConn(c)
			return
		}
		if len(c.wbuf) > 0 {
			l.armSend(c)
		} else {
			l.armRecv(c)
		}

	case opSend:
		c := l.conns[int(ud&^opMask)]
		if c == nil {
			return
		}
		if res < 0 {
			l.closeConn(c)
			return
		}
		c.sent += res
		if c.sent < len(c.wbuf) {
			l.armSend(c)
			return
		}
		c.wbuf, c.sent = c.wbuf[:0], 0
		l.armRecv(c)
	}
}

// process decodes every complete frame in c.rbuf, runs the handler and appends responses to c.wbuf.
func (l *loop) process(c *conn) bool {
	off := 0
	for c.rlen-off >= 4 {
		size := int(binary.BigEndian.Uint32(c.rbuf[off:]))
		if size < 0 || size > 64<<20 {
			return false
		}
		if c.rlen-off < 4+size {
			// make sure the buffer can hold the whole frame
			if need := 4 + size; need > len(c.rbuf) {
				nb := make([]byte, need)
				copy(nb, c.rbuf[off:c.rlen])
				c.rbuf, c.rlen, off = nb, c.rlen-off, 0
			}
			break
		}
		req := &runner.Message{Message: string(c.rbuf[off+4 : off+4+size])}
		resp := runner.ProcessRequest(reporter, req)
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(resp.Message)))
		c.wbuf = append(c.wbuf, hdr[:]...)
		c.wbuf = append(c.wbuf, resp.Message...)
		off += 4 + size
	}
	if off > 0 {
		c.rlen = copy(c.rbuf, c.rbuf[off:c.rlen])
	}
	return true
}
