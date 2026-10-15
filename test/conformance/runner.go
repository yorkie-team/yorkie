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

package conformance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
)

// docKey is the key of every replica's document.
const docKey = key.Key("conformance")

// Results of a run.
const (
	ResultPass     = "pass"
	ResultDiverged = "diverged"
	ResultError    = "error"
	ResultFail     = "fail"
)

// Checks holds the outcome of each invariant and of the expect steps (E).
type Checks struct {
	I1, I2, I3, I4, E bool
}

// String is the checks as five `1`/`0` characters for I1..I4 and E, e.g.
// "01101".
func (c Checks) String() string {
	var sb strings.Builder
	for _, ok := range []bool{c.I1, c.I2, c.I3, c.I4, c.E} {
		if ok {
			sb.WriteByte('1')
		} else {
			sb.WriteByte('0')
		}
	}
	return sb.String()
}

// Record is what one run of a script yields. Two runners agree on a script
// when their records have the same Steps, XML and digests.
type Record struct {
	ID        string
	N         int
	Result    string
	Checks    Checks
	Steps     []string
	XML       []string
	Shapes    []string
	Digest    string
	XMLDigest string
	Err       string

	// Expects describes each expect step that did not hold.
	Expects []string
}

// entry is one server-log entry. A nil changes slice is an op that produced
// nothing; it still takes a log index and still counts for delivery.
type entry struct {
	author  int
	changes []*change.Change
}

// run is one replay of a script in one GC mode.
type run struct {
	minVVMode    bool
	exchangeMode bool
	actors       []time.ActorID
	docs         []*document.Document
	clientSeq    []uint32
	log          []entry
	cursor       []int
	rows         []time.VersionVector
	lines        []string

	// snapshotOK turns false when an I3 comparison fails.
	snapshotOK bool

	// expects counts the expect steps run; expectFails describes those that
	// did not hold. Both stay empty in minvv mode.
	expects     int
	expectFails []string
}

// Run replays the script in off and minvv modes and checks I1-I4 and E.
func Run(s *Script) Record {
	rec := Record{ID: s.ID, N: s.Replicas}
	off, offErr := replay(s, false)
	rec.Steps = off.lines
	if rec.Steps == nil {
		rec.Steps = []string{}
	}
	rec.Expects = off.expectFails
	expects := 0
	for _, st := range s.Steps {
		if st.Op == opExpect {
			expects++
		}
	}
	e := off.expects == expects && len(off.expectFails) == 0
	if offErr != nil {
		rec.Err = offErr.Error()
		return finish(rec, Checks{E: e})
	}

	xml, shapes := off.state()
	rec.XML, rec.Shapes = xml, shapes
	i1 := true
	for i := 1; i < len(xml); i++ {
		if xml[i] != xml[0] || shapes[i] != shapes[0] {
			i1 = false
		}
	}
	i3 := off.snapshotOK
	for r := range off.docs {
		if _, ok := off.snapshotMatches(r); !ok {
			i3 = false
		}
	}

	mv, mvErr := replay(s, true)
	if mvErr != nil {
		rec.Err = mvErr.Error()
		return finish(rec, Checks{I1: i1, I3: i3, E: e})
	}
	mvXML, _ := mv.state()
	i4 := mvXML[0] == xml[0]
	for i := 1; i < len(mvXML); i++ {
		if mvXML[i] != mvXML[0] {
			i4 = false
		}
	}
	return finish(rec, Checks{I1: i1, I2: true, I3: i3, I4: i4, E: e})
}

