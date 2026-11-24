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

package anet

import (
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"github.com/alolainc/anet/internal/logging"
	"github.com/alolainc/anet/internal/netpoll"
	"github.com/alolainc/anet/internal/socket"
)

type connState = int32

const (
	connStateNone         = 0
	connStateConnected    = 1
	connStateDisconnected = 2
)

// connection is the implementation of Connection
type connection struct {
	netFD
	onEvent
	locker
	operator      *FDOperator
	readTimeout   time.Duration
	readDeadline  int64 // UnixNano(). it overwrites readTimeout. 0 if not set.
	readTimer     *time.Timer
	readTrigger   chan error
	waitReadSize  int64
	writeTimeout  time.Duration
	writeDeadline int64 // UnixNano(). it overwrites writeTimeout. 0 if not set.
	writeTimer    *time.Timer
	writeTrigger  chan error
	inputBuffer   *LinkBuffer
	outputBuffer  *LinkBuffer
	maxSize       int       // The maximum size of data between two Release().
	bookSize      int       // The size of data that can be read at once.
	state         connState // Connection state should be changed sequentially.
	inline        bool      // run OnRequest on the poller goroutine; see WithInlineRequest
	stalled       bool      // inline mode: the last OnRequest call consumed no input
	// outOwner says who may send the output buffer: outIdle (a writer may take it), outWriter (Flush is
	// sending) or outPoller (data is left and the poller sends it when the socket is writable). Only the owner
	// reads the buffer for sending, so a Flush from another goroutine never races with the poller.
	outOwner atomic.Int32
	onPoller bool // inline mode: the processing lock is held by a task running on the poller goroutine
}

var (
	_ Connection = &connection{}
	_ Reader     = &connection{}
	_ Writer     = &connection{}
)

// Reader implements Connection.
func (c *connection) Reader() Reader {
	return c
}

// Writer implements Connection.
func (c *connection) Writer() Writer {
	return c
}

// SetSendBuffer implements SocketTuner.
func (c *connection) SetSendBuffer(n int) error { return socket.SetBuffer(c.fd, true, n) }

// SetRecvBuffer implements SocketTuner.
func (c *connection) SetRecvBuffer(n int) error { return socket.SetBuffer(c.fd, false, n) }

// SetNotSentLowat implements SocketTuner.
func (c *connection) SetNotSentLowat(n int) error {
	switch c.network {
	case "tcp", "tcp4", "tcp6":
		return socket.SetNotSentLowat(c.fd, n)
	}
	return Exception(ErrUnsupported, "TCP_NOTSENT_LOWAT on "+c.network)
}

var _ SocketTuner = (*connection)(nil)

// PendingWrite returns the number of flushed bytes that have not been sent yet. It is not part of
// Connection, so look it up with a type assertion, e.g. to stop feeding a peer that does not read.
func (c *connection) PendingWrite() int {
	return c.outputBuffer.Len()
}

// IsActive implements Connection.
func (c *connection) IsActive() bool {
	return c.isCloseBy(none)
}

// SetIdleTimeout implements Connection.
func (c *connection) SetIdleTimeout(timeout time.Duration) error {
	if timeout > 0 {
		return c.SetKeepAlive(int(timeout.Seconds()))
	}
	return nil
}

// SetReadTimeout implements Connection.
func (c *connection) SetReadTimeout(timeout time.Duration) error {
	if timeout >= 0 {
		c.readTimeout = timeout
	}
	c.readDeadline = 0
	return nil
}

// SetWriteTimeout implements Connection.
func (c *connection) SetWriteTimeout(timeout time.Duration) error {
	if timeout >= 0 {
		c.writeTimeout = timeout
	}
	c.writeDeadline = 0
	return nil
}

// SetDeadline implements net.Conn.SetDeadline
func (c *connection) SetDeadline(t time.Time) error {
	v := int64(0)
	if !t.IsZero() {
		v = t.UnixNano()
	}
	c.readDeadline = v
	c.writeDeadline = v
	return nil
}

