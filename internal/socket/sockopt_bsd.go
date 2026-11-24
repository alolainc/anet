// Copyright 2024 Alola Inc.
// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// This file may have been modified by CloudWeGo authors. (“CloudWeGo Modifications”).
// All CloudWeGo Modifications are Copyright 2022 CloudWeGo authors.

//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package socket

import (
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

func SetDefaultSockopts(s, family, sotype int, ipv6only bool) error {
	if runtime.GOOS == "dragonfly" && sotype != unix.SOCK_RAW {
		// On DragonFly BSD, we adjust the ephemeral port
		// range because unlike other BSD systems its default
		// port range doesn't conform to IANA recommendation
		// as described in RFC 6056 and is pretty narrow.
		switch family {
		case unix.AF_INET:
			unix.SetsockoptInt(s, unix.IPPROTO_IP, unix.IP_PORTRANGE, unix.IP_PORTRANGE_HIGH)
		case unix.AF_INET6:
			unix.SetsockoptInt(s, unix.IPPROTO_IPV6, unix.IPV6_PORTRANGE, unix.IPV6_PORTRANGE_HIGH)
		}
	}
	// Allow broadcast.
	return os.NewSyscallError("setsockopt", unix.SetsockoptInt(s, unix.SOL_SOCKET, unix.SO_BROADCAST, 1))
}