func finish(rec Record, c Checks) Record {
	rec.Checks = c
	steps := strings.Join(rec.Steps, "\n")
	switch {
	case !c.I2:
		rec.Result = ResultError
		rec.XML = []string{}
		rec.Shapes = nil
		rec.Digest = digest(steps + "\nERROR")
		rec.XMLDigest = rec.Digest
		return rec
	case !c.I1:
		rec.Result = ResultDiverged
	case !c.I3 || !c.I4 || !c.E:
		rec.Result = ResultFail
	default:
		rec.Result = ResultPass
	}
	xml := strings.Join(rec.XML, "\n")
	rec.Digest = digest(steps + "\n" + xml + "\n" + strings.Join(rec.Shapes, "\n"))
	rec.XMLDigest = digest(steps + "\n" + xml)
	return rec
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// replay runs every step of the script, then drains every replica. Any
// panic or error aborts the run; the lines of the steps completed so far are
// kept.
func replay(s *Script, minVVMode bool) (rn *run, err error) {
	rn = &run{minVVMode: minVVMode, snapshotOK: true}
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()

	rn.exchangeMode = s.Delivery == DeliveryExchange
	rn.setup(s.Replicas, s.Init)
	for _, st := range s.Steps {
		rn.step(st)
	}
	if rn.exchangeMode {
		rn.exchange(nil)
		return rn, nil
	}
	for r := range rn.docs {
		for rn.pull(r) >= 0 {
		}
	}
	if minVVMode {
		vv := rn.minVV()
		for r := range rn.docs {
			rn.apply(r, nil, change.NewCheckpoint(0, 0), vv)
		}
	}
	return rn, nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func (rn *run) setup(n int, init Init) {
	rn.actors = make([]time.ActorID, n)
	rn.docs = make([]*document.Document, n)
	rn.clientSeq = make([]uint32, n)
	rn.cursor = make([]int, n)
	for i := range n {
		actor, err := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1))
		must(err)
		rn.actors[i] = actor
		rn.docs[i] = document.New(docKey)
		rn.docs[i].SetActor(actor)
	}

	root := init.Node
	if root == nil {
		root = &json.TreeNode{
			Type: "doc",
			Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: textType, Value: init.Text}},
			}},
		}
	}
	must(rn.docs[0].Update(func(r *json.Object, p *presence.Presence) error {
		r.SetNewTree("t", *root)
		return nil
	}))
	initChanges := rn.take(0)
	for i := 1; i < n; i++ {
		rn.apply(i, initChanges, change.NewCheckpoint(0, 0), time.InitialVersionVector)
	}

	// The init change is not an edit of the script: an undo must not reach
	// it, or it would remove the tree.
	for _, doc := range rn.docs {
		must(doc.ClearHistory())
	}

	// Every replica holds a row from the start, so minVV never covers what a
	// replica that has not synced yet could still refer to.
	rn.rows = make([]time.VersionVector, n)
	for i := range n {
		rn.rows[i] = rn.docs[i].VersionVector()
	}
}

// packVV is the version vector a pack applied to replica r carries. In
// minvv mode it is minVV, but only once r has every log entry it did not
// author: the real server answers a pull with all pending changes before it
// lets the client collect, so a tombstone is never collected while a
// concurrent change that refers to it is still on its way. A pack that
// leaves r behind the log carries the initial vector and collects nothing.
func (rn *run) packVV(r int) time.VersionVector {
	if !rn.minVVMode || rn.pending(r) {
		return time.InitialVersionVector
	}
	return rn.minVV()
}

// pending reports whether the log holds an entry replica r has not seen.
func (rn *run) pending(r int) bool {
	for i := rn.cursor[r]; i < len(rn.log); i++ {
		if rn.log[i].author != r {
			return true
		}
	}
	return false
}

func (rn *run) minVV() time.VersionVector {
	return time.MinVersionVector(rn.rows...)
}

// apply hands a pack to replica r through the protobuf converter.
func (rn *run) apply(r int, changes []*change.Change, cp change.Checkpoint, vv time.VersionVector) {
	pb, err := converter.ToChangePack(change.NewPack(docKey, cp, changes, vv, nil))
	must(err)
	pack, err := converter.FromChangePack(pb)
	must(err)
	must(rn.docs[r].ApplyChangePack(pack))
}

