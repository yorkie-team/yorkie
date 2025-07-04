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

// Package innerpresence provides the implementation of Presence.
// If the client is watching a document, the presence is shared with
// all other clients watching the same document.
package innerpresence

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/yorkie-team/yorkie/pkg/binary"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Constants for presence change encoding.
const (
	ChangeTypePutByte   = 1
	ChangeTypeClearByte = 2
)

// Pools for reusing objects to reduce GC pressure.
var (
	bufferPool = sync.Pool{
		New: func() interface{} {
			return &bytes.Buffer{}
		},
	}

	byteSlicePool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 0, 1024) // Start with 1KB capacity
		},
	}
)

// Constants for pool management.
const (
	maxByteSliceSize = 64 * 1024 // 64KB max size for pooled byte slices
)

// getByteSlice gets a byte slice from the pool with the specified size.
// If the required size is larger than maxByteSliceSize, it creates a new slice.
func getByteSlice(size int) []byte {
	if size > maxByteSliceSize {
		return make([]byte, size)
	}

	slice := byteSlicePool.Get().([]byte)
	if cap(slice) < size {
		return make([]byte, size)
	}
	return slice[:size]
}

// putByteSlice returns a byte slice to the pool.
// Only slices smaller than maxByteSliceSize are returned to the pool.
func putByteSlice(slice []byte) {
	if cap(slice) <= maxByteSliceSize {
		byteSlicePool.Put(slice[:0]) // Reset length but keep capacity
	}
}

// ChangeType represents the type of presence change.
type ChangeType string

const (
	// Put represents the presence is put.
	Put ChangeType = "put"

	// Clear represents the presence is cleared.
	Clear ChangeType = "clear"
)

// Change represents the change of presence.
type Change struct {
	ChangeType ChangeType
	Presence   Presence
}

// Execute applies the change to the given presences map.
func (c *Change) Execute(actorID time.ActorID, presences *Map) {
	if c.ChangeType == Clear {
		presences.Delete(actorID.String())
	} else {
		presences.Store(actorID.String(), c.Presence)
	}
}

// Bytes encodes the presence change into bytes array using custom binary format.
// This is faster than protobuf encoding for simple presence changes.
func (c *Change) Bytes() ([]byte, error) {
	if c == nil {
		return nil, nil
	}

	// Get buffer from pool and reset it
	buffer := bufferPool.Get().(*bytes.Buffer)
	buffer.Reset()
	defer bufferPool.Put(buffer)

	// Write change type
	if c.ChangeType == Put {
		if err := buffer.WriteByte(ChangeTypePutByte); err != nil {
			return nil, fmt.Errorf("write change type: %w", err)
		}

		// Write presence data only for Put type
		if c.Presence != nil {
			// Write presence count
			if err := binary.WriteUint32(buffer, uint32(len(c.Presence))); err != nil {
				return nil, fmt.Errorf("write presence count: %w", err)
			}

			// Write each key-value pair
			for k, v := range c.Presence {
				// Write key length and key
				if err := binary.WriteUint32(buffer, uint32(len(k))); err != nil {
					return nil, fmt.Errorf("write key length: %w", err)
				}
				if _, err := buffer.WriteString(k); err != nil {
					return nil, fmt.Errorf("write key: %w", err)
				}

				// Write value length and value
				if err := binary.WriteUint32(buffer, uint32(len(v))); err != nil {
					return nil, fmt.Errorf("write value length: %w", err)
				}
				if _, err := buffer.WriteString(v); err != nil {
					return nil, fmt.Errorf("write value: %w", err)
				}
			}
		} else {
			// Empty presence
			if err := binary.WriteUint32(buffer, 0); err != nil {
				return nil, fmt.Errorf("write empty presence count: %w", err)
			}
		}
	} else {
		if err := buffer.WriteByte(ChangeTypeClearByte); err != nil {
			return nil, fmt.Errorf("write change type: %w", err)
		}
	}

	// Copy buffer contents to return slice
	result := make([]byte, buffer.Len())
	copy(result, buffer.Bytes())
	return result, nil
}

// ChangeFromBytes creates a new Change from the given bytes array using custom binary format.
// This is faster than protobuf decoding for simple presence changes.
func ChangeFromBytes(data []byte) (*Change, error) {
	if len(data) == 0 {
		return nil, nil
	}

	buffer := bytes.NewReader(data)
	change := &Change{}

	// Read change type
	changeType, err := buffer.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("read change type: %w", err)
	}

	switch changeType {
	case ChangeTypePutByte:
		// Read presence count
		presenceCount, err := binary.ReadUint32(buffer)
		if err != nil {
			return nil, fmt.Errorf("read presence count: %w", err)
		}

		presence := make(map[string]string, presenceCount)

		for range presenceCount {
			// Read key length
			keyLen, err := binary.ReadUint32(buffer)
			if err != nil {
				return nil, fmt.Errorf("read key length: %w", err)
			}

			// Get byte slice from pool and ensure it's large enough
			keyBytes := getByteSlice(int(keyLen))

			// Read key
			if _, err := buffer.Read(keyBytes); err != nil {
				putByteSlice(keyBytes)
				return nil, fmt.Errorf("read key: %w", err)
			}
			key := string(keyBytes)
			putByteSlice(keyBytes)

			// Read value length
			valueLen, err := binary.ReadUint32(buffer)
			if err != nil {
				return nil, fmt.Errorf("read value length: %w", err)
			}

			// Get byte slice from pool and ensure it's large enough
			valueBytes := getByteSlice(int(valueLen))

			// Read value
			if _, err := buffer.Read(valueBytes); err != nil {
				putByteSlice(valueBytes)
				return nil, fmt.Errorf("read value: %w", err)
			}
			value := string(valueBytes)
			putByteSlice(valueBytes)

			presence[key] = value
		}

		change.ChangeType = Put
		change.Presence = presence

	case ChangeTypeClearByte:
		change.ChangeType = Clear
		change.Presence = nil

	default:
		return nil, fmt.Errorf("unknown change type: %d", changeType)
	}

	return change, nil
}
