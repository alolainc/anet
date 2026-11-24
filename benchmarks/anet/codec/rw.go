/*
 * Copyright 2024 Alola Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package codec

import (
	"encoding/binary"

	"github.com/alolainc/anet"

	"github.com/alolainc/anet/benchmarks/runner"
)

// encode
func Encode(writer anet.Writer, msg *runner.Message) (err error) {
	header, _ := writer.Malloc(4)
	binary.BigEndian.PutUint32(header, uint32(len(msg.Message)))

	writer.WriteString(msg.Message)
	err = writer.Flush()
	return err
}

// Decode reads one message without consuming anything until the whole frame is buffered.
// In the default mode Peek waits for the data; with anet.WithInlineRequest it returns
// anet.ErrNotEnoughData instead, and the caller should return to wait for more data.
func Decode(reader anet.Reader, msg *runner.Message) (err error) {
	bLen, err := reader.Peek(4)
	if err != nil {
		return err
	}
	l := int(binary.BigEndian.Uint32(bLen))
	if _, err = reader.Peek(4 + l); err != nil {
		return err
	}
	if err = reader.Skip(4); err != nil {
		return err
	}
	msg.Message, err = reader.ReadString(l)
	if err != nil {
		return err
	}
	return reader.Release()
}