// take collects replica r's local changes through the converter and acks
// them. It returns nil when there were none.
func (rn *run) take(r int) []*change.Change {
	pb, err := converter.ToChangePack(rn.docs[r].CreateChangePack())
	must(err)
	pack, err := converter.FromChangePack(pb)
	must(err)
	if len(pack.Changes) == 0 {
		return nil
	}
	rn.clientSeq[r] = pack.Changes[len(pack.Changes)-1].ClientSeq()
	vv := time.InitialVersionVector
	if rn.rows != nil {
		rn.rows[r] = rn.docs[r].VersionVector()
		vv = rn.packVV(r)
	}
	rn.apply(r, nil, change.NewCheckpoint(0, rn.clientSeq[r]), vv)
	return pack.Changes
}

// pull delivers the next entry replica r has not seen and returns its log
// index, or -1 when nothing is left.
func (rn *run) pull(r int) int {
	for rn.cursor[r] < len(rn.log) && rn.log[rn.cursor[r]].author == r {
		rn.cursor[r]++
	}
	if rn.cursor[r] >= len(rn.log) {
		return -1
	}
	idx := rn.cursor[r]
	rn.cursor[r]++
	if e := rn.log[idx]; e.changes != nil {
		rn.apply(r, e.changes, change.NewCheckpoint(0, 0), rn.packVV(r))
		rn.rows[r] = rn.docs[r].VersionVector()
	}
	return idx
}

func (rn *run) step(st Step) {
	d := fmt.Sprintf("d%d", st.R+1)
	switch st.Op {
	case "pull":
		if idx := rn.pull(st.R); idx >= 0 {
			rn.lines = append(rn.lines, fmt.Sprintf("pull %s <- #%d", d, idx))
		} else {
			rn.lines = append(rn.lines, fmt.Sprintf("pull %s -", d))
		}
	case "sync":
		for idx := rn.pull(st.R); idx >= 0; idx = rn.pull(st.R) {
			rn.lines = append(rn.lines, fmt.Sprintf("pull %s <- #%d", d, idx))
		}
	case "snapshot":
		rn.snapshot(st.R)
		rn.lines = append(rn.lines, d+".snapshot")
	case "exchange":
		rn.lines = append(rn.lines, rn.exchange(st.Orders))
	case opExpect:
		rn.expect(st)
		rn.lines = append(rn.lines, opExpect)
	default:
		if rn.exchangeMode {
			before := rn.localChanges(st.R)
			desc := rn.local(st)
			if rn.localChanges(st.R) == before || desc == "" {
				desc = "noop"
			}
			rn.lines = append(rn.lines, d+"."+desc)
			return
		}
		desc := rn.local(st)
		changes := rn.take(st.R)
		rn.log = append(rn.log, entry{author: st.R, changes: changes})
		if changes == nil || desc == "" {
			desc = "noop"
		}
		rn.lines = append(rn.lines, d+"."+desc)
	}
}

// local performs a local op on replica st.R and returns its description, or
// "" when the op resolved to nothing.
func (rn *run) local(st Step) string {
	doc := rn.docs[st.R]
	switch st.Op {
	case opEdit, opStyle, opRemoveStyle:
		rn.update(st.R, func(t *json.Tree) { treeOp(t, st) })
		return describeOp(st)
	case "update":
		rn.update(st.R, func(t *json.Tree) {
			for _, op := range st.Ops {
				treeOp(t, op)
			}
		})
		descs := make([]string, len(st.Ops))
		for i, op := range st.Ops {
			descs[i] = describeOp(op)
		}
		return "update[" + strings.Join(descs, ";") + "]"
	case "undo":
		must(doc.Undo())
		return "undo"
	case "redo":
		must(doc.Redo())
		return "redo"
	case opGen:
		return rn.gen(st)
	}
	panic(fmt.Sprintf("unknown op %q", st.Op))
}

func (rn *run) update(r int, fn func(t *json.Tree)) {
	must(rn.docs[r].Update(func(root *json.Object, p *presence.Presence) error {
		fn(root.GetTree("t"))
		return nil
	}))
}

// edit runs an edit with optional text on replica r.
func (rn *run) edit(r, from, to int, text string, split int) {
	rn.update(r, func(t *json.Tree) {
		treeOp(t, Step{Op: opEdit, From: from, To: to, Text: text, Split: split})
	})
}

