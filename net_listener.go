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

//go:build darwin || netbsd || freebsd || openbsd || dragonfly || linux

package anet

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/alolainc/anet/internal/socket"
)

// createReactorListener creates a Listener served by the pollers.
func createReactorListener(network, addr string) (l Listener, err error) {
	if network == "udp" || network == "udp4" || network == "udp6" {
		return nil, Exception(ErrUnsupported, "UDP")
	}
	// tcp, tcp4, tcp6, unix
	ln, err := net.Listen(network, addr)
	if err != nil {
		return nil, err
	}
	return convertReactorListener(ln)
}

// createReactorReusePortListener creates a TCP Listener with SO_REUSEPORT, so that more listeners can bind
// the same address (see WithReusePort). Where the kernel does not balance SO_REUSEPORT sockets it creates a
// plain listener, which the pollers then share.
func createReactorReusePortListener(network, addr string) (Listener, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
	default:
		return nil, Exception(ErrUnsupported, "SO_REUSEPORT on "+network)
	}
	if !socket.ReusePortBalances {
		return createReactorListener(network, addr)
	}
	lc := net.ListenConfig{Control: func(_, _ string, rc syscall.RawConn) error {
		var serr error
		if err := rc.Control(func(fd uintptr) { serr = socket.ReusePort(int(fd)) }); err != nil {
			return err
		}
		return serr
	}}
	ln, err := lc.Listen(context.Background(), network, addr)
	if err != nil {
		return nil, err
	}
	return convertReactorListener(ln)
}

// convertReactorListener converts a net.Listener to a Listener served by the pollers.
func convertReactorListener(l net.Listener) (nl Listener, err error) {
	if tmp, ok := l.(Listener); ok {
		return tmp, nil
	}
	ln := &listener{}
	ln.ln = l
	ln.addr = l.Addr()
	err = ln.parseFD()
	if err != nil {
		return nil, err
	}
	return ln, unix.SetNonblock(ln.fd, true)
}

var _ net.Listener = &listener{}

type listener struct {
	fd   int
	addr net.Addr     // listener's local addr
	ln   net.Listener // tcp|unix listener
	file *os.File
}

// Accept implements Listener.
func (ln *listener) Accept() (net.Conn, error) {
	fd, sa, err := socket.Accept(ln.fd)
	if err != nil {
		/* https://man7.org/linux/man-pages/man2/accept.2.html
		EAGAIN or EWOULDBLOCK
		  The socket is marked nonblocking and no connections are
		  present to be accepted.  POSIX.1-2001 and POSIX.1-2008
		  allow either error to be returned for this case, and do
		  not require these constants to have the same value, so a
		  portable application should check for both possibilities.
		*/
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			return nil, nil
		}
		return nil, err
	}
	nfd := &netFD{}
	nfd.fd = fd
	nfd.localAddr = ln.addr
	nfd.network = ln.addr.Network()
	nfd.remoteAddr = sockaddrToAddr(sa)
	return nfd, nil
}

// Close implements Listener.
func (ln *listener) Close() error {
	if ln.fd != 0 {
		unix.Close(ln.fd)
	}
	if ln.file != nil {
		ln.file.Close()
	}
	if ln.ln != nil {
		ln.ln.Close()
	}
	return nil
}

// Addr implements Listener.
func (ln *listener) Addr() net.Addr {
	return ln.addr
}

// Fd implements Listener.
func (ln *listener) Fd() (fd int) {
	return ln.fd
}

func (ln *listener) parseFD() (err error) {
	switch netln := ln.ln.(type) {
	case *net.TCPListener:
		ln.file, err = netln.File()
	case *net.UnixListener:
		ln.file, err = netln.File()
	default:
		return errors.New("listener type can't support")
	}
	if err != nil {
		return err
	}
	ln.fd = int(ln.file.Fd())
	return nil
}
