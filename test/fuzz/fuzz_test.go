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
	"github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/test/helper"
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

func FuzzValueFromBytes(f *testing.F) {
	f.Add(byte(crdt.Null), []byte{})
	f.Add(byte(crdt.Boolean), []byte{1})
	f.Add(byte(crdt.Integer), []byte{0, 0, 0, 0})
	f.Add(byte(crdt.Long), []byte{0, 0, 0, 0, 0, 0, 0, 0})
	f.Add(byte(crdt.Double), []byte{0, 0, 0, 0, 0, 0, 0, 0})
	f.Add(byte(crdt.String), []byte("yorkie"))
	f.Add(byte(crdt.Bytes), []byte("yorkie"))
	f.Add(byte(crdt.Date), []byte{0, 0, 0, 0, 0, 0, 0, 0})

	f.Fuzz(func(t *testing.T, rawValueType byte, value []byte) {
		_, _ = crdt.ValueFromBytes(crdt.ValueType(rawValueType), value)
	})
}

func FuzzBytesToObject(f *testing.F) {
	if seed, err := converter.ObjectToBytes(
		crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket),
	); err == nil {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = converter.BytesToObject(data)
	})
}

func FuzzBytesToArray(f *testing.F) {
	if seed, err := converter.ArrayToBytes(
		crdt.NewArray(crdt.NewRGATreeList(), time.InitialTicket),
	); err == nil {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = converter.BytesToArray(data)
	})
}

func FuzzBytesToTree(f *testing.F) {
	root := helper.BuildTreeNode(&json.TreeNode{
		Type: "r",
		Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "hello"}}},
		},
	})
	if seed, err := converter.TreeToBytes(crdt.NewTree(root, time.InitialTicket)); err == nil {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = converter.BytesToTree(data)
	})
}

func FuzzBytesToSnapshot(f *testing.F) {
	if seed, err := converter.SnapshotToBytes(
		crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket),
		map[string]presence.Data{},
	); err == nil {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _, _ = converter.BytesToSnapshot(data)
	})
}

func FuzzVersionVectorFromBytes(f *testing.F) {
	if seed, err := time.NewVersionVector().Bytes(); err == nil {
		f.Add(seed)
	}
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = time.VersionVectorFromBytes(data)
	})
}

func FuzzActorIDFromHex(f *testing.F) {
	f.Add("")
	f.Add("000000000000000000000000")
	f.Add("not-a-hex-string")

	f.Fuzz(func(t *testing.T, str string) {
		_, _ = time.ActorIDFromHex(str)
	})
}

func FuzzParseKeyPath(f *testing.F) {
	f.Add("a/b/c")
	f.Add("")
	f.Add("a//b")

	f.Fuzz(func(t *testing.T, str string) {
		_, _ = channel.ParseKeyPath(key.Key(str))
	})
}

func FuzzYSONUnmarshal(f *testing.F) {
	// typeSelector chooses which YSON Element type the data is decoded into.
	f.Add(byte(0), `{"key":"value"}`)                    // Object
	f.Add(byte(1), `[1,2,3]`)                            // Array
	f.Add(byte(2), `{"value":{"type":"root"}}`)          // Tree
	f.Add(byte(3), `{"value":[{"val":"hello"}]}`)        // Text
	f.Add(byte(4), `{"value":{"type":"Int","value":1}}`) // Counter
	f.Add(byte(0), "")
	f.Add(byte(0), `{"n":Int(1),"c":Counter(Int(1))}`) // YSON constructor syntax

	f.Fuzz(func(t *testing.T, typeSelector byte, data string) {
		var elem yson.Element
		switch typeSelector % 5 {
		case 0:
			elem = &yson.Object{}
		case 1:
			elem = &yson.Array{}
		case 2:
			elem = &yson.Tree{}
		case 3:
			elem = &yson.Text{}
		case 4:
			elem = &yson.Counter{}
		}
		_ = yson.Unmarshal(data, elem)
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
