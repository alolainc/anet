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

//go:build linux && arm64

package netpoll

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// EpollWait implements epoll_wait.
func EpollWait(epfd int, events []EpollEvent, msec int) (n int, err error) {
	var r0 uintptr
	_p0 := unsafe.Pointer(&events[0])
	if msec == 0 {
		// When timeout is 0 (non-blocking poll), use RawSyscall6 to avoid the overhead
		// of scheduler coordination (entersyscall/exitsyscall) since it won't block.
		r0, _, err = unix.RawSyscall6(unix.SYS_EPOLL_PWAIT, uintptr(epfd), uintptr(_p0), uintptr(len(events)), 0, 0, 0)
	} else {
		r0, _, err = unix.Syscall6(unix.SYS_EPOLL_PWAIT, uintptr(epfd), uintptr(_p0), uintptr(len(events)), uintptr(msec), 0, 0)
	}
	if err == unix.Errno(0) {
		err = nil
	}
	return int(r0), err
}
