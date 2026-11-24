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

package anet

import "github.com/alolainc/anet/internal/errs"

// Errors of anet. They extend unix.Errno in the range 0x100-0x1FF, so errors.Is works with the
// values below and with the errors returned by Exception.
const (
	// ErrConnClosed means the connection closed when in use.
	ErrConnClosed = errs.ErrConnClosed
	// ErrReadTimeout is a read I/O buffer timeout, returned by Connection.Reader.
	ErrReadTimeout = errs.ErrReadTimeout
	// ErrDialTimeout is a dial timeout.
	ErrDialTimeout = errs.ErrDialTimeout
	// ErrDialNoDeadline is returned when calling the dialer without a timeout.
	ErrDialNoDeadline = errs.ErrDialNoDeadline
	// ErrUnsupported means the calling function is not supported.
	ErrUnsupported = errs.ErrUnsupported
	// ErrEOF is the same as io.EOF.
	ErrEOF = errs.ErrEOF
	// ErrWriteTimeout is a write I/O buffer timeout, returned by Connection.Writer.
	ErrWriteTimeout = errs.ErrWriteTimeout
	// ErrConcurrentAccess is a concurrent connection access error.
	ErrConcurrentAccess = errs.ErrConcurrentAccess
	// ErrNotEnoughData is returned instead of blocking by connections that run OnRequest inline on the poller
	// (see WithInlineRequest), where waiting for data would deadlock the poller.
	ErrNotEnoughData = errs.ErrNotEnoughData
)

// ErrnoMask masks the anet part of an error number.
const ErrnoMask = errs.ErrnoMask

// Exception wraps an Errno with a suffix. The result implements net.Error and works with errors.Is.
func Exception(err error, suffix string) error { return errs.Exception(err, suffix) }
