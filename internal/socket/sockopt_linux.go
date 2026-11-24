// Copyright 2024 Alola Inc.
// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// This file may have been modified by CloudWeGo authors. (“CloudWeGo Modifications”).
// All CloudWeGo Modifications are Copyright 2022 CloudWeGo authors.

package socket

import (
	"os"

	"golang.org/x/sys/unix"
)

func SetDefaultSockopts(s, family, sotype int, ipv6only bool) error {
	if family == unix.AF_INET6 && sotype != unix.SOCK_RAW {
		// Allow both IP versions even if the OS default
		// is otherwise. Note that some operating systems
		// never admit this option.
		unix.SetsockoptInt(s, unix.IPPROTO_IPV6, unix.IPV6_V6ONLY, boolint(ipv6only))
	}

	// Allow broadcast.
	return os.NewSyscallError("setsockopt", unix.SetsockoptInt(s, unix.SOL_SOCKET, unix.SO_BROADCAST, 1))
}
