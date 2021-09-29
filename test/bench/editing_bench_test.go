//go:build bench

/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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
	"encoding/json"
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
)

// editTrace editing trace of Automerge
type editTrace struct {
	Edits     [][]interface{} `json:"edits"`
	FinalText string          `json:"finalText"`
}

// read from editing-trace.json file and unmarshal to editTrace
func readEditingTraceFromFile(b *testing.B) (*editTrace, error) {
	var trace editTrace

	file, err := os.Open("./editing-trace.json")
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err = file.Close(); err != nil {
			b.Fatal(err)
		}
	}()

	byteValue, err := ioutil.ReadAll(file)
	if err != nil {
		b.Fatal(err)
	}

	if err = json.Unmarshal(byteValue, &trace); err != nil {
		b.Fatal(err)
	}

	return &trace, err
}

func BenchmarkTextEditing(b *testing.B) {
	b.StopTimer()

	editingTrace, _ := readEditingTraceFromFile(b)

	b.StartTimer()

	doc := document.New("c1", "d1")
	err := doc.Update(func(root *proxy.ObjectProxy) error {
		root.SetNewText("text")
		return nil
	})

	err = doc.Update(func(root *proxy.ObjectProxy) error {
		text := root.GetText("text")

		for i, edit := range editingTrace.Edits {
			// for logging
			if i%1000 == 0 {
				b.Log("processing...", i)
			}

			cursor := int(edit[0].(float64))
			mode := int(edit[1].(float64))

			if mode == 0 {
				// insertion
				value := edit[2].(string)
				text.Edit(cursor, cursor, value)

			} else if mode == 1 {
				// deletion
				text.Edit(cursor, cursor+1, "")
			}
		}
		return nil
	})

	b.StopTimer()
	assert.NoError(b, err)
	assert.Equal(b, `{"text":"`+editingTrace.FinalText+`"}`, doc.Marshal())
}
