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

//go:build darwin || netbsd || openbsd

package socket

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// Accept accepts a connection on the listening socket fd. There is no accept4 here, so close-on-exec is set
// while holding syscall.ForkLock (see SysSocket), then the descriptor is made non-blocking.
func Accept(fd int) (int, unix.Sockaddr, error) {
	syscall.ForkLock.RLock()
	nfd, sa, err := unix.Accept(fd)
	if err == nil {
		unix.CloseOnExec(nfd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return -1, nil, err
	}
	if err = unix.SetNonblock(nfd, true); err != nil {
		unix.Close(nfd)
		return -1, nil, err
	}
	return nfd, sa, nil
}
