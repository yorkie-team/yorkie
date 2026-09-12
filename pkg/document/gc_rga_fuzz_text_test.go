//go:build rgafuzz

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

// The array fuzz next door is the acceptance criterion for the filed defect.
// These are the same experiment on Text and Tree, and they exist for one
// question: does a fix that closes the array leave the same shape open in the
// other two structures?
//
// READ THE CONTROL BEFORE READING THE NUMBERS. For concurrent edits that
// DELETE, the collection-off control does not pass on Text or on Tree -- it
// fails at roughly the same rate as the collection-on run -- so these two
// attribute NOTHING to collection. They are a lead, not a result.
//
// What makes the harness itself credible is TestAdvTextInsertOnlyFuzz below:
// with the same clients, the same push/pull model and the same comparison,
// inserts alone converge on 1000 of 1000 seeds with collection on AND off. So
// the failures above are not the harness losing changes.
//
// Measured on this worktree, 1000 seeds, 3 clients, 12 rounds:
//
//	                     main        with the reference fix
//	text, collection on   624/1000    595/1000
//	text, control         474/1000    474/1000
//	tree, collection on   554/1000    530/1000
//	tree, control         439/1000    439/1000
//
// The controls are byte-identical either way, so nothing here was introduced
// by the fix; the collection-on runs improve, which is the Text and Tree
// successor barriers doing their part. Closing these needs the control cleaned
// up first, and that is a different defect from the filed one.

package document_test

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func runAdvTextSeed(seed int64, nClients, rounds int, gc bool) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	rnd := rand.New(rand.NewSource(seed))
	srv := newAdvServer()

	cs := make([]*advClient, 0, nClients)
	for i := range nClients {
		id, e := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1))
		if e != nil {
			return e
		}
		d := document.New("adv-doc")
		d.SetActor(id)
		cs = append(cs, &advClient{doc: d, id: id})
	}

	if e := cs[0].doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewText("t").Edit(0, 0, "abcdef")
		return nil
	}); e != nil {
		return e
	}
	for _, c := range cs {
		if e := srv.sync(c, gc); e != nil {
			return fmt.Errorf("bootstrap sync: %w", e)
		}
	}

	for range rounds {
		for _, c := range cs {
			if rnd.Intn(100) >= 70 {
				continue
			}
			tag := c.id.String()[23:]
			if e := c.doc.Update(func(r *json.Object, _ *presence.Presence) error {
				txt := r.GetText("t")
				n := len([]rune(txt.String()))
				if n == 0 {
					txt.Edit(0, 0, tag)
					return nil
				}
				from := rnd.Intn(n)
				to := from + rnd.Intn(n-from+1)
				if rnd.Intn(2) == 0 {
					txt.Edit(from, to, tag)
				} else {
					txt.Edit(from, to, "")
				}
				return nil
			}); e != nil {
				return e
			}
		}
		for _, c := range cs {
			if rnd.Intn(100) < 55 {
				if e := srv.sync(c, gc); e != nil {
					return fmt.Errorf("sync: %w", e)
				}
			}
		}
	}

	for range 6 {
		for _, c := range cs {
			if e := srv.sync(c, gc); e != nil {
				return fmt.Errorf("final sync: %w", e)
			}
		}
	}

	want := cs[0].doc.Root().GetText("t").String()
	for i, c := range cs[1:] {
		if got := c.doc.Root().GetText("t").String(); got != want {
			return fmt.Errorf("DIVERGED c0 vs c%d\n  c0=%q\n  c%d=%q", i+1, want, i+1, got)
		}
	}
	return nil
}

func TestAdvTextFuzz(t *testing.T) {
	for _, tc := range []struct {
		name string
		gc   bool
	}{{"GC ON", true}, {"GC OFF (control)", false}} {
		var fail int
		var first string
		for seed := int64(1); seed <= 1000; seed++ {
			if err := runAdvTextSeed(seed, 3, 12, tc.gc); err != nil {
				fail++
				if first == "" {
					first = fmt.Sprintf("seed %d: %v", seed, err)
				}
			}
		}
		t.Logf("text %-18s %4d/1000 failed  %s", tc.name, fail, first)
	}
}

