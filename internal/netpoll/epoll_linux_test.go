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

//go:build linux

package netpoll

import (
	"net"

	"github.com/alolainc/anet/internal/errs"
	. "github.com/alolainc/anet/internal/testutil"

	"errors"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/alolainc/anet/internal/socket"
)

func newEpollEvent(events uint32, data uint64) *EpollEvent {
	ev := &EpollEvent{}
	ev.Events = events
	*(*uint64)(ev.GetDataPtr()) = data
	return ev
}

func getEventData(ev *EpollEvent) uint64 {
	return *(*uint64)(ev.GetDataPtr())
}

func TestEpollEvent(t *testing.T) {
	epollfd, err := EpollCreate(0)
	MustNil(t, err)
	defer unix.Close(epollfd)

	rfd, wfd := socket.GetSysFdPairs()
	defer unix.Close(rfd)
	defer unix.Close(wfd)

	send := []byte("hello")
	recv := make([]byte, 5)
	events := make([]EpollEvent, 128)
	var eventdata1 uint64 = 1
	var eventdata2 uint64 = 2
	var eventdata3 uint64 = 3
	event1 := newEpollEvent(unix.EPOLLIN, eventdata1)
	event2 := newEpollEvent(unix.EPOLLIN, eventdata2)
	event3 := newEpollEvent(unix.EPOLLIN|unix.EPOLLOUT, eventdata3)

	// EPOLL: add ,del and add
	err = EpollCtl(epollfd, unix.EPOLL_CTL_ADD, rfd, event1)
	MustNil(t, err)
	err = EpollCtl(epollfd, unix.EPOLL_CTL_DEL, rfd, event1)
	MustNil(t, err)
	err = EpollCtl(epollfd, unix.EPOLL_CTL_ADD, rfd, event2)
	MustNil(t, err)
	_, err = unix.Write(wfd, send)
	MustNil(t, err)
	n, err := epollWaitUntil(epollfd, events, -1)
	MustNil(t, err)
	Equal(t, n, 1)
	Equal(t, getEventData(&events[0]), eventdata2)
	_, err = unix.Read(rfd, recv)
	MustTrue(t, err == nil && string(recv) == string(send))
	err = EpollCtl(epollfd, unix.EPOLL_CTL_DEL, rfd, event2)
	MustNil(t, err)

	// EPOLL: add ,mod and mod
	err = EpollCtl(epollfd, unix.EPOLL_CTL_ADD, rfd, event1)
	MustNil(t, err)
	err = EpollCtl(epollfd, unix.EPOLL_CTL_MOD, rfd, event2)
	MustNil(t, err)
	err = EpollCtl(epollfd, unix.EPOLL_CTL_MOD, rfd, event3)
	MustNil(t, err)
	_, err = unix.Write(wfd, send)
	MustNil(t, err)
	n, err = epollWaitUntil(epollfd, events, -1)
	MustNil(t, err)
	Equal(t, n, 1)
	Equal(t, getEventData(&events[0]), eventdata3)
	_, err = unix.Read(rfd, recv)
	MustTrue(t, err == nil && string(recv) == string(send))
	Assert(t, events[0].Events&unix.EPOLLIN != 0)
	Assert(t, events[0].Events&unix.EPOLLOUT != 0)

	err = EpollCtl(epollfd, unix.EPOLL_CTL_DEL, rfd, event2)
	MustNil(t, err)
}

