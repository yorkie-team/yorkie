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
	"sort"
	"strings"

	yjson "github.com/yorkie-team/yorkie/pkg/document/json"
)

// Delivery modes of a script. `exchange` names both a delivery mode and the
// op only that mode takes: DeliveryExchange is the value of `delivery`,
// opExchange the value of a step's `op`.
const (
	DeliveryLog      = "log"
	DeliveryExchange = "exchange"
)

// Script is one conformance script: a starting tree and the steps the
// replicas take from it.
type Script struct {
	V        int               `json:"v"`
	ID       string            `json:"id"`
	Issues   []string          `json:"issues,omitempty"`
	Status   map[string]string `json:"status,omitempty"`
	Note     string            `json:"note,omitempty"`
	Replicas int               `json:"replicas"`
	Delivery string            `json:"delivery,omitempty"`
	Init     Init              `json:"init"`
	Steps    []Step            `json:"steps"`
}

// Init is the tree a script starts from: either the text of a single
// paragraph or, from format v2, a whole tree.
type Init struct {
	Text string
	Node *yjson.TreeNode
}

// UnmarshalJSON reads a string shorthand or a tree node object.
func (in *Init) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) > 0 && b[0] == '"':
		if err := json.Unmarshal(b, &in.Text); err != nil {
			return fmt.Errorf("init: %w", err)
		}
		return nil
	case len(b) > 0 && b[0] == '{':
		node, err := parseNode(b)
		if err != nil {
			return fmt.Errorf("init: %w", err)
		}
		if node.Type == textType {
			return fmt.Errorf("init: the root must be an element")
		}
		in.Node = &node
		return nil
	}
	return fmt.Errorf("init must be a string or a tree node")
}

