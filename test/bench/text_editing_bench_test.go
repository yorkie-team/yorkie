//go:build bench

/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package bench

import (
	gojson "encoding/json"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

func BenchmarkTextEditing(b *testing.B) {
	b.StopTimer()

	editingTrace, err := readEditingTraceFromFile(b)
	if err != nil {
		b.Fatal(err)
	}

	b.StartTimer()

	doc := document.New("d1")
	err = doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("text")
		return nil
	})

	for _, edit := range editingTrace.Edits {
		cursor := int(edit[0].(float64))
		mode := int(edit[1].(float64))

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.GetText("text")
			if mode == 0 {
				value := edit[2].(string)
				text.Edit(cursor, cursor, value)
			} else if mode == 1 {
				// deletion
				text.Edit(cursor, cursor+1, "")
			}
			return nil
		})
		assert.NoError(b, err)
	}
	b.StopTimer()

	assert.Equal(
		b,
		editingTrace.FinalText,
		doc.Root().GetText("text").String(),
	)
}

type editTrace struct {
	Edits     [][]interface{} `json:"edits"`
	FinalText string          `json:"finalText"`
}

// readEditingTraceFromFile reads trace from editing-trace.json.
func readEditingTraceFromFile(b *testing.B) (*editTrace, error) {
	var trace editTrace

	file, err := os.Open("./editing-trace.json")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err = file.Close(); err != nil {
			b.Fatal(err)
		}
	}()

	byteValue, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	if err = gojson.Unmarshal(byteValue, &trace); err != nil {
		return nil, err
	}

	return &trace, err
}
