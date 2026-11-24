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

//go:build linux

package anet

import "github.com/alolainc/anet/internal/netpoll"

// EPOLLET is the edge-triggered epoll flag.
const EPOLLET = netpoll.EPOLLET

type epollevent = netpoll.EpollEvent

// EpollCreate wraps epoll_create1.
func EpollCreate(flag int) (fd int, err error) { return netpoll.EpollCreate(flag) }

// EpollCtl wraps epoll_ctl.
func EpollCtl(epfd, op, fd int, event *epollevent) (err error) {
	return netpoll.EpollCtl(epfd, op, fd, event)
}

// EpollWait wraps epoll_wait.
func EpollWait(epfd int, events []epollevent, msec int) (n int, err error) {
	return netpoll.EpollWait(epfd, events, msec)
}