// treeOp runs one edit, style or removeStyle op on the tree.
func treeOp(t *json.Tree, st Step) {
	switch st.Op {
	case opEdit:
		switch {
		case st.nodes != nil:
			t.EditBulk(st.From, st.To, st.nodes, st.Split)
		case st.Text != "":
			t.Edit(st.From, st.To, &json.TreeNode{Type: textType, Value: st.Text}, st.Split)
		default:
			t.Edit(st.From, st.To, nil, st.Split)
		}
	case opStyle:
		t.Style(st.From, st.To, st.Attrs)
	case opRemoveStyle:
		t.RemoveStyle(st.From, st.To, st.Keys)
	}
}

// describeOp is the step-line form of an edit, style or removeStyle op.
func describeOp(st Step) string {
	switch st.Op {
	case opStyle:
		return describeStyle(st.From, st.To, st.Attrs)
	case opRemoveStyle:
		return fmt.Sprintf("removeStyle(%d,%d,%s)", st.From, st.To, strings.Join(st.Keys, ","))
	}
	if st.nodes != nil {
		xml := make([]string, len(st.nodes))
		for i, n := range st.nodes {
			xml[i] = nodeXML(n)
		}
		return fmt.Sprintf("edit(%d,%d,%s)", st.From, st.To, strings.Join(xml, ","))
	}
	return describeEdit(st.From, st.To, st.Text, st.Split)
}

func describeEdit(from, to int, text string, split int) string {
	switch {
	case text != "":
		return fmt.Sprintf("edit(%d,%d,'%s')", from, to, text)
	case split == 1 && from == to:
		return fmt.Sprintf("split(%d)", from)
	case split > 1 && from == to:
		return fmt.Sprintf("split(%d,%d)", from, split)
	default:
		return fmt.Sprintf("edit(%d,%d)", from, to)
	}
}

// localChanges counts replica r's changes not yet exchanged.
func (rn *run) localChanges(r int) int {
	return len(rn.docs[r].CreateChangePack().Changes)
}

// exchange hands every replica's local changes to the others, as one pack
// per author, in the given orders (nil: each replica receives the others in
// ascending index), then acks every author. In minvv mode every replica has
// then seen everything, so all rows are set and one empty pack carrying
// minVV goes to every replica. It returns the step line.
func (rn *run) exchange(orders [][]int) string {
	n := len(rn.docs)
	if orders == nil {
		orders = make([][]int, n)
		for i := range n {
			for j := range n {
				if j != i {
					orders[i] = append(orders[i], j)
				}
			}
		}
	}

	packs := make([][]*change.Change, n)
	for i, doc := range rn.docs {
		pb, err := converter.ToChangePack(doc.CreateChangePack())
		must(err)
		pack, err := converter.FromChangePack(pb)
		must(err)
		if len(pack.Changes) > 0 {
			packs[i] = pack.Changes
		}
	}
	for i, order := range orders {
		for _, j := range order {
			if packs[j] != nil {
				rn.apply(i, packs[j], change.NewCheckpoint(0, 0), time.InitialVersionVector)
			}
		}
	}
	for i, changes := range packs {
		if changes != nil {
			rn.clientSeq[i] = changes[len(changes)-1].ClientSeq()
			rn.apply(i, nil, change.NewCheckpoint(0, rn.clientSeq[i]), time.InitialVersionVector)
		}
	}
	if rn.minVVMode {
		for i, doc := range rn.docs {
			rn.rows[i] = doc.VersionVector()
		}
		vv := rn.minVV()
		for i := range rn.docs {
			rn.apply(i, nil, change.NewCheckpoint(0, 0), vv)
		}
	}

	parts := make([]string, n)
	for i, order := range orders {
		from := make([]string, len(order))
		for k, j := range order {
			from[k] = fmt.Sprintf("d%d", j+1)
		}
		parts[i] = fmt.Sprintf("d%d<-[%s]", i+1, strings.Join(from, ","))
	}
	return "exchange " + strings.Join(parts, " ")
}

