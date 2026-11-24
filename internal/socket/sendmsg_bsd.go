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

//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package socket

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// sendmsg wraps the sendmsg system call.
// Must len(iovs) >= len(vs)
func Sendmsg(fd int, bs [][]byte, ivs []unix.Iovec, zerocopy bool) (n int, err error) {
	iovLen := Iovecs(bs, ivs)
	if iovLen == 0 {
		return 0, nil
	}
	msghdr := unix.Msghdr{Iov: &ivs[0]}
	msghdr.SetIovlen(iovLen)
	// flags = unix.MSG_DONTWAIT
	r, _, e := unix.RawSyscall(unix.SYS_SENDMSG, uintptr(fd), uintptr(unsafe.Pointer(&msghdr)), uintptr(0))
	ResetIovecs(bs, ivs[:iovLen])
	if e != 0 {
		return int(r), e
	}
	return int(r), nil
}
