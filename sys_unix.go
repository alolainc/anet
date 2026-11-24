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

package anet

import "github.com/alolainc/anet/internal/socket"

// GetSysFdPairs creates and returns the fds of a pair of connected unix sockets.
func GetSysFdPairs() (r, w int) { return socket.GetSysFdPairs() }

// SetKeepAlive sets TCP keepalive on fd with the given period in seconds.
func SetKeepAlive(fd, secs int) error { return socket.SetKeepAlive(fd, secs) }
