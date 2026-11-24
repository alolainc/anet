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

package socket

import "golang.org/x/sys/unix"

// ReusePort lets several listening sockets bind the same address; Linux spreads new connections over them.
func ReusePort(fd int) error { return unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEPORT, 1) }

// ReusePortBalances reports whether the kernel spreads new connections over sockets bound with ReusePort.
const ReusePortBalances = true
