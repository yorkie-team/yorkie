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
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

var paraRe = regexp.MustCompile(`<p>([^<]*)</p>`)

// treeParas returns the text of each top-level paragraph, or nil when the tree
// is not the flat <doc><p>text</p>...</doc> shape the fuzz generates.
func treeParas(d *document.Document) []string {
	x := treeXMLOf(d)
	if !strings.HasPrefix(x, "<doc>") || !strings.HasSuffix(x, "</doc>") {
		return nil
	}
	body := x[len("<doc>") : len(x)-len("</doc>")]
	var out []string
	rest := body
	for rest != "" {
		m := paraRe.FindStringSubmatchIndex(rest)
		if m == nil || m[0] != 0 {
			return nil
		}
		out = append(out, rest[m[2]:m[3]])
		rest = rest[m[1]:]
	}
	return out
}

// treeFuzzOp applies one random structural edit; it returns a description or
// "" when nothing applicable was found. Panics from the engine propagate.
func treeFuzzOp(t *testing.T, rnd *rand.Rand, d *document.Document, mix string, ch *rune) string {
	paras := treeParas(d)
	if len(paras) == 0 {
		return ""
	}
	starts := make([]int, len(paras))
	pos := 0
	for i, p := range paras {
		starts[i] = pos
		pos += len([]rune(p)) + 2
	}
	pi := rnd.Intn(len(paras))
	plen := len([]rune(paras[pi]))
	textAt := func(off int) int { return starts[pi] + 1 + off }

	kinds := []string{"ins", "ins", "ins", "del"}
	if mix == "struct" {
		kinds = append(kinds, "split", "merge", "para")
	}
	switch kinds[rnd.Intn(len(kinds))] {
	case "ins":
		off := rnd.Intn(plen + 1)
		c := string(*ch)
		*ch++
		treeEdit(t, d, textAt(off), textAt(off), &json.TreeNode{Type: "text", Value: c})
		return fmt.Sprintf("ins %q p%d+%d", c, pi, off)
	case "del":
		if plen == 0 {
			return ""
		}
		off := rnd.Intn(plen)
		w := 1 + rnd.Intn(min(2, plen-off))
		treeEdit(t, d, textAt(off), textAt(off+w), nil)
		return fmt.Sprintf("del p%d+%d..%d", pi, off, off+w)
	case "split":
		if plen < 2 {
			return ""
		}
		off := 1 + rnd.Intn(plen-1)
		require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
			root.GetTree("t").Edit(textAt(off), textAt(off), nil, 1)
			return nil
		}))
		return fmt.Sprintf("split p%d+%d", pi, off)
	case "merge":
		if pi+1 >= len(paras) {
			return ""
		}
		end := textAt(plen)
		treeEdit(t, d, end, end+2, nil)
		return fmt.Sprintf("merge p%d|p%d", pi, pi+1)
	case "para":
		at := starts[pi]
		if rnd.Intn(2) == 0 {
			at = starts[pi] + plen + 2
		}
		c := string(*ch)
		*ch++
		treeEdit(t, d, at, at, &json.TreeNode{Type: "p", Children: []json.TreeNode{{Type: "text", Value: c}}})
		return fmt.Sprintf("para %q at %d", c, at)
	}
	return ""
}

// TestTreeOriginFuzz runs random concurrent Tree edits with GC on some replicas
// and checks convergence. ORIGIN_TREE_MIX=struct adds element split, merge and
// paragraph insertion to plain text insert/delete. ORIGIN_TREE_FUZZ sets trials.
func TestTreeOriginFuzz(t *testing.T) {
	trials := 2000
	if v := os.Getenv("ORIGIN_TREE_FUZZ"); v != "" {
		trials, _ = strconv.Atoi(v)
	}
	for _, mix := range []string{"text", "struct"} {
		if m := os.Getenv("ORIGIN_TREE_MIX"); m != "" && m != mix {
			continue
		}
		t.Run(mix, func(t *testing.T) {
			diverged, errs := 0, 0
			var first string
			first0 := 0
			if v := os.Getenv("ORIGIN_SEED0"); v != "" {
				first0, _ = strconv.Atoi(v)
				trials += first0
			}
			if v := os.Getenv("ORIGIN_SEED"); v != "" {
				first0, _ = strconv.Atoi(v)
				trials = first0 + 1
			}
			config := fmt.Sprintf("origins=%v gc=%v seeds=[%d,%d)",
				crdt.TreeOrigins, os.Getenv("ORIGIN_NOGC") == "", first0, trials)
			for seed := first0; seed < trials; seed++ {
				rnd := rand.New(rand.NewSource(int64(seed)))
				n := 2 + rnd.Intn(2)
				docs := treeReplicas(t, n, "XY")
				if mix == "struct" {
					require.NoError(t, docs[0].Update(func(root *json.Object, _ *presence.Presence) error {
						root.GetTree("t").Edit(4, 4, &json.TreeNode{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ZW"}}}, 0)
						return nil
					}))
					intentSyncAll(t, docs)
				}
				var trace []string
				ch := 'a'
				err := func() (err error) {
					defer func() {
						if r := recover(); r != nil {
							err = fmt.Errorf("panic: %v", r)
						}
					}()
					rounds := 1 + rnd.Intn(4)
					for round := range rounds {
						for i, d := range docs {
							for k := rnd.Intn(3); k > 0; k-- {
								if s := treeFuzzOp(t, rnd, d, mix, &ch); s != "" {
									trace = append(trace, fmt.Sprintf("r%d d%d %s", round, i, s))
								}
							}
						}
						noGC := os.Getenv("ORIGIN_NOGC") != ""
						if rnd.Intn(2) == 0 && !noGC {
							vv := originMinVV(docs)
							for i, d := range docs {
								if rnd.Intn(2) == 0 {
									trace = append(trace, fmt.Sprintf("r%d GC d%d", round, i))
									d.GarbageCollect(vv)
								}
							}
						}
						trace = append(trace, fmt.Sprintf("r%d sync", round))
						if err := originSyncAll(docs); err != nil {
							return err
						}
						if rnd.Intn(4) == 0 && !noGC {
							i := rnd.Intn(n)
							trace = append(trace, fmt.Sprintf("r%d reload d%d", round, i))
							if err := originReload(docs[i]); err != nil {
								return err
							}
						}
					}
					return nil
				}()
				if err != nil {
					errs++
					if first == "" {
						first = fmt.Sprintf("seed=%d err=%v\n  %s", seed, err, strings.Join(trace, "\n  "))
					}
					continue
				}
				for _, d := range docs[1:] {
					if treeXMLOf(d) != treeXMLOf(docs[0]) {
						diverged++
						if os.Getenv("ORIGIN_LIST") != "" {
							t.Logf("DIVSEED %d %d", seed, len(trace))
						}
						if first == "" {
							var xs []string
							for _, dd := range docs {
								xs = append(xs, treeXMLOf(dd))
							}
							first = fmt.Sprintf("seed=%d diverged %q\n  %s", seed, xs, strings.Join(trace, "\n  "))
						}
						break
					}
				}
			}
			t.Logf("TREEFUZZ mix=%s diverged=%d errors=%d of %d (%s)", mix, diverged, errs, trials-first0, config)
			if first != "" {
				t.Logf("first problem (%s):\n%s", config, first)
			}
			if mix == "text" {
				// Without element split/merge the Tree converges outright.
				// The struct mix is measured, not asserted: the createdAt-only
				// Tree already diverges under concurrent split/merge.
				require.Zero(t, diverged, "%s\n%s", config, first)
				require.Zero(t, errs, "%s\n%s", config, first)
			}
		})
	}
}
