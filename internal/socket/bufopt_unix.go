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

//go:build !windows

package socket

import "golang.org/x/sys/unix"

// setSocketBuffer sets SO_SNDBUF (send=true) or SO_RCVBUF on fd.
func SetBuffer(fd int, send bool, n int) error {
	opt := unix.SO_RCVBUF
	if send {
		opt = unix.SO_SNDBUF
	}
	return unix.SetsockoptInt(fd, unix.SOL_SOCKET, opt, n)
}