// SetReadDeadline implements net.Conn.SetReadDeadline
func (c *connection) SetReadDeadline(t time.Time) error {
	if t.IsZero() {
		c.readDeadline = 0
	} else {
		c.readDeadline = t.UnixNano()
	}
	return nil
}

// SetWriteDeadline implements net.Conn.SetWriteDeadline
func (c *connection) SetWriteDeadline(t time.Time) error {
	if t.IsZero() {
		c.writeDeadline = 0
	} else {
		c.writeDeadline = t.UnixNano()
	}
	return nil
}

// ------------------------------------------ implement zero-copy reader ------------------------------------------

// Next implements Connection.
func (c *connection) Next(n int) (p []byte, err error) {
	if err = c.waitRead(n); err != nil {
		return p, err
	}
	return c.inputBuffer.Next(n)
}

// Peek implements Connection.
func (c *connection) Peek(n int) (buf []byte, err error) {
	if err = c.waitRead(n); err != nil {
		return buf, err
	}
	return c.inputBuffer.Peek(n)
}

// Skip implements Connection.
func (c *connection) Skip(n int) (err error) {
	if err = c.waitRead(n); err != nil {
		return err
	}
	return c.inputBuffer.Skip(n)
}

// Release implements Connection.
func (c *connection) Release() (err error) {
	// Check inputBuffer length first to reduce contention in mux situation.
	// c.operator.do competes with c.inputs/c.inputAck
	if c.inputBuffer.Len() == 0 && netpoll.Do(c.operator) {
		maxSize := c.inputBuffer.calcMaxSize()
		// Set the maximum value of maxsize equal to mallocMax to prevent GC pressure.
		if maxSize > mallocMax {
			maxSize = mallocMax
		}

		if maxSize > c.maxSize {
			c.maxSize = maxSize
		}
		// Double check length to reset tail node
		if c.inputBuffer.Len() == 0 {
			c.inputBuffer.resetTail(c.maxSize)
		}
		netpoll.Done(c.operator)
	}
	return c.inputBuffer.Release()
}

// Slice implements Connection.
func (c *connection) Slice(n int) (r Reader, err error) {
	if err = c.waitRead(n); err != nil {
		return nil, err
	}
	return c.inputBuffer.Slice(n)
}

// Len implements Connection.
func (c *connection) Len() (length int) {
	return c.inputBuffer.Len()
}

// Until implements Connection.
func (c *connection) Until(delim byte) (line []byte, err error) {
	var n, l int
	for {
		if err = c.waitRead(n + 1); err != nil {
			// return all the data in the buffer
			line, _ = c.inputBuffer.Next(c.inputBuffer.Len())
			return
		}

		l = c.inputBuffer.Len()
		i := c.inputBuffer.indexByte(delim, n)
		if i < 0 {
			n = l // skip all exists bytes
			continue
		}
		return c.Next(i + 1)
	}
}

// ReadString implements Connection.
func (c *connection) ReadString(n int) (s string, err error) {
	if err = c.waitRead(n); err != nil {
		return s, err
	}
	return c.inputBuffer.ReadString(n)
}

// ReadBinary implements Connection.
func (c *connection) ReadBinary(n int) (p []byte, err error) {
	if err = c.waitRead(n); err != nil {
		return p, err
	}
	return c.inputBuffer.ReadBinary(n)
}

// ReadByte implements Connection.
func (c *connection) ReadByte() (b byte, err error) {
	if err = c.waitRead(1); err != nil {
		return b, err
	}
	return c.inputBuffer.ReadByte()
}

// ------------------------------------------ implement zero-copy writer ------------------------------------------

// Malloc implements Connection.
func (c *connection) Malloc(n int) (buf []byte, err error) {
	if !c.IsActive() {
		return nil, Exception(ErrConnClosed, "when malloc")
	}
	return c.outputBuffer.Malloc(n)
}

