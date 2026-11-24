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

//go:build darwin

package socket

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// SysSocket creates a non-blocking socket that is closed on exec. macOS has no SOCK_CLOEXEC, so the flag is
// set after creation while holding syscall.ForkLock, which os/exec takes to fork: no child can inherit the
// descriptor in between. Package net does the same.
func SysSocket(family, sotype, proto int) (int, error) {
	syscall.ForkLock.RLock()
	s, err := unix.Socket(family, sotype, proto)
	if err == nil {
		unix.CloseOnExec(s)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return -1, os.NewSyscallError("socket", err)
	}
	if err = unix.SetNonblock(s, true); err != nil {
		unix.Close(s)
		return -1, os.NewSyscallError("setnonblock", err)
	}
	return s, nil
}
