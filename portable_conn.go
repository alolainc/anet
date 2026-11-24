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

import (
	"context"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// This file implements a portable Connection on top of net.Conn, using one read goroutine
// per connection. It has no OS specific code, so it works wherever the Go runtime has its own
// network poller (Windows uses IOCP underneath). It is the fallback for platforms where anet
// has no native poller. It is compiled on every platform so that it can be tested on Linux,
// but only wired into the public API on Windows (see anet_windows.go).

// portableBarrierCap is the maximum number of segments gathered per write.
const portableBarrierCap = 32

// stdConn implements Connection over a net.Conn.
type stdConn struct {
	net.Conn
	locker

	ctx          context.Context
	onConnect    OnConnect
	onDisconnect OnDisconnect
	onRequest    atomic.Value // OnRequest

	inputBuffer  *LinkBuffer
	outputBuffer *LinkBuffer
	bookSize     int
	maxSize      int
	wbs          [][]byte // reusable gather list for writes

	readTimeout   time.Duration
	writeTimeout  time.Duration
	readDeadline  int64 // UnixNano, 0 if not set
	writeDeadline int64 // UnixNano, 0 if not set
	readTimer     *time.Timer
	waitReadSize  int64
	readTrigger   chan error // wakes goroutines blocked in waitRead
	reqSignal     chan struct{}

	// lifecycle
	mu           sync.Mutex
	closeFns     []CloseCallback
	readDone     bool          // read loop exited
	procDone     bool          // request loop exited
	userClosed   bool          // Close was called by the user
	autoClose    bool          // release resources without waiting for the user (OnRequest/OnConnect set)
	finalized    bool          // resources released and close callbacks run
	readEnded    chan struct{} // closed when the read loop exits
	remoteString string
}

var (
	_ Connection = &stdConn{}
	_ Reader     = &stdConn{}
	_ Writer     = &stdConn{}
)

// newStdConn wraps raw and starts serving it. opts may be nil (client side).
func newStdConn(raw net.Conn, opts *options) *stdConn {
	c := &stdConn{
		Conn:         raw,
		bookSize:     defaultLinkBufferSize,
		maxSize:      defaultLinkBufferSize,
		inputBuffer:  NewLinkBuffer(),
		outputBuffer: NewLinkBuffer(),
		wbs:          make([][]byte, portableBarrierCap),
		readTrigger:  make(chan error, 1),
		reqSignal:    make(chan struct{}, 1),
		readEnded:    make(chan struct{}),
	}
	if ra := raw.RemoteAddr(); ra != nil {
		c.remoteString = ra.String()
	}
	if opts != nil {
		c.onConnect = opts.onConnect
		c.onDisconnect = opts.onDisconnect
		if opts.onRequest != nil {
			c.onRequest.Store(opts.onRequest)
		}
		c.SetReadTimeout(opts.readTimeout)
		c.SetWriteTimeout(opts.writeTimeout)
		c.SetIdleTimeout(opts.idleTimeout)
		if opts.sendBuffer > 0 {
			c.SetSendBuffer(opts.sendBuffer)
		}
		if opts.recvBuffer > 0 {
			c.SetRecvBuffer(opts.recvBuffer)
		}
		if opts.onPrepare != nil {
			c.ctx = opts.onPrepare(c)
		}
	}
	if c.ctx == nil {
		c.ctx = context.Background()
	}
	c.autoClose = c.onConnect != nil || c.loadOnRequest() != nil
	go c.run()
	return c
}

func (c *stdConn) loadOnRequest() OnRequest {
	f, _ := c.onRequest.Load().(OnRequest)
	return f
}

// run drives the connection lifecycle: OnConnect, then the read loop with a request loop beside it.
func (c *stdConn) run() {
	if c.IsActive() && c.onConnect != nil {
		c.ctx = c.onConnect(c.ctx, c)
	}
	go c.requestLoop()
	c.readLoop()
}

func (c *stdConn) readLoop() {
	defer func() {
		c.mu.Lock()
		c.readDone = true
		c.mu.Unlock()
		close(c.readEnded)
		c.maybeFinalize()
	}()
	for c.IsActive() {
		p := c.inputBuffer.book(c.bookSize, c.maxSize)
		n, err := c.Conn.Read(p)
		c.inputAck(n)
		if err != nil {
			c.onPeerClose()
			return
		}
	}
}

// inputAck publishes n bytes read from the network.
func (c *stdConn) inputAck(n int) {
	if n <= 0 {
		c.inputBuffer.bookAck(0)
		return
	}
	// Auto size bookSize.
	if n == c.bookSize && c.bookSize < mallocMax {
		c.bookSize <<= 1
	}
	length, _ := c.inputBuffer.bookAck(n)
	if c.maxSize < length {
		c.maxSize = length
	}
	if c.maxSize > mallocMax {
		c.maxSize = mallocMax
	}
	select {
	case c.reqSignal <- struct{}{}:
	default:
	}
	if length >= int(atomic.LoadInt64(&c.waitReadSize)) {
		c.triggerRead(nil)
	}
}

// onPeerClose handles the read side ending (EOF, reset, or the connection being closed locally).
func (c *stdConn) onPeerClose() {
	if !c.closeBy(poller) {
		return // closed by the user, which already woke the readers
	}
	c.triggerRead(Exception(ErrEOF, "peer close"))
	if c.onDisconnect != nil {
		c.onDisconnect(c.ctx, c)
	}
}

func (c *stdConn) triggerRead(err error) {
	select {
	case c.readTrigger <- err:
	default:
	}
}

// requestLoop runs OnRequest serially whenever there is unread data.
func (c *stdConn) requestLoop() {
	defer func() {
		c.mu.Lock()
		c.procDone = true
		c.mu.Unlock()
		c.maybeFinalize()
	}()
	for {
		select {
		case <-c.reqSignal:
		case <-c.readEnded:
		}
		if !c.drainRequests() {
			return
		}
		select {
		case <-c.readEnded:
			// The read side is finished, so no more data will arrive. drainRequests has already
			// consumed everything the handler could, so exit instead of spinning.
			return
		default:
		}
	}
}

// drainRequests calls OnRequest while data is available. It returns false when the loop should exit.
func (c *stdConn) drainRequests() bool {
	for c.inputBuffer.Len() > 0 && !c.isCloseBy(user) {
		onRequest := c.loadOnRequest()
		if onRequest == nil {
			return !c.isCloseBy(user)
		}
		if !c.lock(processing) {
			return true
		}
		before := c.inputBuffer.Len()
		c.runOnRequest(onRequest)
		c.unlock(processing)
		if c.inputBuffer.Len() >= before {
			// OnRequest made no progress: wait for more data instead of spinning.
			select {
			case <-c.readEnded:
				return false // nothing more will arrive
			default:
			}
			return true
		}
	}
	return !c.isCloseBy(user)
}

func (c *stdConn) runOnRequest(f OnRequest) {
	panicked := true
	defer func() {
		if panicked {
			c.unlock(processing)
			c.Close()
		}
	}()
	_ = f(c.ctx, c)
	panicked = false
}

// maybeFinalize releases resources once the read and request loops are done and
// the connection may be released.
func (c *stdConn) maybeFinalize() {
	c.mu.Lock()
	if c.finalized || !c.readDone || !c.procDone || !(c.userClosed || c.autoClose) {
		c.mu.Unlock()
		return
	}
	c.finalized = true
	fns := c.closeFns
	c.closeFns = nil
	c.mu.Unlock()

	c.stop(flushing)
	c.Conn.Close()
	for i := len(fns) - 1; i >= 0; i-- {
		fns[i](c)
	}
	// Release buffers unless a peer-closed connection still has unread data nobody will read.
	if c.inputBuffer.Len() == 0 || c.autoClose || c.userClosed {
		c.inputBuffer.Close()
	}
	if c.outputBuffer.Len() == 0 || c.autoClose || c.userClosed {
		c.outputBuffer.Close()
	}
}

// ------------------------------------------ Connection ------------------------------------------

// Reader implements Connection.
func (c *stdConn) Reader() Reader { return c }

// Writer implements Connection.
func (c *stdConn) Writer() Writer { return c }

// IsActive implements Connection.
var _ SocketTuner = (*stdConn)(nil)

// SetSendBuffer implements SocketTuner.
func (c *stdConn) SetSendBuffer(n int) error {
	if tc, ok := c.Conn.(*net.TCPConn); ok {
		return tc.SetWriteBuffer(n)
	}
	return Exception(ErrUnsupported, "send buffer on this connection")
}

// SetRecvBuffer implements SocketTuner.
func (c *stdConn) SetRecvBuffer(n int) error {
	if tc, ok := c.Conn.(*net.TCPConn); ok {
		return tc.SetReadBuffer(n)
	}
	return Exception(ErrUnsupported, "recv buffer on this connection")
}

// SetNotSentLowat implements SocketTuner. The portable backend does not support it.
func (c *stdConn) SetNotSentLowat(n int) error {
	return Exception(ErrUnsupported, "TCP_NOTSENT_LOWAT")
}

// PendingWrite returns the number of flushed bytes that have not been sent yet.
func (c *stdConn) PendingWrite() int {
	return c.outputBuffer.Len()
}

func (c *stdConn) IsActive() bool { return c.isCloseBy(none) }

// SetOnRequest implements Connection.
func (c *stdConn) SetOnRequest(on OnRequest) error {
	if on == nil {
		return nil
	}
	c.onRequest.Store(on)
	c.mu.Lock()
	c.autoClose = true
	c.mu.Unlock()
	select {
	case c.reqSignal <- struct{}{}:
	default:
	}
	return nil
}

// AddCloseCallback implements Connection.
func (c *stdConn) AddCloseCallback(callback CloseCallback) error {
	if callback == nil {
		return nil
	}
	c.mu.Lock()
	if c.finalized {
		c.mu.Unlock()
		return callback(c)
	}
	c.closeFns = append(c.closeFns, callback)
	c.mu.Unlock()
	return nil
}

// SetIdleTimeout implements Connection by enabling TCP keep-alive when the transport supports it.
func (c *stdConn) SetIdleTimeout(timeout time.Duration) error {
	if timeout <= 0 {
		return nil
	}
	if tc, ok := c.Conn.(*net.TCPConn); ok {
		if err := tc.SetKeepAlive(true); err != nil {
			return err
		}
		return tc.SetKeepAlivePeriod(timeout)
	}
	return nil
}

// SetReadTimeout implements Connection.
func (c *stdConn) SetReadTimeout(timeout time.Duration) error {
	if timeout >= 0 {
		c.readTimeout = timeout
	}
	atomic.StoreInt64(&c.readDeadline, 0)
	return nil
}

// SetWriteTimeout implements Connection.
func (c *stdConn) SetWriteTimeout(timeout time.Duration) error {
	if timeout >= 0 {
		c.writeTimeout = timeout
	}
	atomic.StoreInt64(&c.writeDeadline, 0)
	return nil
}

// SetDeadline implements net.Conn. Deadlines apply to the nocopy Reader/Writer API;
// the underlying socket deadline is managed internally.
func (c *stdConn) SetDeadline(t time.Time) error {
	c.SetReadDeadline(t)
	return c.SetWriteDeadline(t)
}

// SetReadDeadline implements net.Conn.
func (c *stdConn) SetReadDeadline(t time.Time) error {
	atomic.StoreInt64(&c.readDeadline, unixNano(t))
	return nil
}

// SetWriteDeadline implements net.Conn.
func (c *stdConn) SetWriteDeadline(t time.Time) error {
	atomic.StoreInt64(&c.writeDeadline, unixNano(t))
	return nil
}

func unixNano(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

// Close implements Connection.
func (c *stdConn) Close() error {
	c.mu.Lock()
	c.userClosed = true
	c.mu.Unlock()
	if c.closeBy(user) {
		c.triggerRead(Exception(ErrConnClosed, "self close"))
		c.Conn.Close() // unblocks the read loop
	} else {
		// already closed by the peer: the user now takes over releasing resources
		c.force(closing, user)
	}
	c.maybeFinalize()
	return nil
}

// isIdle reports whether the connection has no work in progress.
func (c *stdConn) isIdle() bool {
	return c.isUnlock(processing) && c.inputBuffer.IsEmpty() && c.outputBuffer.IsEmpty()
}

// ------------------------------------------ zero-copy reader ------------------------------------------

// waitRead waits until n bytes are buffered.
func (c *stdConn) waitRead(n int) (err error) {
	if n <= c.inputBuffer.Len() {
		return nil
	}
	atomic.StoreInt64(&c.waitReadSize, int64(n))
	defer atomic.StoreInt64(&c.waitReadSize, 0)

	timeout := c.readTimeout
	if dl := atomic.LoadInt64(&c.readDeadline); dl > 0 {
		timeout = time.Duration(dl - time.Now().UnixNano())
		if timeout <= 0 {
			return Exception(ErrReadTimeout, c.remoteString)
		}
	}
	var timerC <-chan time.Time
	if timeout > 0 {
		if c.readTimer == nil {
			c.readTimer = time.NewTimer(timeout)
		} else {
			c.readTimer.Reset(timeout)
		}
		defer func() {
			if !c.readTimer.Stop() {
				select {
				case <-c.readTimer.C:
				default:
				}
			}
		}()
		timerC = c.readTimer.C
	}
	for c.inputBuffer.Len() < n {
		switch c.status(closing) {
		case poller:
			return Exception(ErrEOF, "wait read")
		case user:
			return Exception(ErrConnClosed, "wait read")
		}
		select {
		case err = <-c.readTrigger:
			if err != nil {
				return err
			}
		case <-timerC:
			if c.inputBuffer.Len() >= n {
				return nil
			}
			return Exception(ErrReadTimeout, c.remoteString)
		}
	}
	return nil
}

// Next implements Connection.
func (c *stdConn) Next(n int) (p []byte, err error) {
	if err = c.waitRead(n); err != nil {
		return p, err
	}
	return c.inputBuffer.Next(n)
}

// Peek implements Connection.
func (c *stdConn) Peek(n int) (buf []byte, err error) {
	if err = c.waitRead(n); err != nil {
		return buf, err
	}
	return c.inputBuffer.Peek(n)
}

// Skip implements Connection.
func (c *stdConn) Skip(n int) (err error) {
	if err = c.waitRead(n); err != nil {
		return err
	}
	return c.inputBuffer.Skip(n)
}

// Release implements Connection.
func (c *stdConn) Release() (err error) {
	return c.inputBuffer.Release()
}

// Slice implements Connection.
func (c *stdConn) Slice(n int) (r Reader, err error) {
	if err = c.waitRead(n); err != nil {
		return nil, err
	}
	return c.inputBuffer.Slice(n)
}

// Len implements Connection.
func (c *stdConn) Len() (length int) {
	return c.inputBuffer.Len()
}

// Until implements Connection.
func (c *stdConn) Until(delim byte) (line []byte, err error) {
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
func (c *stdConn) ReadString(n int) (s string, err error) {
	if err = c.waitRead(n); err != nil {
		return s, err
	}
	return c.inputBuffer.ReadString(n)
}

// ReadBinary implements Connection.
func (c *stdConn) ReadBinary(n int) (p []byte, err error) {
	if err = c.waitRead(n); err != nil {
		return p, err
	}
	return c.inputBuffer.ReadBinary(n)
}

// ReadByte implements Connection.
func (c *stdConn) ReadByte() (b byte, err error) {
	if err = c.waitRead(1); err != nil {
		return b, err
	}
	return c.inputBuffer.ReadByte()
}

// ------------------------------------------ zero-copy writer ------------------------------------------

// Malloc implements Connection.
func (c *stdConn) Malloc(n int) (buf []byte, err error) {
	if !c.IsActive() {
		return nil, Exception(ErrConnClosed, "when malloc")
	}
	return c.outputBuffer.Malloc(n)
}

// MallocLen implements Connection.
func (c *stdConn) MallocLen() (length int) {
	return c.outputBuffer.MallocLen()
}

// Flush sends all malloc data to the peer.
func (c *stdConn) Flush() error {
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
func (c *stdConn) MallocAck(n int) (err error) {
	if !c.IsActive() {
		return Exception(ErrConnClosed, "when malloc ack")
	}
	return c.outputBuffer.MallocAck(n)
}

// Append implements Connection.
func (c *stdConn) Append(w Writer) (err error) {
	if !c.IsActive() {
		return Exception(ErrConnClosed, "when append")
	}
	return c.outputBuffer.Append(w)
}

// WriteString implements Connection.
func (c *stdConn) WriteString(s string) (n int, err error) {
	if !c.IsActive() {
		return 0, Exception(ErrConnClosed, "when write string")
	}
	return c.outputBuffer.WriteString(s)
}

// WriteBinary implements Connection.
func (c *stdConn) WriteBinary(b []byte) (n int, err error) {
	if !c.IsActive() {
		return 0, Exception(ErrConnClosed, "when write binary")
	}
	return c.outputBuffer.WriteBinary(b)
}

// WriteDirect implements Connection.
func (c *stdConn) WriteDirect(p []byte, remainCap int) (err error) {
	if !c.IsActive() {
		return Exception(ErrConnClosed, "when write direct")
	}
	return c.outputBuffer.WriteDirect(p, remainCap)
}

// WriteByte implements Connection.
func (c *stdConn) WriteByte(b byte) (err error) {
	if !c.IsActive() {
		return Exception(ErrConnClosed, "when write byte")
	}
	return c.outputBuffer.WriteByte(b)
}

// flush writes the whole output buffer to the network, blocking until done or timed out.
func (c *stdConn) flush() error {
	for !c.outputBuffer.IsEmpty() {
		timeout := c.writeTimeout
		if dl := atomic.LoadInt64(&c.writeDeadline); dl > 0 {
			timeout = time.Duration(dl - time.Now().UnixNano())
			if timeout <= 0 {
				return Exception(ErrWriteTimeout, c.remoteString)
			}
		}
		if timeout > 0 {
			c.Conn.SetWriteDeadline(time.Now().Add(timeout))
		} else {
			c.Conn.SetWriteDeadline(time.Time{})
		}
		bs := c.outputBuffer.GetBytes(c.wbs)
		bufs := net.Buffers(bs) // WriteTo consumes the slice header, not the underlying bytes
		n, err := bufs.WriteTo(c.Conn)
		if n > 0 {
			if serr := c.outputBuffer.Skip(int(n)); serr != nil {
				return Exception(serr, "when flush")
			}
			c.outputBuffer.Release()
		}
		if err != nil {
			if os.IsTimeout(err) {
				return Exception(ErrWriteTimeout, c.remoteString)
			}
			return Exception(err, "when flush")
		}
	}
	return nil
}

// ------------------------------------------ net.Conn ------------------------------------------

// Read behaves like net.Conn.Read, returning EOF when the peer closed and the buffer is empty.
func (c *stdConn) Read(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err = c.waitRead(1); err != nil {
		return 0, err
	}
	return c.inputBuffer.readCopy(p), nil
}

// Write copies p into the output buffer and flushes it.
func (c *stdConn) Write(p []byte) (n int, err error) {
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
