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

// Package conformance replays the cross-SDK Tree convergence scripts
// described in README.md. The JS SDK runs the same scripts and must produce
// the same records, byte for byte.
package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// Script is one conformance script: a starting paragraph and the steps the
// replicas take from it.
type Script struct {
	V        int               `json:"v"`
	ID       string            `json:"id"`
	Issues   []string          `json:"issues,omitempty"`
	Status   map[string]string `json:"status,omitempty"`
	Note     string            `json:"note,omitempty"`
	Replicas int               `json:"replicas"`
	Init     string            `json:"init"`
	Steps    []Step            `json:"steps"`
}

// Step is one step of a script, taken by replica R.
type Step struct {
	R  int    `json:"r"`
	Op string `json:"op"`

	// edit, style, removeStyle
	From  int               `json:"from,omitempty"`
	To    int               `json:"to,omitempty"`
	Text  string            `json:"text,omitempty"`
	Split int               `json:"split,omitempty"`
	Attrs map[string]string `json:"attrs,omitempty"`
	Keys  []string          `json:"keys,omitempty"`

	// gen
	K   string  `json:"k,omitempty"`
	F   float64 `json:"f,omitempty"`
	G   float64 `json:"g,omitempty"`
	H   float64 `json:"h,omitempty"`
	Val string  `json:"v,omitempty"`
}

// statuses are the ledger values a corpus script may record per SDK, each
// with the only result it accepts.
var statuses = map[string]string{
	"pass":          ResultPass,
	"known-diverge": ResultDiverged,
	"known-error":   ResultError,
	"known-fail":    ResultFail,
}

// ops are the step ops of format v1.
var ops = map[string]bool{
	"edit": true, "style": true, "removeStyle": true, "undo": true, "redo": true,
	"pull": true, "sync": true, "snapshot": true, "gen": true,
}

// genKinds are the generator events a `gen` step may carry.
var genKinds = map[string]bool{
	"ins": true, "split": true, "del": true, "merge": true, "style": true,
}

// LoadScript reads and validates a script file. Unknown fields, ops,
// generator kinds and ledger values are errors, so a typo fails loudly
// instead of silently replaying something else.
func LoadScript(path string) (*Script, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- test fixture path
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s Script
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("parse %s: trailing data after the script", path)
	}
	if s.V != 1 {
		return nil, fmt.Errorf("%s: unsupported version %d", path, s.V)
	}
	if s.Replicas < 1 {
		return nil, fmt.Errorf("%s: replicas must be at least 1", path)
	}
	for sdk, status := range s.Status {
		if _, ok := statuses[status]; !ok {
			return nil, fmt.Errorf("%s: status.%s: unknown value %q", path, sdk, status)
		}
	}
	for i, st := range s.Steps {
		if st.R < 0 || st.R >= s.Replicas {
			return nil, fmt.Errorf("%s: step %d: replica %d out of range", path, i, st.R)
		}
		if !ops[st.Op] {
			return nil, fmt.Errorf("%s: step %d: unknown op %q", path, i, st.Op)
		}
		if st.Op == "gen" && !genKinds[st.K] {
			return nil, fmt.Errorf("%s: step %d: unknown gen kind %q", path, i, st.K)
		}
	}
	return &s, nil
}
