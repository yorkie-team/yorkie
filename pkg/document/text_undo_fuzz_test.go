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

// This harness drives Text undo and redo against concurrent deletions over
// the push/pull model of gc_rga_fuzz_test.go, every change through protobuf:
//
//	go test -tags rgafuzz ./pkg/document/ -run TestTextUndoFuzz -v
//
// It requires convergence with GC off and with GC on when nothing is
// inserted, and that every replica's removal ledger survives a snapshot. With
// GC on and inserts, a recreated piece can still land after text inserted
// between the gap and the following piece of its insertion (see
// docs/tasks/active/20261021-text-undo-removal-ledger-todo.md), so that
// setting is only reported.

package document_test

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// undoFuzzWire passes the server log through protobuf.
func undoFuzzWire(t *testing.T, s *advServer) {
	t.Helper()
	for i, ch := range s.log {
		pb, err := converter.ToChangePack(change.NewPack("k", change.NewCheckpoint(0, 0), []*change.Change{ch}, nil, nil))
		require.NoError(t, err)
		p, err := converter.FromChangePack(pb)
		require.NoError(t, err)
		s.log[i] = p.Changes[0]
	}
}

func undoFuzzLen(t *testing.T, d *document.Document) int {
	t.Helper()
	return len([]rune(textOf(t, d, "t").String()))
}

// undoFuzzLedger is the removal ledger of every node as a value: removedAt,
// the removals and the cancel pairs.
func undoFuzzLedger(text *crdt.Text) string {
	var b strings.Builder
	for _, n := range text.Nodes() {
		ledgered, dels, cancels := n.Ledger()
		var ds, cs []string
		if ledgered {
			for _, d := range dels {
				ds = append(ds, d.ToTestString())
			}
			for _, c := range cancels {
				cs = append(cs, c.Deletion.ToTestString()+">"+c.CancelledAt.ToTestString())
			}
		} else if n.RemovedAt() != nil {
			ds = append(ds, n.RemovedAt().ToTestString())
		}
		sort.Strings(ds)
		sort.Strings(cs)
		ra := "-"
		if n.RemovedAt() != nil {
			ra = n.RemovedAt().ToTestString()
		}
		fmt.Fprintf(&b, "%s r=%s d=%v c=%v|", n.ID().ToTestString(), ra, ds, cs)
	}
	return b.String()
}

// undoFuzzSnapshot reports a ledger that does not survive a snapshot.
func undoFuzzSnapshot(t *testing.T, d *document.Document) string {
	t.Helper()
	bytes, err := converter.SnapshotToBytes(d.RootObject(), nil)
	require.NoError(t, err)
	obj, _, err := converter.BytesToSnapshot(bytes)
	if err != nil {
		return "decode: " + err.Error()
	}
	got, ok := obj.Get("t").(*crdt.Text)
	if !ok {
		return "no text"
	}
	if want, have := undoFuzzLedger(textOf(t, d, "t")), undoFuzzLedger(got); want != have {
		return "ledger\n " + want + "\n " + have
	}
	return ""
}

// undoFuzzSeed runs one seed and returns "" on convergence, or the failure.
func undoFuzzSeed(t *testing.T, seed int64, gc bool, insertPct, clients, steps int) string {
	t.Helper()
	rnd := rand.New(rand.NewSource(seed)) //nolint:gosec // seeded so a failing seed reproduces
	s := newAdvServer()
	cs := newAdvClients(t, clients)
	require.NoError(t, cs[0].doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("t").Edit(0, 0, "abcdefghij")
		return nil
	}))
	for _, c := range cs {
		require.NoError(t, s.sync(c, gc))
	}
	require.NoError(t, s.sync(cs[0], gc))
	require.NoError(t, cs[0].doc.ClearHistory())

	for step := range steps {
		c := cs[rnd.Intn(len(cs))]
		k := rnd.Intn(10)
		if k < 2 && rnd.Intn(100) >= insertPct {
			k = 2
		}
		var err error
		switch {
		case k < 2:
			pos := rnd.Intn(undoFuzzLen(t, c.doc) + 1)
			ch := string(rune('A' + rnd.Intn(26)))
			err = c.doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetText("t").Edit(pos, pos, ch)
				return nil
			})
		case k < 5:
			l := undoFuzzLen(t, c.doc)
			if l == 0 {
				continue
			}
			from := rnd.Intn(l)
			to := from + 1 + rnd.Intn(min(3, l-from))
			err = c.doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetText("t").Edit(from, to, "")
				return nil
			})
		case k < 7:
			err = c.doc.Undo()
		case k < 8:
			err = c.doc.Redo()
		default:
			err = s.sync(c, gc)
			undoFuzzWire(t, s)
		}
		if err != nil {
			return fmt.Sprintf("step %d: %v", step, err)
		}
		for _, x := range cs {
			if why := undoFuzzSnapshot(t, x.doc); why != "" {
				return fmt.Sprintf("step %d: %s", step, why)
			}
		}
	}
	for range 3 {
		for _, c := range cs {
			if err := s.sync(c, gc); err != nil {
				return fmt.Sprintf("final sync: %v", err)
			}
		}
	}
	want := textOf(t, cs[0].doc, "t").String()
	for _, c := range cs[1:] {
		if got := textOf(t, c.doc, "t").String(); got != want {
			return fmt.Sprintf("diverged %q vs %q", want, got)
		}
	}
	return ""
}

func TestTextUndoFuzz(t *testing.T) {
	const seeds = 1000
	for _, tc := range []struct {
		gc        bool
		insertPct int
		clients   int
		required  bool
	}{
		{gc: false, insertPct: 0, clients: 3, required: true},
		{gc: false, insertPct: 100, clients: 3, required: true},
		{gc: false, insertPct: 100, clients: 4, required: true},
		{gc: true, insertPct: 0, clients: 3, required: true},
		{gc: true, insertPct: 100, clients: 3, required: false},
	} {
		name := fmt.Sprintf("gc=%v insert=%d%% clients=%d", tc.gc, tc.insertPct, tc.clients)
		t.Run(name, func(t *testing.T) {
			failed := 0
			for seed := range int64(seeds) {
				why := undoFuzzSeed(t, seed, tc.gc, tc.insertPct, tc.clients, 40)
				if why == "" {
					continue
				}
				failed++
				if tc.required && failed <= 3 {
					t.Errorf("seed %d: %s", seed, why)
				}
			}
			t.Logf("%s: %d of %d seeds failed", name, failed, seeds)
			if tc.required {
				assert.Zero(t, failed)
			}
		})
	}
}
