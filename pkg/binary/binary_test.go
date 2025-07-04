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

package binary

import (
	"bytes"
	"testing"
)

func TestInt64ReadWrite(t *testing.T) {
	tests := []int64{
		0, 1, -1, 42, -42, 9223372036854775807, -9223372036854775808,
	}

	for _, test := range tests {
		buffer := bytes.Buffer{}

		// Write
		err := WriteInt64(&buffer, test)
		if err != nil {
			t.Fatalf("WriteInt64 failed: %v", err)
		}

		// Read
		reader := bytes.NewReader(buffer.Bytes())
		result, err := ReadInt64(reader)
		if err != nil {
			t.Fatalf("ReadInt64 failed: %v", err)
		}

		if result != test {
			t.Errorf("Expected %d, got %d", test, result)
		}
	}
}

func TestUint32ReadWrite(t *testing.T) {
	tests := []uint32{
		0, 1, 42, 255, 65535, 4294967295,
	}

	for _, test := range tests {
		buffer := bytes.Buffer{}

		// Write
		err := WriteUint32(&buffer, test)
		if err != nil {
			t.Fatalf("WriteUint32 failed: %v", err)
		}

		// Read
		reader := bytes.NewReader(buffer.Bytes())
		result, err := ReadUint32(reader)
		if err != nil {
			t.Fatalf("ReadUint32 failed: %v", err)
		}

		if result != test {
			t.Errorf("Expected %d, got %d", test, result)
		}
	}
}

func TestBigEndianEncoding(t *testing.T) {
	// Test that we're using big-endian encoding
	buffer := bytes.Buffer{}

	// Write 0x12345678 (305419896 in decimal)
	err := WriteUint32(&buffer, 0x12345678)
	if err != nil {
		t.Fatalf("WriteUint32 failed: %v", err)
	}

	data := buffer.Bytes()
	expected := []byte{0x12, 0x34, 0x56, 0x78}

	if !bytes.Equal(data, expected) {
		t.Errorf("Expected %v, got %v", expected, data)
	}
}