func runAdvTreeSeed(seed int64, nClients, rounds int, gc bool) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	rnd := rand.New(rand.NewSource(seed))
	srv := newAdvServer()

	cs := make([]*advClient, 0, nClients)
	for i := range nClients {
		id, e := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1))
		if e != nil {
			return e
		}
		d := document.New("adv-doc")
		d.SetActor(id)
		cs = append(cs, &advClient{doc: d, id: id})
	}

	if e := cs[0].doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewTree("tr", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "abcd"}}},
		}})
		return nil
	}); e != nil {
		return e
	}
	for _, c := range cs {
		if e := srv.sync(c, gc); e != nil {
			return fmt.Errorf("bootstrap sync: %w", e)
		}
	}

	for range rounds {
		for _, c := range cs {
			if rnd.Intn(100) >= 70 {
				continue
			}
			tag := c.id.String()[23:]
			if e := c.doc.Update(func(r *json.Object, _ *presence.Presence) error {
				tr := r.GetTree("tr")
				n := tr.Len()
				if n < 2 {
					return nil
				}
				from := 1 + rnd.Intn(n-1)
				to := from + rnd.Intn(n-from)
				if rnd.Intn(2) == 0 {
					tr.Edit(from, to, &json.TreeNode{Type: "text", Value: tag}, 0)
				} else {
					tr.Edit(from, to, nil, 0)
				}
				return nil
			}); e != nil {
				return e
			}
		}
		for _, c := range cs {
			if rnd.Intn(100) < 55 {
				if e := srv.sync(c, gc); e != nil {
					return fmt.Errorf("sync: %w", e)
				}
			}
		}
	}

	for range 6 {
		for _, c := range cs {
			if e := srv.sync(c, gc); e != nil {
				return fmt.Errorf("final sync: %w", e)
			}
		}
	}

	want := cs[0].doc.Root().GetTree("tr").ToXML()
	for i, c := range cs[1:] {
		if got := c.doc.Root().GetTree("tr").ToXML(); got != want {
			return fmt.Errorf("DIVERGED c0 vs c%d\n  c0=%q\n  c%d=%q", i+1, want, i+1, got)
		}
	}
	return nil
}

func TestAdvTreeFuzz(t *testing.T) {
	for _, tc := range []struct {
		name string
		gc   bool
	}{{"GC ON", true}, {"GC OFF (control)", false}} {
		var fail int
		var first string
		for seed := int64(1); seed <= 1000; seed++ {
			if err := runAdvTreeSeed(seed, 3, 12, tc.gc); err != nil {
				fail++
				if first == "" {
					first = fmt.Sprintf("seed %d: %v", seed, err)
				}
			}
		}
		t.Logf("tree %-18s %4d/1000 failed  %s", tc.name, fail, first)
	}
}

// TestAdvTextInsertOnlyFuzz is what licenses reading anything at all into the
// two above: restricted to inserts, the same harness converges everywhere.
func TestAdvTextInsertOnlyFuzz(t *testing.T) {
	for _, tc := range []struct {
		name string
		gc   bool
	}{{"GC ON", true}, {"GC OFF (control)", false}} {
		var fail int
		var first string
		for seed := int64(1); seed <= 1000; seed++ {
			if err := runAdvTextInsertSeed(seed, 3, 12, tc.gc); err != nil {
				fail++
				if first == "" {
					first = fmt.Sprintf("seed %d: %v", seed, err)
				}
			}
		}
		if fail > 0 {
			t.Errorf("text inserts %s: %d/1000 failed  %s", tc.name, fail, first)
		}
	}
}

func runAdvTextInsertSeed(seed int64, nClients, rounds int, gc bool) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	rnd := rand.New(rand.NewSource(seed))
	srv := newAdvServer()

	cs := make([]*advClient, 0, nClients)
	for i := range nClients {
		id, e := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1))
		if e != nil {
			return e
		}
		d := document.New("adv-doc")
		d.SetActor(id)
		cs = append(cs, &advClient{doc: d, id: id})
	}

	if e := cs[0].doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewText("t").Edit(0, 0, "abcdef")
		return nil
	}); e != nil {
		return e
	}
	for _, c := range cs {
		if e := srv.sync(c, gc); e != nil {
			return fmt.Errorf("bootstrap sync: %w", e)
		}
	}

	for range rounds {
		for _, c := range cs {
			if rnd.Intn(100) >= 70 {
				continue
			}
			tag := c.id.String()[23:]
			if e := c.doc.Update(func(r *json.Object, _ *presence.Presence) error {
				txt := r.GetText("t")
				n := len([]rune(txt.String()))
				at := 0
				if n > 0 {
					at = rnd.Intn(n)
				}
				txt.Edit(at, at, tag)
				return nil
			}); e != nil {
				return e
			}
		}
		for _, c := range cs {
			if rnd.Intn(100) < 55 {
				if e := srv.sync(c, gc); e != nil {
					return fmt.Errorf("sync: %w", e)
				}
			}
		}
	}

	for range 6 {
		for _, c := range cs {
			if e := srv.sync(c, gc); e != nil {
				return fmt.Errorf("final sync: %w", e)
			}
		}
	}

	want := cs[0].doc.Root().GetText("t").String()
	for i, c := range cs[1:] {
		if got := c.doc.Root().GetText("t").String(); got != want {
			return fmt.Errorf("DIVERGED c0 vs c%d\n  c0=%q\n  c%d=%q", i+1, want, i+1, got)
		}
	}
	return nil
}