// MallocLen implements Connection.
func (c *connection) MallocLen() (length int) {
	return c.outputBuffer.MallocLen()
}

// Flush will send all malloc data to the peer,
// so must confirm that the allocated bytes have been correctly assigned.
//
// Flush first checks whether the out buffer is empty.
// If empty, it will call unix.Write to send data directly,
// otherwise the buffer will be sent asynchronously by the epoll trigger.
func (c *connection) Flush() error {
	if !c.IsActive() {
		return Exception(ErrConnClosed, "when flush")
	}

	if !c.lock(flushing) {
		return Exception(ErrConcurrentAccess, "when flush")
	}
	defer c.unlock(flushing)

	c.outputBuffer.Flush()
	return c.flush()
}

// MallocAck implements Connection.
func (c *connection) MallocAck(n int) (err error) {
	if !c.IsActive() {
		return Exception(ErrConnClosed, "when malloc ack")
	}
	return c.outputBuffer.MallocAck(n)
}

// Append implements Connection.
func (c *connection) Append(w Writer) (err error) {
	if !c.IsActive() {
		return Exception(ErrConnClosed, "when append")
	}
	return c.outputBuffer.Append(w)
}

// WriteString implements Connection.
func (c *connection) WriteString(s string) (n int, err error) {
	if !c.IsActive() {
		return 0, Exception(ErrConnClosed, "when write string")
	}
	return c.outputBuffer.WriteString(s)
}

// WriteBinary implements Connection.
func (c *connection) WriteBinary(b []byte) (n int, err error) {
	if !c.IsActive() {
		return 0, Exception(ErrConnClosed, "when write binary")
	}
	return c.outputBuffer.WriteBinary(b)
}

// WriteDirect implements Connection.
func (c *connection) WriteDirect(p []byte, remainCap int) (err error) {
	if !c.IsActive() {
		return Exception(ErrConnClosed, "when write direct")
	}
	return c.outputBuffer.WriteDirect(p, remainCap)
}

// WriteByte implements Connection.
func (c *connection) WriteByte(b byte) (err error) {
	if !c.IsActive() {
		return Exception(ErrConnClosed, "when write byte")
	}
	return c.outputBuffer.WriteByte(b)
}

// ------------------------------------------ implement net.Conn ------------------------------------------

// Read behavior is the same as net.Conn, it will return io.EOF if buffer is empty.
func (c *connection) Read(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err = c.waitRead(1); err != nil {
		return 0, err
	}
	return c.inputBuffer.readCopy(p), nil
}

// Write will Flush soon.
func (c *connection) Write(p []byte) (n int, err error) {
	if !c.IsActive() {
		return 0, Exception(ErrConnClosed, "when write")
	}

	if !c.lock(flushing) {
		return 0, Exception(ErrConcurrentAccess, "when write")
	}
	defer c.unlock(flushing)

	dst, _ := c.outputBuffer.Malloc(len(p))
	n = copy(dst, p)
	c.outputBuffer.Flush()
	err = c.flush()
	return n, err
}

// Close implements Connection.
func (c *connection) Close() error {
	return c.onClose()
}

// Detach detaches the connection from poller but doesn't close it.
func (c *connection) Detach() error {
	c.detaching = true
	return c.onClose()
}

// ------------------------------------------ private ------------------------------------------

var barrierPool = sync.Pool{
	New: func() any { return socket.NewBarrier() },
}

