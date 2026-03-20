/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package database

import (
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
)

const (
	// SnapshotFormatZstd is the format header byte for zstd-compressed snapshots.
	// Existing protobuf snapshots never start with 0x01 (protobuf field 1 with
	// wire type 2 encodes as 0x0A), so this is safe for detection.
	SnapshotFormatZstd byte = 0x01

	// SnapshotBodyThreshold is the size threshold (in bytes) above which
	// compressed snapshot data is stored in a separate body collection.
	// 12MB leaves headroom below MongoDB's 16MB BSON limit for metadata.
	SnapshotBodyThreshold = 12 * 1024 * 1024
)

var (
	encoderOnce sync.Once
	decoderOnce sync.Once
	encoder     *zstd.Encoder
	decoder     *zstd.Decoder
	encoderErr  error
	decoderErr  error
)

func getEncoder() (*zstd.Encoder, error) {
	encoderOnce.Do(func() {
		encoder, encoderErr = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	})
	return encoder, encoderErr
}

func getDecoder() (*zstd.Decoder, error) {
	decoderOnce.Do(func() {
		decoder, decoderErr = zstd.NewReader(nil)
	})
	return decoder, decoderErr
}

// CompressSnapshot compresses the given snapshot bytes using zstd and prepends
// a format header byte (0x01).
func CompressSnapshot(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	enc, err := getEncoder()
	if err != nil {
		return nil, fmt.Errorf("get zstd encoder: %w", err)
	}

	compressed := enc.EncodeAll(data, nil)

	// Prepend format header
	result := make([]byte, 1+len(compressed))
	result[0] = SnapshotFormatZstd
	copy(result[1:], compressed)

	return result, nil
}

// DecompressSnapshot decompresses the given snapshot bytes. It detects the
// format from the first byte:
//   - 0x01: zstd-compressed, decompress after stripping header
//   - anything else: raw protobuf (backward compatible), returned as-is
func DecompressSnapshot(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	if data[0] != SnapshotFormatZstd {
		return data, nil
	}

	dec, err := getDecoder()
	if err != nil {
		return nil, fmt.Errorf("get zstd decoder: %w", err)
	}

	decompressed, err := dec.DecodeAll(data[1:], nil)
	if err != nil {
		return nil, fmt.Errorf("decompress snapshot: %w", err)
	}

	return decompressed, nil
}
