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
	"net"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/alolainc/anet/benchmarks/runner"
)

const initialBuf = 16 << 10

type epollServer struct{}

// Run starts one epoll loop per CPU, each with its own SO_REUSEPORT listener. The structure mirrors
// ../iouring so that the two differ only in the I/O mechanism (readiness + read/write vs completion).
func (s *epollServer) Run(network, address string) error {
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
	fd      int
	rbuf    []byte
	rlen    int
	wbuf    []byte
	sent    int
	pollOut bool
}

func listenReusePort(addr *net.TCPAddr) (int, error) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC|syscall.SOCK_NONBLOCK, 0)
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
	epfd, err := unix.EpollCreate1(unix.EPOLL_CLOEXEC)
	if err != nil {
		return err
	}
	if err := unix.EpollCtl(epfd, unix.EPOLL_CTL_ADD, lfd, &unix.EpollEvent{Events: unix.EPOLLIN, Fd: int32(lfd)}); err != nil {
		return err
	}
	var conns []*conn
	events := make([]unix.EpollEvent, 1024)
	closeConn := func(c *conn) {
		syscall.Close(c.fd) // closing the fd removes it from the epoll set
		conns[c.fd] = nil
	}
	for {
		n, err := unix.EpollWait(epfd, events, -1)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return err
		}
		for i := 0; i < n; i++ {
			ev := events[i]
			fd := int(ev.Fd)
			if fd == lfd {
				for {
					nfd, _, err := unix.Accept4(lfd, unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC)
					if err != nil {
						break // EAGAIN: no more pending connections
					}
					syscall.SetsockoptInt(nfd, syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 1)
					for nfd >= len(conns) {
						conns = append(conns, make([]*conn, len(conns)+1024)...)
					}
					conns[nfd] = &conn{fd: nfd, rbuf: make([]byte, initialBuf)}
					unix.EpollCtl(epfd, unix.EPOLL_CTL_ADD, nfd, &unix.EpollEvent{Events: unix.EPOLLIN | unix.EPOLLRDHUP, Fd: int32(nfd)})
				}
				continue
			}
			c := conns[fd]
			if c == nil {
				continue
			}
			if ev.Events&(unix.EPOLLERR|unix.EPOLLHUP) != 0 {
				closeConn(c)
				continue
			}
			if ev.Events&unix.EPOLLIN != 0 {
				if cap(c.rbuf)-c.rlen == 0 {
					nb := make([]byte, len(c.rbuf)*2)
					copy(nb, c.rbuf[:c.rlen])
					c.rbuf = nb
				}
				nr, err := syscall.Read(c.fd, c.rbuf[c.rlen:])
				if nr == 0 || (err != nil && err != syscall.EAGAIN && err != syscall.EINTR) {
					closeConn(c)
					continue
				}
				if nr > 0 {
					c.rlen += nr
					if !process(c) {
						closeConn(c)
						continue
					}
				}
			}
			if len(c.wbuf) > c.sent {
				nw, err := syscall.Write(c.fd, c.wbuf[c.sent:])
				if err != nil && err != syscall.EAGAIN && err != syscall.EINTR {
					closeConn(c)
					continue
				}
				if nw > 0 {
					c.sent += nw
				}
				if c.sent >= len(c.wbuf) {
					c.wbuf, c.sent = c.wbuf[:0], 0
					if c.pollOut {
						c.pollOut = false
						unix.EpollCtl(epfd, unix.EPOLL_CTL_MOD, c.fd, &unix.EpollEvent{Events: unix.EPOLLIN | unix.EPOLLRDHUP, Fd: int32(c.fd)})
					}
				} else if !c.pollOut {
					c.pollOut = true
					unix.EpollCtl(epfd, unix.EPOLL_CTL_MOD, c.fd, &unix.EpollEvent{Events: unix.EPOLLIN | unix.EPOLLOUT | unix.EPOLLRDHUP, Fd: int32(c.fd)})
				}
			}
		}
	}
}

// process decodes every complete frame in c.rbuf, runs the handler and appends responses to c.wbuf.
func process(c *conn) bool {
	off := 0
	for c.rlen-off >= 4 {
		size := int(binary.BigEndian.Uint32(c.rbuf[off:]))
		if size < 0 || size > 64<<20 {
			return false
		}
		if c.rlen-off < 4+size {
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