func TestEpollWait(t *testing.T) {
	epollfd, err := EpollCreate(0)
	MustNil(t, err)
	defer unix.Close(epollfd)

	rfd, wfd := socket.GetSysFdPairs()
	defer unix.Close(wfd)

	send := []byte("hello")
	recv := make([]byte, 5)
	events := make([]EpollEvent, 128)

	// EPOLL: init state
	event := newEpollEvent(unix.EPOLLIN|unix.EPOLLOUT|unix.EPOLLRDHUP|unix.EPOLLERR, 1)
	err = EpollCtl(epollfd, unix.EPOLL_CTL_ADD, rfd, event)
	MustNil(t, err)
	_, err = epollWaitUntil(epollfd, events, -1)
	MustNil(t, err)
	Assert(t, events[0].Events&unix.EPOLLIN == 0)
	Assert(t, events[0].Events&unix.EPOLLOUT != 0)

	// EPOLL: readable
	_, err = unix.Write(wfd, send)
	MustNil(t, err)
	_, err = epollWaitUntil(epollfd, events, -1)
	MustNil(t, err)
	Assert(t, events[0].Events&unix.EPOLLIN != 0)
	Assert(t, events[0].Events&unix.EPOLLOUT != 0)
	_, err = unix.Read(rfd, recv)
	MustTrue(t, err == nil && string(recv) == string(send))

	// EPOLL: read finished
	_, err = epollWaitUntil(epollfd, events, -1)
	MustNil(t, err)
	Assert(t, events[0].Events&unix.EPOLLIN == 0)
	Assert(t, events[0].Events&unix.EPOLLOUT != 0)

	// EPOLL: close peer fd
	err = unix.Close(wfd)
	MustNil(t, err)
	_, err = epollWaitUntil(epollfd, events, -1)
	MustNil(t, err)
	Assert(t, events[0].Events&unix.EPOLLIN != 0)
	Assert(t, events[0].Events&unix.EPOLLOUT != 0)
	Assert(t, events[0].Events&unix.EPOLLRDHUP != 0)
	Assert(t, events[0].Events&unix.EPOLLERR == 0)

	// EPOLL: close current fd
	rfd2, wfd2 := socket.GetSysFdPairs()
	defer unix.Close(wfd2)
	err = EpollCtl(epollfd, unix.EPOLL_CTL_ADD, rfd2, event)
	MustNil(t, err)
	err = unix.Close(rfd2)
	MustNil(t, err)
	_, err = epollWaitUntil(epollfd, events, -1)
	MustNil(t, err)
	Assert(t, events[0].Events&unix.EPOLLIN != 0)
	Assert(t, events[0].Events&unix.EPOLLOUT != 0)
	Assert(t, events[0].Events&unix.EPOLLRDHUP != 0)
	Assert(t, events[0].Events&unix.EPOLLERR == 0)

	err = EpollCtl(epollfd, unix.EPOLL_CTL_DEL, rfd, event)
	MustNil(t, err)
}

func TestEpollETClose(t *testing.T) {
	epollfd, err := EpollCreate(0)
	MustNil(t, err)
	defer unix.Close(epollfd)
	rfd, wfd := socket.GetSysFdPairs()
	events := make([]EpollEvent, 128)
	event := newEpollEvent(EPOLLET|unix.EPOLLIN|unix.EPOLLOUT|unix.EPOLLRDHUP|unix.EPOLLERR, 1)

	// EPOLL: init state
	err = EpollCtl(epollfd, unix.EPOLL_CTL_ADD, rfd, event)
	MustNil(t, err)
	_, err = epollWaitUntil(epollfd, events, -1)
	MustNil(t, err)
	Assert(t, events[0].Events&unix.EPOLLIN == 0)
	Assert(t, events[0].Events&unix.EPOLLOUT != 0)
	Assert(t, events[0].Events&unix.EPOLLRDHUP == 0)
	Assert(t, events[0].Events&unix.EPOLLERR == 0)

	// EPOLL: close current fd
	// nothing will happen
	err = unix.Close(rfd)
	MustNil(t, err)
	n, err := epollWaitUntil(epollfd, events, 100)
	MustNil(t, err)
	Assert(t, n == 0, n)
	err = unix.Close(wfd)
	MustNil(t, err)

	// EPOLL: close peer fd
	// EPOLLIN and EPOLLOUT
	rfd, wfd = socket.GetSysFdPairs()
	err = EpollCtl(epollfd, unix.EPOLL_CTL_ADD, rfd, event)
	MustNil(t, err)
	err = unix.Close(wfd)
	MustNil(t, err)
	n, err = epollWaitUntil(epollfd, events, 100)
	MustNil(t, err)
	Assert(t, n == 1, n)
	Assert(t, events[0].Events&unix.EPOLLIN != 0)
	Assert(t, events[0].Events&unix.EPOLLOUT != 0)
	Assert(t, events[0].Events&unix.EPOLLRDHUP != 0)
	Assert(t, events[0].Events&unix.EPOLLERR == 0)
	buf := make([]byte, 1024)
	ivs := make([]unix.Iovec, 1)
	n, err = socket.IORead(rfd, [][]byte{buf}, ivs) // EOF
	Assert(t, n == 0 && errors.Is(err, errs.ErrEOF), n, err)
}

func TestEpollETDel(t *testing.T) {
	epollfd, err := EpollCreate(0)
	MustNil(t, err)
	defer unix.Close(epollfd)
	rfd, wfd := socket.GetSysFdPairs()
	send := []byte("hello")
	events := make([]EpollEvent, 128)
	event := newEpollEvent(EPOLLET|unix.EPOLLIN|unix.EPOLLRDHUP|unix.EPOLLERR, 1)

	// EPOLL: del partly
	err = EpollCtl(epollfd, unix.EPOLL_CTL_ADD, rfd, event)
	MustNil(t, err)
	event.Events = unix.EPOLLIN | unix.EPOLLOUT | unix.EPOLLRDHUP | unix.EPOLLERR
	err = EpollCtl(epollfd, unix.EPOLL_CTL_DEL, rfd, event)
	MustNil(t, err)
	_, err = unix.Write(wfd, send)
	MustNil(t, err)
	_, err = epollWaitUntil(epollfd, events, 100)
	MustNil(t, err)
	Assert(t, events[0].Events&unix.EPOLLIN == 0)
	Assert(t, events[0].Events&unix.EPOLLRDHUP == 0)
	Assert(t, events[0].Events&unix.EPOLLERR == 0)
}