// init initializes the connection with options
func (c *connection) init(conn Conn, opts *options) (err error) {
	// init buffer, barrier, finalizer
	c.readTrigger = make(chan error, 1)
	c.writeTrigger = make(chan error, 1)
	c.bookSize, c.maxSize = defaultLinkBufferSize, defaultLinkBufferSize
	// Both buffers start empty: the input buffer is allocated by book() on the first read,
	// so idle connections do not hold defaultLinkBufferSize bytes each.
	c.inputBuffer, c.outputBuffer = NewLinkBuffer(), NewLinkBuffer()
	c.state = connStateNone

	c.initNetFD(conn) // conn must be *netFD{}
	c.initFDOperator()
	c.initFinalizer()

	unix.SetNonblock(c.fd, true)
	// enable TCP_NODELAY by default
	switch c.network {
	case "tcp", "tcp4", "tcp6":
		socket.SetTCPNoDelay(c.fd, true)
	}

	// connection initialized and prepare options
	return c.onPrepare(opts)
}

func (c *connection) initNetFD(conn Conn) {
	if nfd, ok := conn.(*netFD); ok {
		c.netFD = *nfd
		return
	}
	c.netFD = netFD{
		fd:         conn.Fd(),
		localAddr:  conn.LocalAddr(),
		remoteAddr: conn.RemoteAddr(),
	}
}

func (c *connection) initFDOperator() {
	poll := netpoll.Default.Pick()
	op := poll.Alloc()
	op.FD = c.fd
	op.OnRead, op.OnWrite, op.OnHup = nil, nil, c.onHup
	op.Inputs, op.InputAck = c.inputs, c.inputAck
	op.Outputs, op.OutputAck = c.outputs, c.outputAck
	c.operator = op
}

func (c *connection) initFinalizer() {
	c.AddCloseCallback(func(connection Connection) (err error) {
		c.stop(flushing)
		release := func() {
			c.operator.Free()
			if err := c.netFD.Close(); err != nil {
				logging.Printf("[anet]: netFD close failed: %v", err)
			}
			c.closeBuffer()
		}
		// Closing from an inline handler runs on the poller goroutine while the poller still owns the operator,
		// and Free waits for the poller to release it. So release after the poller finished this batch.
		if c.onPoller {
			if lp, ok := netpoll.PollOf(c.operator).(netpoll.Deferrer); ok {
				lp.Later(release)
				return nil
			}
		}
		release()
		return nil
	})
}

func (c *connection) triggerRead(err error) {
	select {
	case c.readTrigger <- err:
	default:
	}
}

func (c *connection) triggerWrite(err error) {
	select {
	case c.writeTrigger <- err:
	default:
	}
}

