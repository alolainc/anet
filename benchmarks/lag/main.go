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

// Command lag measures how much latency a slow link adds to a stream sent with anet.
//
// A source writes small frames at a fixed rate that is higher than the link can carry; the receiver
// reports the age of each frame when it arrives. Run it on a link whose rate is limited, for example
//
//	tc qdisc add dev lo root netem rate 2mbit
//
// and compare the configurations given by -mode.
package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/alolainc/anet"
	"github.com/alolainc/anet/tee"
)

var (
	mode     = flag.String("mode", "default", "default | lowat | lowat+age")
	frameLen = flag.Int("frame", 1000, "frame size in bytes")
	interval = flag.Duration("interval", 2*time.Millisecond, "time between frames")
	duration = flag.Duration("d", 8*time.Second, "how long to send")
	maxAge   = flag.Duration("maxage", 150*time.Millisecond, "MaxAge in lowat+age mode")
	lowat    = flag.Int("lowat", 16<<10, "TCP_NOTSENT_LOWAT in lowat modes")
	pending  = flag.Int("pending", 32<<10, "tee MaxPending in lowat modes")
)

func main() {
	flag.Parse()

	cfg := tee.Config{MaxPending: 64 << 20, Policy: tee.DropOldest}
	var opts []anet.Option
	switch *mode {
	case "default":
	case "lowat":
		opts = append(opts, anet.WithNotSentLowat(*lowat))
		cfg.MaxPending = *pending
	case "lowat+age":
		opts = append(opts, anet.WithNotSentLowat(*lowat))
		cfg.MaxPending = *pending
		cfg.MaxAge = *maxAge
	default:
		panic("unknown mode")
	}
	t := tee.New(cfg)
	opts = append(opts, anet.WithOnConnect(func(ctx context.Context, c anet.Connection) context.Context {
		t.Add(c)
		return ctx
	}))

	ln, err := anet.CreateListener("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	el, _ := anet.NewEventLoop(nil, opts...)
	go el.Serve(ln)

	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		panic(err)
	}
	for t.Len() == 0 {
		time.Sleep(time.Millisecond)
	}

	// receiver: age of each frame on arrival
	var ages []time.Duration
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, *frameLen)
		for {
			if _, err := readFull(raw, buf); err != nil {
				return
			}
			sent := time.Unix(0, int64(binary.BigEndian.Uint64(buf)))
			ages = append(ages, time.Since(sent))
		}
	}()

	end := time.Now().Add(*duration)
	sentFrames := 0
	for tick := time.NewTicker(*interval); time.Now().Before(end); <-tick.C {
		f := make([]byte, *frameLen)
		binary.BigEndian.PutUint64(f, uint64(time.Now().UnixNano()))
		t.Write(f)
		sentFrames++
	}
	time.Sleep(500 * time.Millisecond)
	raw.Close()
	<-done

	_, dropped := t.Stats()
	sort.Slice(ages, func(i, j int) bool { return ages[i] < ages[j] })
	pct := func(p float64) time.Duration {
		if len(ages) == 0 {
			return 0
		}
		return ages[int(float64(len(ages)-1)*p)].Round(time.Millisecond)
	}
	fmt.Printf("mode=%-10s lowat=%d pending=%d sent=%d received=%d dropped=%d  age p50=%v p90=%v p99=%v max=%v\n",
		*mode, *lowat, *pending, sentFrames, len(ages), dropped, pct(0.5), pct(0.9), pct(0.99), pct(1))
}

func readFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := c.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
