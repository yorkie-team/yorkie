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
	"io"
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

// sdks are the keys a script's status may carry.
var sdks = map[string]bool{"go": true, "js": true}

// opStyle is the op that styles a range, not to be confused with the `style`
// generator kind.
const opStyle = "style"

// opFields lists, for each step op of format v1, the fields a step may carry
// besides `r` and `op`, each marked true when it is required. A field valid
// for another op is an error, so a misplaced field fails loudly too.
var opFields = map[string]map[string]bool{
	"edit":        {"from": true, "to": true, "text": false, "split": false},
	opStyle:       {"from": true, "to": true, "attrs": true},
	"removeStyle": {"from": true, "to": true, "keys": true},
	"undo":        {},
	"redo":        {},
	"pull":        {},
	"sync":        {},
	"snapshot":    {},
	"gen":         {"k": true, "f": true, "g": true, "h": true, "v": true},
}

// genKinds are the generator events a `gen` step may carry.
var genKinds = map[string]bool{
	"ins": true, "split": true, "del": true, "merge": true, "style": true,
}

// LoadScript reads and validates a script file. Unknown fields, ops,
// generator kinds and ledger values are errors, and so are steps missing a
// field their op needs or carrying one it does not take, so a typo fails
// loudly instead of silently replaying something else.
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
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("parse %s: trailing data after the script", path)
	}

	// The typed decode cannot tell a missing or null field from a zero one,
	// so the steps are checked once more as raw fields.
	var fields struct {
		Steps []map[string]json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if s.V != 1 {
		return nil, fmt.Errorf("%s: unsupported version %d", path, s.V)
	}
	if s.Replicas < 1 {
		return nil, fmt.Errorf("%s: replicas must be at least 1", path)
	}
	for sdk, status := range s.Status {
		if !sdks[sdk] {
			return nil, fmt.Errorf("%s: status: unknown sdk %q", path, sdk)
		}
		if _, ok := statuses[status]; !ok {
			return nil, fmt.Errorf("%s: status.%s: unknown value %q", path, sdk, status)
		}
	}
	for i, st := range s.Steps {
		if err := checkStep(st, fields.Steps[i], s.Replicas); err != nil {
			return nil, fmt.Errorf("%s: step %d: %w", path, i, err)
		}
	}
	return &s, nil
}

// checkStep validates one step against its raw fields.
func checkStep(st Step, raw map[string]json.RawMessage, replicas int) error {
	for _, k := range []string{"r", "op"} {
		if v, ok := raw[k]; !ok || isNull(v) {
			return fmt.Errorf("missing %q", k)
		}
	}
	if st.R < 0 || st.R >= replicas {
		return fmt.Errorf("replica %d out of range", st.R)
	}
	allowed, ok := opFields[st.Op]
	if !ok {
		return fmt.Errorf("unknown op %q", st.Op)
	}
	for k, v := range raw {
		if k == "r" || k == "op" {
			continue
		}
		if _, ok := allowed[k]; !ok {
			return fmt.Errorf("field %q is not valid for op %q", k, st.Op)
		}
		if isNull(v) {
			return fmt.Errorf("field %q is null", k)
		}
	}
	for k, required := range allowed {
		if _, ok := raw[k]; required && !ok {
			return fmt.Errorf("op %q needs %q", st.Op, k)
		}
	}

	switch st.Op {
	case opStyle:
		var attrs map[string]*string
		if err := json.Unmarshal(raw["attrs"], &attrs); err != nil {
			return fmt.Errorf("attrs: %w", err)
		}
		if len(attrs) == 0 {
			return fmt.Errorf("attrs is empty")
		}
		for k, v := range attrs {
			if v == nil {
				return fmt.Errorf("attrs.%s is not a string", k)
			}
		}
	case "removeStyle":
		var keys []*string
		if err := json.Unmarshal(raw["keys"], &keys); err != nil {
			return fmt.Errorf("keys: %w", err)
		}
		if len(keys) == 0 {
			return fmt.Errorf("keys is empty")
		}
		for j, v := range keys {
			if v == nil {
				return fmt.Errorf("keys[%d] is not a string", j)
			}
		}
	case "gen":
		if !genKinds[st.K] {
			return fmt.Errorf("unknown gen kind %q", st.K)
		}
		for k, v := range map[string]float64{"f": st.F, "g": st.G, "h": st.H} {
			if v < 0 || v >= 1 {
				return fmt.Errorf("%s %v is not in [0, 1)", k, v)
			}
		}
	}
	return nil
}

func isNull(v json.RawMessage) bool {
	return string(bytes.TrimSpace(v)) == "null"
}