// waitRead will wait full n bytes.
func (c *connection) waitRead(n int) (err error) {
	if n <= c.inputBuffer.Len() {
		return nil
	}
	if c.inline {
		// Waiting would block the poller goroutine, which is the only one that can deliver more data.
		return Exception(ErrNotEnoughData, "wait read")
	}
	atomic.StoreInt64(&c.waitReadSize, int64(n))
	defer atomic.StoreInt64(&c.waitReadSize, 0)
	if dl := c.readDeadline; dl > 0 {
		timeout := time.Duration(dl - time.Now().UnixNano())
		if timeout <= 0 {
			return Exception(ErrReadTimeout, c.remoteAddr.String())
		}
		return c.waitReadWithTimeout(n, timeout)
	} else if c.readTimeout > 0 {
		return c.waitReadWithTimeout(n, c.readTimeout)
	}
	// wait full n
	for c.inputBuffer.Len() < n {
		switch c.status(closing) {
		case poller:
			return Exception(ErrEOF, "wait read")
		case user:
			return Exception(ErrConnClosed, "wait read")
		default:
			err = <-c.readTrigger
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// waitReadWithTimeout will wait full n bytes or until timeout.
func (c *connection) waitReadWithTimeout(n int, timeout time.Duration) (err error) {
	if c.readTimer == nil {
		c.readTimer = time.NewTimer(timeout)
	} else {
		c.readTimer.Reset(timeout)
	}

	for c.inputBuffer.Len() < n {
		switch c.status(closing) {
		case poller:
			// cannot return directly, stop timer first!
			err = Exception(ErrEOF, "wait read")
			goto RET
		case user:
			// cannot return directly, stop timer first!
			err = Exception(ErrConnClosed, "wait read")
			goto RET
		default:
			select {
			case <-c.readTimer.C:
				// double check if there is enough data to be read
				if c.inputBuffer.Len() >= n {
					return nil
				}
				return Exception(ErrReadTimeout, c.remoteAddr.String())
			case err = <-c.readTrigger:
				if err != nil {
					goto RET
				}
				continue
			}
		}
	}
RET:
	// clean timer.C
	if !c.readTimer.Stop() {
		<-c.readTimer.C
	}
	return err
}

// flush writes data directly.
func (c *connection) flush() error {
	if c.outputBuffer.IsEmpty() {
		return nil
	}
	// The data was published by outputBuffer.Flush. If the poller is sending, it will send this too.
	if !c.outOwner.CompareAndSwap(outIdle, outWriter) {
		return nil
	}
	// The barrier is only needed while sending, so take it from the pool for this call only.
	barrier := barrierPool.Get().(*socket.Barrier)
	bs := c.outputBuffer.GetBytes(barrier.Bs)
	n, err := socket.Sendmsg(c.fd, bs, barrier.Ivs, false)
	barrierPool.Put(barrier)
	if err != nil && err != unix.EAGAIN {
		c.outOwner.Store(outIdle)
		return Exception(err, "when flush")
	}
	if n > 0 {
		err = c.outputBuffer.Skip(n)
		c.outputBuffer.Release()
		if err != nil {
			c.outOwner.Store(outIdle)
			return Exception(err, "when flush")
		}
	}
	// return if write all buffer.
	if c.outputBuffer.IsEmpty() {
		// Everything is sent and we own the buffer: give its memory back to the shared pool.
		c.outputBuffer.dropIfDrained()
		c.outOwner.Store(outIdle)
		return nil
	}
	// Hand the rest to the poller, which owns the buffer until it is drained (see outputDrained).
	c.outOwner.Store(outPoller)
	err = c.operator.Control(PollR2RW)
	if err != nil {
		return Exception(err, "when flush")
	}
	if c.inline {
		// The remaining data is sent by the poller when the socket becomes writable.
		// Waiting here would block the poller goroutine that has to do that.
		return nil
	}

	return c.waitFlush()
}

func (c *connection) waitFlush() (err error) {
	timeout := c.writeTimeout
	if dl := c.writeDeadline; dl > 0 {
		timeout = time.Duration(dl - time.Now().UnixNano())
		if timeout <= 0 {
			return Exception(ErrWriteTimeout, c.remoteAddr.String())
		}
	}
	if timeout == 0 {
		return <-c.writeTrigger
	}

	// set write timeout
	if c.writeTimer == nil {
		c.writeTimer = time.NewTimer(timeout)
	} else {
		c.writeTimer.Reset(timeout)
	}

	select {
	case err = <-c.writeTrigger:
		if !c.writeTimer.Stop() { // clean timer
			<-c.writeTimer.C
		}
		return err
	case <-c.writeTimer.C:
		select {
		// try fetch writeTrigger if both cases fires
		case err = <-c.writeTrigger:
			return err
		default:
		}
		// if timeout, remove write event from poller
		// we cannot flush it again, since we don't if the poller is still process outputBuffer
		c.operator.Control(PollRW2R)
		c.outOwner.Store(outIdle) // the next Flush sends the rest itself
		return Exception(ErrWriteTimeout, c.remoteAddr.String())
	}
}

func (c *connection) getState() connState {
	return atomic.LoadInt32(&c.state)
}

func (c *connection) setState(newState connState) {
	atomic.StoreInt32(&c.state, newState)
}

func (c *connection) changeState(from, to connState) bool {
	return atomic.CompareAndSwapInt32(&c.state, from, to)
}