// Step is one step of a script, taken by replica R. An op inside an
// `update` is a Step too, without R.
type Step struct {
	R  int    `json:"r"`
	Op string `json:"op"`

	// edit, style, removeStyle
	From     int               `json:"from,omitempty"`
	To       int               `json:"to,omitempty"`
	Text     string            `json:"text,omitempty"`
	Contents []json.RawMessage `json:"contents,omitempty"`
	Split    int               `json:"split,omitempty"`
	Attrs    map[string]string `json:"attrs,omitempty"`
	Keys     []string          `json:"keys,omitempty"`

	// update
	Ops []Step `json:"ops,omitempty"`

	// exchange
	Orders [][]int `json:"orders,omitempty"`

	// expect
	XML   []string `json:"xml,omitempty"`
	Label string   `json:"label,omitempty"`

	// gen
	K   string  `json:"k,omitempty"`
	F   float64 `json:"f,omitempty"`
	G   float64 `json:"g,omitempty"`
	H   float64 `json:"h,omitempty"`
	Val string  `json:"v,omitempty"`

	// nodes are the parsed Contents.
	nodes []*yjson.TreeNode
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

// Step ops. opStyle is the op that styles a range, not to be confused with
// the `style` generator kind.
const (
	opEdit        = "edit"
	opStyle       = "style"
	opRemoveStyle = "removeStyle"
	opUpdate      = "update"
	opUndo        = "undo"
	opRedo        = "redo"
	opPull        = "pull"
	opSync        = "sync"
	opExchange    = "exchange"
	opSnapshot    = "snapshot"
	opExpect      = "expect"
	opGen         = "gen"
)

// textType is the type of a text node.
const textType = "text"

// opFields lists, for each step op, the fields a step may carry besides `r`
// and `op`, each marked true when it is required. A field valid for another
// op is an error, so a misplaced field fails loudly too.
var opFields = map[string]map[string]bool{
	opEdit:        {"from": true, "to": true, "text": false, "contents": false, "split": false},
	opStyle:       {"from": true, "to": true, "attrs": true},
	opRemoveStyle: {"from": true, "to": true, "keys": true},
	opUpdate:      {"ops": true},
	opUndo:        {},
	opRedo:        {},
	opPull:        {},
	opSync:        {},
	opExchange:    {"orders": false},
	opSnapshot:    {},
	opExpect:      {"xml": true, "label": false},
	opGen:         {"k": true, "f": true, "g": true, "h": true, "v": true},
}

// v2Ops are the ops, and v2Fields the fields, that format v1 does not have.
var (
	v2Ops    = map[string]bool{opUpdate: true, opExchange: true, opExpect: true}
	v2Fields = map[string]bool{"contents": true}
)

// replicaless ops take no `r`; nestable ops are the ones an `update` holds.
var (
	replicaless = map[string]bool{opExchange: true, opExpect: true}
	nestable    = map[string]bool{opEdit: true, opStyle: true, opRemoveStyle: true}
)

// deliveryOps are the ops valid in one delivery mode only.
var deliveryOps = map[string]string{
	opPull: DeliveryLog, opSync: DeliveryLog, opExchange: DeliveryExchange,
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
	s, err := ParseScript(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// ParseScript parses and validates a script.
func ParseScript(raw []byte) (*Script, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s Script
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("parse: trailing data after the script")
	}

	// The typed decode cannot tell a missing or null field from a zero one,
	// so the script and its steps are checked once more as raw fields.
	var fields struct {
		Delivery json.RawMessage              `json:"delivery"`
		Steps    []map[string]json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	if s.V != 1 && s.V != 2 {
		return nil, fmt.Errorf("unsupported version %d", s.V)
	}
	if s.Replicas < 1 {
		return nil, fmt.Errorf("replicas must be at least 1")
	}
	for sdk, status := range s.Status {
		if !sdks[sdk] {
			return nil, fmt.Errorf("status: unknown sdk %q", sdk)
		}
		if _, ok := statuses[status]; !ok {
			return nil, fmt.Errorf("status.%s: unknown value %q", sdk, status)
		}
	}
	if s.V == 1 && s.Init.Node != nil {
		return nil, fmt.Errorf("init: a tree node needs v 2")
	}
	if fields.Delivery != nil {
		if s.V == 1 {
			return nil, fmt.Errorf("delivery needs v 2")
		}
		if isNull(fields.Delivery) {
			return nil, fmt.Errorf("field %q is null", "delivery")
		}
		if s.Delivery != DeliveryLog && s.Delivery != DeliveryExchange {
			return nil, fmt.Errorf("unknown delivery %q", s.Delivery)
		}
	}
	if s.Delivery == "" {
		s.Delivery = DeliveryLog
	}
	for i := range s.Steps {
		if err := checkStep(&s, &s.Steps[i], fields.Steps[i], false); err != nil {
			return nil, fmt.Errorf("step %d: %w", i, err)
		}
	}
	return &s, nil
}

// checkStep validates one step against its raw fields. A nested step is an
// op of an `update`.
func checkStep(s *Script, st *Step, raw map[string]json.RawMessage, nested bool) error {
	if v, ok := raw["op"]; !ok || isNull(v) {
		return fmt.Errorf("missing %q", "op")
	}
	allowed, ok := opFields[st.Op]
	if !ok {
		return fmt.Errorf("unknown op %q", st.Op)
	}
	if nested && !nestable[st.Op] {
		return fmt.Errorf("op %q is not valid in an update", st.Op)
	}
	if nested || replicaless[st.Op] {
		if _, ok := raw["r"]; ok {
			return fmt.Errorf("field %q is not valid for op %q", "r", st.Op)
		}
	} else {
		if v, ok := raw["r"]; !ok || isNull(v) {
			return fmt.Errorf("missing %q", "r")
		}
		if st.R < 0 || st.R >= s.Replicas {
			return fmt.Errorf("replica %d out of range", st.R)
		}
	}
	if s.V == 1 && v2Ops[st.Op] {
		return fmt.Errorf("op %q needs v 2", st.Op)
	}
	if mode, ok := deliveryOps[st.Op]; ok && mode != s.Delivery {
		return fmt.Errorf("op %q is not valid with delivery %q", st.Op, s.Delivery)
	}
	if st.Op == opGen {
		if span := spanGenKinds[st.K]; span && s.Init.Node == nil {
			return fmt.Errorf("gen kind %q needs a tree init", st.K)
		} else if !span && s.Init.Node != nil {
			return fmt.Errorf("op %q needs a string init", st.Op)
		}
	}
	for k, v := range raw {
		if k == "r" || k == "op" {
			continue
		}
		if _, ok := allowed[k]; !ok {
			return fmt.Errorf("field %q is not valid for op %q", k, st.Op)
		}
		if s.V == 1 && v2Fields[k] {
			return fmt.Errorf("field %q needs v 2", k)
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
	return checkValues(s, st, raw)
}

// checkValues validates the values of a step whose fields are in place.
func checkValues(s *Script, st *Step, raw map[string]json.RawMessage) error {
	switch st.Op {
	case opEdit:
		if _, ok := raw["contents"]; ok {
			return checkContents(st, raw)
		}
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
	case opRemoveStyle:
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
	case opUpdate:
		var ops []map[string]json.RawMessage
		if err := json.Unmarshal(raw["ops"], &ops); err != nil {
			return fmt.Errorf("ops: %w", err)
		}
		if len(ops) == 0 {
			return fmt.Errorf("ops is empty")
		}
		for j := range st.Ops {
			if ops[j] == nil {
				return fmt.Errorf("ops[%d] is null", j)
			}
			if err := checkStep(s, &st.Ops[j], ops[j], true); err != nil {
				return fmt.Errorf("ops[%d]: %w", j, err)
			}
		}
	case opExchange:
		if _, ok := raw["orders"]; ok {
			if err := checkOrders(raw["orders"], s.Replicas); err != nil {
				return fmt.Errorf("orders: %w", err)
			}
		}
	case opExpect:
		return checkXML(raw["xml"], s.Replicas)
	case opGen:
		if !genKinds[st.K] && !spanGenKinds[st.K] {
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

// checkContents parses the nodes of an edit's contents.
func checkContents(st *Step, raw map[string]json.RawMessage) error {
	if _, ok := raw["text"]; ok {
		return fmt.Errorf("text and contents are exclusive")
	}
	if len(st.Contents) == 0 {
		return fmt.Errorf("contents is empty")
	}
	for j, c := range st.Contents {
		node, err := parseNode(c)
		if err != nil {
			return fmt.Errorf("contents[%d]: %w", j, err)
		}
		st.nodes = append(st.nodes, &node)
	}
	return nil
}

// checkXML checks that an expect step holds one string per replica.
func checkXML(raw json.RawMessage, replicas int) error {
	var xml []*string
	if err := json.Unmarshal(raw, &xml); err != nil {
		return fmt.Errorf("xml: %w", err)
	}
	if len(xml) != replicas {
		return fmt.Errorf("xml has %d entries for %d replicas", len(xml), replicas)
	}
	for j, v := range xml {
		if v == nil {
			return fmt.Errorf("xml[%d] is not a string", j)
		}
	}
	return nil
}

// checkOrders checks that orders holds, for each replica, the other
// replicas in some order, each exactly once.
func checkOrders(raw json.RawMessage, replicas int) error {
	var orders [][]*int
	if err := json.Unmarshal(raw, &orders); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if len(orders) != replicas {
		return fmt.Errorf("has %d entries for %d replicas", len(orders), replicas)
	}
	for i, order := range orders {
		if order == nil {
			return fmt.Errorf("[%d] is not an array", i)
		}
		seen := map[int]bool{}
		for _, j := range order {
			if j == nil || *j < 0 || *j >= replicas || *j == i || seen[*j] {
				return fmt.Errorf("[%d] is not a permutation of the other replicas", i)
			}
			seen[*j] = true
		}
		if len(seen) != replicas-1 {
			return fmt.Errorf("[%d] is not a permutation of the other replicas", i)
		}
	}
	return nil
}

// parseNode reads a tree node: `{"type":"text","value":…}` or an element
// `{"type":…,"children"?:[…],"attributes"?:{…}}`, with no other fields.
func parseNode(raw json.RawMessage) (yjson.TreeNode, error) {
	var fields map[string]json.RawMessage
	if isNull(raw) {
		return yjson.TreeNode{}, fmt.Errorf("node is null")
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return yjson.TreeNode{}, fmt.Errorf("node is not an object")
	}
	for k, v := range fields {
		if isNull(v) {
			return yjson.TreeNode{}, fmt.Errorf("field %q is null", k)
		}
	}
	var typ string
	if v, ok := fields["type"]; !ok || json.Unmarshal(v, &typ) != nil || typ == "" {
		return yjson.TreeNode{}, fmt.Errorf("node needs a non-empty string type")
	}
	node := yjson.TreeNode{Type: typ}
	if typ == textType {
		for k := range fields {
			if k != "type" && k != "value" {
				return yjson.TreeNode{}, fmt.Errorf("field %q is not valid for a text node", k)
			}
		}
		v, ok := fields["value"]
		if !ok || json.Unmarshal(v, &node.Value) != nil {
			return yjson.TreeNode{}, fmt.Errorf("text node needs a string value")
		}
		return node, nil
	}
	for k, v := range fields {
		switch k {
		case "type":
		case "children":
			var children []json.RawMessage
			if json.Unmarshal(v, &children) != nil {
				return yjson.TreeNode{}, fmt.Errorf("children is not an array")
			}
			for j, c := range children {
				child, err := parseNode(c)
				if err != nil {
					return yjson.TreeNode{}, fmt.Errorf("children[%d]: %w", j, err)
				}
				node.Children = append(node.Children, child)
			}
		case "attributes":
			var attrs map[string]*string
			if json.Unmarshal(v, &attrs) != nil {
				return yjson.TreeNode{}, fmt.Errorf("attributes is not an object of strings")
			}
			node.Attributes = map[string]string{}
			for ak, av := range attrs {
				if av == nil {
					return yjson.TreeNode{}, fmt.Errorf("attributes.%s is not a string", ak)
				}
				node.Attributes[ak] = *av
			}
		default:
			return yjson.TreeNode{}, fmt.Errorf("field %q is not valid for an element node", k)
		}
	}
	return node, nil
}

// nodeXML is the canonical XML of a script node, as step lines show it:
// `<type k="v" …>children</type>` with attributes sorted by key, and a text
// node as its value, nothing escaped.
func nodeXML(n *yjson.TreeNode) string {
	if n.Type == textType {
		return n.Value
	}
	keys := make([]string, 0, len(n.Attributes))
	for k := range n.Attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("<" + n.Type)
	for _, k := range keys {
		sb.WriteString(" " + k + `="` + n.Attributes[k] + `"`)
	}
	sb.WriteString(">")
	for i := range n.Children {
		sb.WriteString(nodeXML(&n.Children[i]))
	}
	sb.WriteString("</" + n.Type + ">")
	return sb.String()
}

func isNull(v json.RawMessage) bool {
	return string(bytes.TrimSpace(v)) == "null"
}
