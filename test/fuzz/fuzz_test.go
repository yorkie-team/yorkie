//go:build fuzz

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

// Package fuzz provides fuzz tests for decoders of client-controlled input.
package fuzz

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
)

func FuzzFromChangePack(f *testing.F) {
	addProtoSeeds(f,
		&api.ChangePack{Checkpoint: &api.Checkpoint{}},
	)

	f.Fuzz(func(t *testing.T, data []byte) {
		pbPack := &api.ChangePack{}
		if err := proto.Unmarshal(data, pbPack); err != nil {
			return
		}
		_, _ = converter.FromChangePack(pbPack)
	})
}

func FuzzCounterValueFromBytes(f *testing.F) {
	f.Add(byte(crdt.IntegerCnt), []byte{0, 0, 0, 0})
	f.Add(byte(crdt.LongCnt), []byte{0, 0, 0, 0, 0, 0, 0, 0})
	f.Add(byte(crdt.IntegerDedupCnt), []byte{0, 0, 0, 0})

	f.Fuzz(func(t *testing.T, rawCounterType byte, value []byte) {
		counterType := crdt.CounterType(rawCounterType % byte(crdt.IntegerDedupCnt+1))
		_, _ = crdt.CounterValueFromBytes(counterType, value)
	})
}

func addProtoSeeds(f *testing.F, messages ...proto.Message) {
	f.Helper()
	for _, message := range messages {
		data, err := proto.Marshal(message)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
}
