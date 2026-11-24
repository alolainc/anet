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

import (
	"testing"

	"golang.org/x/sys/unix"

	"github.com/alolainc/anet/internal/socket"
)

func checkNotSentLowat(t *testing.T, fd, want int) {
	t.Helper()
	lowat, err := unix.GetsockoptInt(fd, unix.IPPROTO_TCP, socket.NotSentLowat)
	MustNil(t, err)
	Equal(t, lowat, want)
}
