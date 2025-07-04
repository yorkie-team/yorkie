/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

// Package binary provides functions to read and write binary data in a specific format.
// It avoids reflection and uses fixed-size byte slices for better performance than encoding/binary.
package binary

import (
	"bytes"
	"fmt"
)

// WriteInt64 writes an int64 value to the buffer in big-endian format.
func WriteInt64(buffer *bytes.Buffer, value int64) error {
	data := make([]byte, 8)
	for i := range 8 {
		data[i] = byte(value >> (56 - i*8))
	}

	if _, err := buffer.Write(data); err != nil {
		return fmt.Errorf("write int64: %w", err)
	}

	return nil
}

// ReadInt64 reads an int64 value from the buffer in big-endian format.
func ReadInt64(buffer *bytes.Reader) (int64, error) {
	data := make([]byte, 8)
	if _, err := buffer.Read(data); err != nil {
		return 0, fmt.Errorf("read int64: %w", err)
	}

	var value int64
	for i := range 8 {
		value = (value << 8) | int64(data[i])
	}
	return value, nil
}

// WriteUint32 writes a uint32 value to the buffer in big-endian format.
func WriteUint32(buffer *bytes.Buffer, value uint32) error {
	data := make([]byte, 4)
	data[0] = byte(value >> 24)
	data[1] = byte(value >> 16)
	data[2] = byte(value >> 8)
	data[3] = byte(value)

	if _, err := buffer.Write(data); err != nil {
		return fmt.Errorf("write uint32: %w", err)
	}

	return nil
}

// ReadUint32 reads a uint32 value from the buffer in big-endian format.
func ReadUint32(buffer *bytes.Reader) (uint32, error) {
	data := make([]byte, 4)
	if _, err := buffer.Read(data); err != nil {
		return 0, fmt.Errorf("read uint32: %w", err)
	}

	value := uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	return value, nil
}
