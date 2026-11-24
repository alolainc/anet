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

//go:build !windows

package socket

import (
	"math"
	"unsafe"

	"golang.org/x/sys/unix"
)

// GetSysFdPairs creates and returns the fds of a pair of sockets.
func GetSysFdPairs() (r, w int) {
	fds, _ := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	return fds[0], fds[1]
}

// setTCPNoDelay set the TCP_NODELAY flag on socket
func SetTCPNoDelay(fd int, b bool) (err error) {
	return unix.SetsockoptInt(fd, unix.IPPROTO_TCP, unix.TCP_NODELAY, boolint(b))
}

const BarrierCap = 32

// Barrier holds the buffers and iovecs of one readv or sendmsg call.
type Barrier struct {
	Bs  [][]byte
	Ivs []unix.Iovec
}

// NewBarrier returns a Barrier for up to BarrierCap buffers.
func NewBarrier() *Barrier {
	return &Barrier{Bs: make([][]byte, BarrierCap), Ivs: make([]unix.Iovec, BarrierCap)}
}

// writev wraps the writev system call.
func Writev(fd int, bs [][]byte, ivs []unix.Iovec) (n int, err error) {
	iovLen := Iovecs(bs, ivs)
	if iovLen == 0 {
		return 0, nil
	}
	// syscall
	r, _, e := unix.RawSyscall(unix.SYS_WRITEV, uintptr(fd), uintptr(unsafe.Pointer(&ivs[0])), uintptr(iovLen))
	ResetIovecs(bs, ivs[:iovLen])
	if e != 0 {
		return int(r), e
	}
	return int(r), nil
}

// readv wraps the readv system call.
// return 0, nil means EOF.
func Readv(fd int, bs [][]byte, ivs []unix.Iovec) (n int, err error) {
	iovLen := Iovecs(bs, ivs)
	if iovLen == 0 {
		return 0, nil
	}
	// syscall
	r, _, e := unix.RawSyscall(unix.SYS_READV, uintptr(fd), uintptr(unsafe.Pointer(&ivs[0])), uintptr(iovLen))
	ResetIovecs(bs, ivs[:iovLen])
	if e != 0 {
		return int(r), e
	}
	return int(r), nil
}

// TODO: read from sysconf(_SC_IOV_MAX)? The Linux default is
//
//	1024 and this seems conservative enough for now. Darwin's
//	UIO_MAXIOV also seems to be 1024.
//
// iovecs limit length to 2GB(2^31)
func Iovecs(bs [][]byte, ivs []unix.Iovec) (iovLen int) {
	totalLen := 0
	for i := 0; i < len(bs); i++ {
		chunk := bs[i]
		l := len(chunk)
		if l == 0 {
			continue
		}
		ivs[iovLen].Base = &chunk[0]
		totalLen += l
		if totalLen < math.MaxInt32 {
			ivs[iovLen].SetLen(l)
			iovLen++
		} else {
			newLen := math.MaxInt32 - totalLen + l
			ivs[iovLen].SetLen(newLen)
			iovLen++
			return iovLen
		}
	}

	return iovLen
}

func ResetIovecs(bs [][]byte, ivs []unix.Iovec) {
	for i := 0; i < len(bs); i++ {
		bs[i] = nil
	}
	for i := 0; i < len(ivs); i++ {
		ivs[i].Base = nil
	}
}

// Boolean to int.
func boolint(b bool) int {
	if b {
		return 1
	}
	return 0
}
