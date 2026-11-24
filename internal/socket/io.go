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

package socket

import (
	"golang.org/x/sys/unix"

	"github.com/alolainc/anet/internal/errs"
)

// return value:
// - n: n == 0 but err == nil, retry syscall
// - err: if not nil, connection should be closed.
func IORead(fd int, bs [][]byte, ivs []unix.Iovec) (n int, err error) {
	n, err = Readv(fd, bs, ivs)
	if n == 0 && err == nil { // means EOF
		return 0, errs.Exception(errs.ErrEOF, "")
	}
	if err == unix.EINTR || err == unix.EAGAIN {
		return 0, nil
	}
	return n, err
}

// return value:
// - n: n == 0 but err == nil, retry syscall
// - err: if not nil, connection should be closed.
func IOSend(fd int, bs [][]byte, ivs []unix.Iovec, zerocopy bool) (n int, err error) {
	n, err = Sendmsg(fd, bs, ivs, zerocopy)
	if err == unix.EAGAIN {
		return 0, nil
	}
	return n, err
}
