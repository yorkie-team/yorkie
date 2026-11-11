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

package document_test

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func originMinVV(docs []*document.Document) time.VersionVector {
	vv := docs[0].VersionVector()
	for _, d := range docs[1:] {
		o := d.VersionVector()
		vv.Min(&o)
	}
	return vv
}

func originEdit(t *testing.T, d *document.Document, fn func(*json.Text)) {
	t.Helper()
	require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
		fn(root.GetText("t"))
		return nil
	}))
}

// originRunIntact reports whether the surviving characters of run appear in the
// merged text in run order with nothing else between them.
func originRunIntact(merged []rune, run string, alpha string) bool {
	var pos []int
	for i, c := range merged {
		if strings.ContainsRune(alpha, c) {
			pos = append(pos, i)
		}
	}
	if len(pos) == 0 {
		return true
	}
	for k := 1; k < len(pos); k++ {
		if pos[k] != pos[k-1]+1 {
			return false
		}
	}
	// order: surviving chars must be a subsequence of run in order
	got := []rune{}
	for _, p := range pos {
		got = append(got, merged[p])
	}
	ri := 0
	rr := []rune(run)
	for _, c := range got {
		for ri < len(rr) && rr[ri] != c {
			ri++
		}
		if ri == len(rr) {
			return false
		}
		ri++
	}
	return true
}

// TestMixedOps runs rounds of concurrent edits at one spot: each replica
// types a run (forward/backward/random) and may also delete, replace, style
// or undo there. Some replicas can be "old clients" that send no origins, and
// the base text can come from an old document (no origins). Between rounds
// some replicas GC under a legitimate minimum version vector before syncing.
func TestMixedOps(t *testing.T) {
	type cfg struct {
		name       string
		legacy     func(i int) bool
		legacyBase bool
		extra      bool
		gc         bool
	}
	cfgs := []cfg{
		{"inserts+gc", func(int) bool { return false }, false, false, true},
		{"insert-vs-delete/replace/style/undo", func(int) bool { return false }, false, true, false},
		{"insert-vs-delete/replace/style/undo+gc", func(int) bool { return false }, false, true, true},
		{"old-base-doc", func(int) bool { return false }, true, false, false},
		{"one-old-client", func(i int) bool { return i == 0 }, false, false, false},
		{"one-old-client+extra+gc", func(i int) bool { return i == 0 }, true, true, true},
	}
	defer func() { json.RecordInsertOrigins = true }()

	for _, c := range cfgs {
		for _, n := range []int{2, 3} {
			name := fmt.Sprintf("%s/replicas=%d", c.name, n)
			t.Run(name, func(t *testing.T) {
				trials := 300
				if v := os.Getenv("ORIGIN_TRIALS"); v != "" {
					trials, _ = strconv.Atoi(v)
				}
				rounds := 2
				if v := os.Getenv("ORIGIN_ROUNDS"); v != "" {
					rounds, _ = strconv.Atoi(v)
				}
				diverged, broken, errs := 0, 0, 0
				for seed := range trials {
					rnd := rand.New(rand.NewSource(int64(seed + 1)))
					json.RecordInsertOrigins = !c.legacyBase
					docs := intentReplicas(t, n, "0123456789")
					var runs []string
					var trace []string
					logf := func(f string, a ...any) { trace = append(trace, fmt.Sprintf(f, a...)) }
					ok := func() (ok bool) {
						defer func() {
							if r := recover(); r != nil {
								ok = false
							}
						}()
						for round := 0; round < rounds; round++ {
							for i, d := range docs {
								json.RecordInsertOrigins = !c.legacy(i)
								at := 4 + rnd.Intn(3)
								if l := len([]rune(intentText(d))); at > l {
									at = l
								}
								strategy := rnd.Intn(3)
								var chars []rune
								for k := 0; k < 2+rnd.Intn(3); k++ {
									chars = append(chars, rune(0xAC00+i*1000+round*10+k))
								}
								var run []rune
								for _, ch := range chars {
									off := 0
									switch strategy {
									case 0:
										off = len(run)
									case 2:
										off = rnd.Intn(len(run) + 1)
									}
									logf("r%d d%d insert %q at %d", round, i, string(ch), at+off)
									originEdit(t, d, func(tx *json.Text) { tx.Edit(at+off, at+off, string(ch)) })
									run = append(run[:off], append([]rune{ch}, run[off:]...)...)
								}
								runs = append(runs, string(run))
								if c.extra {
									l := len([]rune(intentText(d)))
									switch rnd.Intn(5) {
									case 0: // delete a base char right before the run
										if at > 0 {
											logf("r%d d%d delete [%d,%d)", round, i, at-1, at)
											originEdit(t, d, func(tx *json.Text) { tx.Edit(at-1, at, "") })
										}
									case 1: // replace the char right after the run
										if e := at + len(run); e < l {
											logf("r%d d%d replace [%d,%d) with *", round, i, e, e+1)
											originEdit(t, d, func(tx *json.Text) { tx.Edit(e, e+1, "*") })
										}
									case 2:
										logf("r%d d%d style [%d,%d)", round, i, max(0, at-1), min(l, at+len(run)+1))
										originEdit(t, d, func(tx *json.Text) { tx.Style(max(0, at-1), min(l, at+len(run)+1), map[string]string{"b": "1"}) })
									case 3:
										logf("r%d d%d undo", round, i)
										require.NoError(t, d.Undo())
									}
								}
							}
							if c.gc && rnd.Intn(2) == 0 {
								vv := originMinVV(docs)
								for _, d := range docs {
									if rnd.Intn(2) == 0 {
										logf("r%d GC on d%d", round, docIndex(docs, d))
										d.GarbageCollect(vv)
									}
								}
							}
							logf("r%d sync all -> %q", round, intentText(docs[0]))
							intentSyncAll(t, docs)
						}
						if c.gc {
							vv := originMinVV(docs)
							for _, d := range docs {
								d.GarbageCollect(vv)
							}
						}
						return true
					}()
					if !ok {
						errs++
						continue
					}
					merged := docs[0].Marshal()
					same := true
					for _, d := range docs[1:] {
						if d.Marshal() != merged {
							same = false
						}
					}
					if !same {
						if os.Getenv("ORIGIN_LOGDIV") != "" {
							var texts []string
							for _, d := range docs {
								texts = append(texts, intentText(d))
							}
							t.Logf("DIVERGED seed=%d steps=%d texts=%q\n  %s", seed, len(trace), texts, strings.Join(trace, "\n  "))
						}
						diverged++
						continue
					}
					text := []rune(intentText(docs[0]))
					// Only the last round's runs: a round-1 run may be legitimately
					// split by a round-2 insert made after seeing it.
					for i := range docs {
						run := runs[(rounds-1)*n+i]
						if !originRunIntact(text, run, run) {
							broken++
							break
						}
					}
				}
				t.Logf("MIXED %s errors=%d diverged=%d interleaved=%d (of %d)", name, errs, diverged, broken, trials)
			})
		}
	}
}

func docIndex(docs []*document.Document, d *document.Document) int {
	for i, x := range docs {
		if x == d {
			return i
		}
	}
	return -1
}
