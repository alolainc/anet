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

package socket

import (
	"strconv"

	"golang.org/x/sys/unix"

	. "github.com/alolainc/anet/internal/testutil"

	"math"
	"testing"
)

func TestIovecs(t *testing.T) {
	var got int
	var bs [][]byte
	ivs := make([]unix.Iovec, 4)

	// case 1
	bs = [][]byte{
		make([]byte, 10),
		make([]byte, 20),
		make([]byte, 30),
		make([]byte, 40),
	}
	got = Iovecs(bs, ivs)
	Equal(t, got, 4)
	Equal(t, int(ivs[0].Len), 10)
	Equal(t, int(ivs[1].Len), 20)
	Equal(t, int(ivs[2].Len), 30)
	Equal(t, int(ivs[3].Len), 40)

	// case 2: a buffer larger than an iovec may describe (64-bit only)
	if strconv.IntSize < 64 {
		return
	}
	ResetIovecs(bs, ivs)
	huge := int64(math.MaxInt32) + 100
	bs = [][]byte{
		make([]byte, int(huge)),
		make([]byte, 20),
		make([]byte, 30),
		make([]byte, 40),
	}
	got = Iovecs(bs, ivs)
	Equal(t, got, 1)
	Equal(t, int(ivs[0].Len), math.MaxInt32)
	Assert(t, ivs[1].Base == nil)
	Assert(t, ivs[2].Base == nil)
	Assert(t, ivs[3].Base == nil)

	// case 3
	ResetIovecs(bs, ivs)
	bs = [][]byte{
		make([]byte, 10),
		make([]byte, 20),
		make([]byte, int(huge)),
		make([]byte, 40),
	}
	got = Iovecs(bs, ivs)
	Equal(t, got, 3)
	Equal(t, int(ivs[0].Len), 10)
	Equal(t, int(ivs[1].Len), 20)
	Equal(t, int(ivs[2].Len), math.MaxInt32-30)
	Assert(t, ivs[3].Base == nil)
}

func TestWritev(t *testing.T) {
	r, w := GetSysFdPairs()
	br := Barrier{}
	br.Bs = [][]byte{
		[]byte(""),            // len=0
		[]byte("first line"),  // len=10
		[]byte("second line"), // len=11
		[]byte("third line"),  // len=10
	}
	br.Ivs = make([]unix.Iovec, len(br.Bs))
	wn, err := Writev(w, br.Bs, br.Ivs)
	MustNil(t, err)
	Equal(t, wn, 31)
	p := make([]byte, 50)
	rn, err := unix.Read(r, p)
	MustNil(t, err)
	Equal(t, rn, 31)
	t.Logf("READ %s", p[:rn])
}

func TestReadv(t *testing.T) {
	r, w := GetSysFdPairs()
	vs := [][]byte{
		[]byte("first line"),  // len=10
		[]byte("second line"), // len=11
		[]byte("third line"),  // len=10
	}
	w1, _ := unix.Write(w, vs[0])
	w2, _ := unix.Write(w, vs[1])
	w3, _ := unix.Write(w, vs[2])
	Equal(t, w1+w2+w3, 31)

	br := Barrier{
		Bs: make([][]byte, 4),
	}
	res := [][]byte{
		make([]byte, 0),
		make([]byte, 10),
		make([]byte, 11),
		make([]byte, 10),
	}
	copy(br.Bs, res)
	br.Ivs = make([]unix.Iovec, len(br.Bs))
	rn, err := Readv(r, br.Bs, br.Ivs)
	MustNil(t, err)
	Equal(t, rn, 31)
	for i, v := range res {
		t.Logf("READ [%d] %s", i, v)
	}
}

func TestSendmsg(t *testing.T) {
	r, w := GetSysFdPairs()
	br := Barrier{}
	br.Bs = [][]byte{
		[]byte(""),            // len=0
		[]byte("first line"),  // len=10
		[]byte("second line"), // len=11
		[]byte("third line"),  // len=10
	}
	br.Ivs = make([]unix.Iovec, len(br.Bs))
	wn, err := Sendmsg(w, br.Bs, br.Ivs, false)
	MustNil(t, err)
	Equal(t, wn, 31)
	p := make([]byte, 50)
	rn, err := unix.Read(r, p)
	MustNil(t, err)
	Equal(t, rn, 31)
	t.Logf("READ %s", p[:rn])
}

func BenchmarkWrite(b *testing.B) {
	b.StopTimer()
	r, w := GetSysFdPairs()
	message := "hello, world!"
	size := 5

	go func() {
		buffer := make([]byte, 13)
		for {
			unix.Read(r, buffer)
		}
	}()

	// benchmark
	b.ReportAllocs()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		wmsg := make([]byte, len(message)*5)
		var n int
		for j := 0; j < size; j++ {
			n += copy(wmsg[n:], message)
		}
		unix.Write(w, wmsg)
	}
}

func BenchmarkWritev(b *testing.B) {
	b.StopTimer()
	r, w := GetSysFdPairs()
	message := "hello, world!"
	size := 5
	br := Barrier{}
	br.Bs = make([][]byte, size)
	br.Ivs = make([]unix.Iovec, len(br.Bs))
	for i := range br.Bs {
		br.Bs[i] = make([]byte, len(message))
	}

	go func() {
		buffer := make([]byte, 13)
		for {
			unix.Read(r, buffer)
		}
	}()

	// benchmark
	b.ReportAllocs()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		Writev(w, br.Bs, br.Ivs)
	}
}

func BenchmarkSendmsg(b *testing.B) {
	b.StopTimer()
	r, w := GetSysFdPairs()
	message := "hello, world!"
	size := 5
	br := Barrier{}
	br.Bs = make([][]byte, size)
	br.Ivs = make([]unix.Iovec, len(br.Bs))
	for i := range br.Bs {
		br.Bs[i] = make([]byte, len(message))
	}

	go func() {
		buffer := make([]byte, 13)
		for {
			unix.Read(r, buffer)
		}
	}()

	// benchmark
	b.ReportAllocs()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		Sendmsg(w, br.Bs, br.Ivs, false)
	}
}

func BenchmarkRead(b *testing.B) {
	b.StopTimer()
	r, w := GetSysFdPairs()
	message := "hello, world!"
	size := 5
	wmsg := make([]byte, size*len(message))
	var n int
	for j := 0; j < size; j++ {
		n += copy(wmsg[n:], message)
	}

	go func() {
		for {
			unix.Write(w, wmsg)
		}
	}()

	// benchmark
	b.ReportAllocs()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		buffer := make([]byte, size*len(message))
		unix.Read(r, buffer)
	}
}

func BenchmarkReadv(b *testing.B) {
	b.StopTimer()
	r, w := GetSysFdPairs()
	message := "hello, world!"
	size := 5
	br := Barrier{}
	br.Bs = make([][]byte, size)
	br.Ivs = make([]unix.Iovec, len(br.Bs))
	for i := range br.Bs {
		br.Bs[i] = make([]byte, len(message))
	}

	go func() {
		for {
			writeAll(w, []byte(message))
		}
	}()

	// benchmark
	b.ReportAllocs()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		Readv(r, br.Bs, br.Ivs)
	}
}

func writeAll(fd int, buf []byte) error {
	for len(buf) > 0 {
		n, err := unix.Write(fd, buf)
		if n < 0 {
			return err
		}
		buf = buf[n:]
	}
	return nil
}