func TestEpollConnectSameFD(t *testing.T) {
	// a listening socket is enough: the kernel completes the handshake before accept
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	MustNil(t, err)
	defer ln.Close()
	addr := unix.SockaddrInet4{
		Port: ln.Addr().(*net.TCPAddr).Port,
		Addr: [4]byte{127, 0, 0, 1},
	}

	epollfd, err := EpollCreate(0)
	MustNil(t, err)
	defer unix.Close(epollfd)
	events := make([]EpollEvent, 128)
	var eventdata2 uint64 = 2
	event1 := newEpollEvent(EPOLLET|unix.EPOLLOUT|unix.EPOLLRDHUP|unix.EPOLLERR, 1)
	event2 := newEpollEvent(EPOLLET|unix.EPOLLOUT|unix.EPOLLRDHUP|unix.EPOLLERR, eventdata2)
	eventin := newEpollEvent(unix.EPOLLIN|unix.EPOLLRDHUP|unix.EPOLLERR, 1)

	// connect non-block socket
	fd1, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, unix.IPPROTO_TCP)
	MustNil(t, err)
	t.Logf("create fd: %d", fd1)
	err = unix.SetNonblock(fd1, true)
	MustNil(t, err)
	err = EpollCtl(epollfd, unix.EPOLL_CTL_ADD, fd1, event1)
	MustNil(t, err)
	err = unix.Connect(fd1, &addr)
	t.Log(err) // EINPROGRESS
	_, err = epollWaitUntil(epollfd, events, -1)
	MustNil(t, err)
	Assert(t, events[0].Events&unix.EPOLLOUT != 0)
	Assert(t, events[0].Events&unix.EPOLLRDHUP == 0)
	Assert(t, events[0].Events&unix.EPOLLERR == 0)
	// forget to del fd
	// err = EpollCtl(epollfd, unix.EPOLL_CTL_DEL, fd1, event1)
	// MustNil(t, err)
	err = unix.Close(fd1) // close fd1
	MustNil(t, err)

	// connect non-block socket with same fd
	fd2, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, unix.IPPROTO_TCP)
	MustNil(t, err)
	t.Logf("create fd: %d", fd2)
	err = unix.SetNonblock(fd2, true)
	MustNil(t, err)
	err = EpollCtl(epollfd, unix.EPOLL_CTL_ADD, fd2, event2)
	MustNil(t, err)
	err = unix.Connect(fd2, &addr)
	t.Log(err) // EINPROGRESS
	_, err = epollWaitUntil(epollfd, events, -1)
	MustNil(t, err)
	Assert(t, events[0].Events&unix.EPOLLOUT != 0)
	Assert(t, events[0].Events&unix.EPOLLRDHUP == 0)
	Assert(t, events[0].Events&unix.EPOLLERR == 0)
	err = EpollCtl(epollfd, unix.EPOLL_CTL_DEL, fd2, event2)
	MustNil(t, err)
	err = unix.Close(fd2) // close fd2
	MustNil(t, err)
	Equal(t, getEventData(&events[0]), eventdata2)

	// no event after close fd
	fd3, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, unix.IPPROTO_TCP)
	MustNil(t, err)
	t.Logf("create fd: %d", fd3)
	err = unix.SetNonblock(fd3, true)
	MustNil(t, err)
	err = EpollCtl(epollfd, unix.EPOLL_CTL_ADD, fd3, event1)
	MustNil(t, err)
	err = unix.Connect(fd3, &addr)
	t.Log(err) // EINPROGRESS
	_, err = epollWaitUntil(epollfd, events, -1)
	MustNil(t, err)
	Assert(t, events[0].Events&unix.EPOLLOUT != 0)
	Assert(t, events[0].Events&unix.EPOLLRDHUP == 0)
	Assert(t, events[0].Events&unix.EPOLLERR == 0)
	MustNil(t, err)
	err = EpollCtl(epollfd, unix.EPOLL_CTL_MOD, fd3, eventin)
	MustNil(t, err)
	err = unix.Close(fd3) // close fd3
	MustNil(t, err)
	n, err := epollWaitUntil(epollfd, events, 100)
	MustNil(t, err)
	Assert(t, n == 0)
}

func epollWaitUntil(epfd int, events []EpollEvent, msec int) (n int, err error) {
WAIT:
	n, err = EpollWait(epfd, events, msec)
	if err == unix.EINTR {
		goto WAIT
	}
	return n, err
}
