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

import (
	"net/netip"
	"unsafe"

	"golang.org/x/sys/unix"
)

// RecvFrom reads one datagram from the non-blocking socket fd into p and returns the sender's address.
// Unlike unix.Recvfrom it does not allocate: the address is decoded from a sockaddr on the stack.
// p must not be empty.
func RecvFrom(fd int, p []byte) (n int, from netip.AddrPort, err error) {
	var rsa unix.RawSockaddrAny
	salen := uint32(unix.SizeofSockaddrAny)
	r, _, e := unix.RawSyscall6(unix.SYS_RECVFROM, uintptr(fd), uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)),
		0, uintptr(unsafe.Pointer(&rsa)), uintptr(unsafe.Pointer(&salen)))
	if e != 0 {
		return 0, netip.AddrPort{}, e
	}
	return int(r), decodeAddr(&rsa), nil
}

// decodeAddr converts an AF_INET or AF_INET6 sockaddr; other families give the zero AddrPort.
func decodeAddr(rsa *unix.RawSockaddrAny) netip.AddrPort {
	switch int(rsa.Addr.Family) {
	case unix.AF_INET:
		sa := (*unix.RawSockaddrInet4)(unsafe.Pointer(rsa))
		return netip.AddrPortFrom(netip.AddrFrom4(sa.Addr), ntohs(sa.Port))
	case unix.AF_INET6:
		sa := (*unix.RawSockaddrInet6)(unsafe.Pointer(rsa))
		// a dual-stack socket reports IPv4 senders as IPv4-mapped IPv6; unmap them like package net does
		return netip.AddrPortFrom(netip.AddrFrom16(sa.Addr).Unmap(), ntohs(sa.Port))
	}
	return netip.AddrPort{}
}

// ntohs converts a port stored in network byte order.
func ntohs(port uint16) uint16 {
	b := (*[2]byte)(unsafe.Pointer(&port))
	return uint16(b[0])<<8 | uint16(b[1])
}
