//go:build integration

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

package packs_test

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
)

// pbConverter is satisfied by *packs.ServerPack.
type pbConverter interface {
	ToPBChangePack() (*api.ChangePack, error)
}

// wireSize holds the size of a ChangePack after protobuf serialization and
// after gzip compression of the serialized bytes.
type wireSize struct{ Proto, Gzip int }

// measureWire converts the given pack to a protobuf ChangePack, serializes it
// and compresses the result with gzip at the default compression level. It
// returns the size after each of the last two steps.
func measureWire(p pbConverter) (wireSize, error) {
	pb, err := p.ToPBChangePack()
	if err != nil {
		return wireSize{}, err
	}

	raw, err := proto.Marshal(pb)
	if err != nil {
		return wireSize{}, fmt.Errorf("marshal change pack: %w", err)
	}

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return wireSize{}, fmt.Errorf("gzip write: %w", err)
	}
	// Close flushes the remaining data; without it the size is incomplete.
	if err := zw.Close(); err != nil {
		return wireSize{}, fmt.Errorf("gzip close: %w", err)
	}

	return wireSize{Proto: len(raw), Gzip: buf.Len()}, nil
}

// reportWire attaches the precomputed wire size to the benchmark result as
// custom metrics named <prefix>proto-B and <prefix>gzip-B. Call it once after
// the b.N loop, since the values do not depend on the iteration count.
func reportWire(b *testing.B, prefix string, ws wireSize) {
	b.Helper()
	b.ReportMetric(float64(ws.Proto), prefix+"proto-B")
	b.ReportMetric(float64(ws.Gzip), prefix+"gzip-B")
}

type fakePack struct{ pb *api.ChangePack }

func (f fakePack) ToPBChangePack() (*api.ChangePack, error) { return f.pb, nil }

// TestMeasureWireSanity verifies the wire size pipeline without a database,
// using highly repetitive data that gzip must shrink.
func TestMeasureWireSanity(t *testing.T) {
	snap := bytes.Repeat([]byte("yorkie"), 1000)
	ws, err := measureWire(fakePack{&api.ChangePack{DocumentKey: "sanity", Snapshot: snap}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("snapshot=%dB proto=%dB gzip=%dB", len(snap), ws.Proto, ws.Gzip)

	if ws.Proto < len(snap) {
		t.Fatalf("proto must contain the snapshot: proto=%d snap=%d", ws.Proto, len(snap))
	}
	if ws.Gzip >= ws.Proto {
		t.Fatalf("gzip should shrink repetitive data: proto=%d gzip=%d", ws.Proto, ws.Gzip)
	}
}