// expect compares each replica's XML with the expected one, in off mode.
func (rn *run) expect(st Step) {
	if rn.minVVMode {
		return
	}
	rn.expects++
	for r, d := range rn.docs {
		if got := tree(d).ToXML(); got != st.XML[r] {
			rn.expectFails = append(rn.expectFails, fmt.Sprintf(
				"expect %q (step line %d): d%d is %s, want %s", st.Label, len(rn.lines), r+1, got, st.XML[r]))
		}
	}
}

func describeStyle(from, to int, attrs map[string]string) string {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	kv := make([]string, len(keys))
	for i, k := range keys {
		kv[i] = k + "=" + attrs[k]
	}
	return fmt.Sprintf("style(%d,%d,%s)", from, to, strings.Join(kv, ","))
}

// rebuild returns a fresh replica r restored from its own snapshot bytes,
// the way a client applies a snapshot pack from the server.
func (rn *run) rebuild(r int) *document.Document {
	old := rn.docs[r]
	bytes, err := converter.SnapshotToBytes(old.RootObject(), nil)
	must(err)
	pb, err := converter.ToChangePack(change.NewPack(
		docKey, change.NewCheckpoint(0, rn.clientSeq[r]), nil, old.VersionVector(), bytes,
	))
	must(err)
	pack, err := converter.FromChangePack(pb)
	must(err)

	doc := document.New(docKey)
	doc.SetActor(rn.actors[r])
	must(doc.ApplyChangePack(pack))
	return doc
}

// snapshotMatches is I3 for replica r: it rebuilds r and reports whether the
// rebuild has the same XML and shape. A rebuild that panics counts as a
// mismatch and yields no document, as JS's checkRebuild does.
func (rn *run) snapshotMatches(r int) (rebuilt *document.Document, ok bool) {
	defer func() {
		if recover() != nil {
			rebuilt, ok = nil, false
		}
	}()
	rebuilt = rn.rebuild(r)
	ok = tree(rebuilt).ToXML() == tree(rn.docs[r]).ToXML() && shape(rebuilt) == shape(rn.docs[r])
	return rebuilt, ok
}

// snapshot replaces replica r by its rebuild; in off mode it checks I3 too.
// A rebuild that panics outside the check aborts the run, and so does a
// replica holding changes not yet exchanged, which the rebuild would lose.
func (rn *run) snapshot(r int) {
	if rn.docs[r].HasLocalChanges() {
		panic(fmt.Sprintf("snapshot of d%d with changes not yet exchanged", r+1))
	}
	var rebuilt *document.Document
	if !rn.minVVMode {
		var ok bool
		if rebuilt, ok = rn.snapshotMatches(r); !ok {
			rn.snapshotOK = false
		}
	}
	if rebuilt == nil {
		rebuilt = rn.rebuild(r)
	}
	rn.docs[r] = rebuilt
}

func (rn *run) state() (xml, shapes []string) {
	for _, d := range rn.docs {
		xml = append(xml, tree(d).ToXML())
		shapes = append(shapes, shape(d))
	}
	return xml, shapes
}

func tree(doc *document.Document) *crdt.Tree {
	return doc.RootObject().Get("t").(*crdt.Tree)
}

// nodeID is `{lamport}:{actorHex}:{offset}`, built from fields.
func nodeID(n *crdt.TreeNode) string {
	id := n.ID()
	return fmt.Sprintf("%d:%s:%d", id.CreatedAt.Lamport(), id.CreatedAt.ActorID().String(), id.Offset)
}

// shape is the canonical identity string of the tree, tombstones included.
func shape(doc *document.Document) string {
	var walk func(n *crdt.TreeNode) string
	walk = func(n *crdt.TreeNode) string {
		removed := ""
		if n.RemovedAt() != nil {
			removed = "x"
		}
		if n.IsText() {
			return nodeID(n) + removed + `"` + n.Value + `"`
		}
		children := n.Children(true)
		cs := make([]string, len(children))
		for i, c := range children {
			cs[i] = walk(c)
		}
		return n.Type() + "#" + nodeID(n) + removed + "[" + strings.Join(cs, ",") + "]"
	}
	return walk(tree(doc).Root())
}
