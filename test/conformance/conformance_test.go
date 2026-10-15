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
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	corpusDir  = "tree/corpus"
	parityDir  = "tree/parity"
	paritySeed = 1
	parityRuns = 200
	sdk        = "go"
)

// Row is one seed's outcome in a parity fixture.
type Row struct {
	Seed      int    `json:"seed"`
	Result    string `json:"result"`
	Checks    string `json:"checks"`
	Digest    string `json:"digest"`
	XMLDigest string `json:"xmlDigest"`
}

// Fixture is a parity fixture: one family's rows over a seed range.
type Fixture struct {
	Fam      string `json:"fam"`
	N        int    `json:"n"`
	Steps    int    `json:"steps"`
	Seed0    int    `json:"seed0"`
	Runs     int    `json:"runs"`
	Diverged int    `json:"diverged"`
	Rows     []Row  `json:"rows"`
}

// runFamily runs seeds seed0..seed0+runs-1 of a family in parallel.
func runFamily(f Family, seed0, runs int) Fixture {
	rows := make([]Row, runs)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Go(func() {
			for k := range jobs {
				seed := seed0 + k
				rec := Run(Gen(uint32(seed), f.Fam, f.N, f.Steps))
				rows[k] = Row{
					Seed:      seed,
					Result:    rec.Result,
					Checks:    rec.Checks.String(),
					Digest:    rec.Digest,
					XMLDigest: rec.XMLDigest,
				}
			}
		})
	}
	for k := range runs {
		jobs <- k
	}
	close(jobs)
	wg.Wait()

	fx := Fixture{Fam: f.Fam, N: f.N, Steps: f.Steps, Seed0: seed0, Runs: runs, Rows: rows}
	for _, r := range rows {
		if r.Result == ResultDiverged {
			fx.Diverged++
		}
	}
	return fx
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(b, '\n'), 0o600)) // #nosec G703 -- test-chosen path
}

// TestCorpus runs every corpus script and holds it to its ledger status:
// the result must be exactly the one the status maps to.
func TestCorpus(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(corpusDir, "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)

	for _, path := range paths {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".json"), func(t *testing.T) {
			s, err := LoadScript(path)
			require.NoError(t, err)
			status, ok := s.Status[sdk]
			require.True(t, ok, "%s: status.%s is missing", path, sdk)
			want := statuses[status]
			rec := Run(s)
			assert.Equal(t, want, rec.Result,
				"%s: status.%s is %q but the result is %q (checks %s, xml %q, err %q, expects %q): "+
					"update status.%s in the ledger if the change is intended\n%s",
				path, sdk, status, rec.Result, rec.Checks, rec.XML, rec.Err, rec.Expects, sdk,
				strings.Join(rec.Steps, "\n"))
		})
	}
}

// TestLoadScriptRejects checks that a malformed script fails to load and
// names its file. The JS runner's loader test holds the same cases.
func TestLoadScriptRejects(t *testing.T) {
	head := `"v":1,"id":"x","replicas":1,"init":"ab"`
	step := func(st string) string { return `{` + head + `,"steps":[` + st + `]}` }
	cases := map[string]string{
		"version":         `{"v":3,"id":"x","replicas":1,"init":"ab","steps":[]}`,
		"field":           `{` + head + `,"steps":[],"extra":1}`,
		"step field":      step(`{"r":0,"op":"pull","form":1}`),
		"op":              step(`{"r":0,"op":"insert"}`),
		"gen kind":        step(`{"r":0,"op":"gen","k":"move","f":0,"g":0,"h":0,"v":"c"}`),
		"status":          `{` + head + `,"status":{"go":"diverged"},"steps":[]}`,
		"status sdk":      `{` + head + `,"status":{"goo":"pass"},"steps":[]}`,
		"replica":         step(`{"r":1,"op":"pull"}`),
		"fractional r":    step(`{"r":0.5,"op":"pull"}`),
		"missing r":       step(`{"op":"pull"}`),
		"null r":          step(`{"r":null,"op":"pull"}`),
		"null step":       step(`null`),
		"replicas":        `{"v":1,"id":"x","replicas":0,"init":"ab","steps":[]}`,
		"fractional reps": `{"v":1,"id":"x","replicas":1.5,"init":"ab","steps":[]}`,
		"trailing doc":    `{` + head + `,"steps":[]} {}`,
		"trailing brace":  `{` + head + `,"steps":[]} }`,
		"edit no to":      step(`{"r":0,"op":"edit","from":1}`),
		"edit float":      step(`{"r":0,"op":"edit","from":1.5,"to":2}`),
		"edit null text":  step(`{"r":0,"op":"edit","from":1,"to":1,"text":null}`),
		"edit int text":   step(`{"r":0,"op":"edit","from":1,"to":1,"text":1}`),
		"edit attrs":      step(`{"r":0,"op":"edit","from":1,"to":1,"attrs":{"b":"1"}}`),
		"style no attrs":  step(`{"r":0,"op":"style","from":0,"to":1}`),
		"style {} attrs":  step(`{"r":0,"op":"style","from":0,"to":1,"attrs":{}}`),
		"style int attr":  step(`{"r":0,"op":"style","from":0,"to":1,"attrs":{"b":1}}`),
		"style null attr": step(`{"r":0,"op":"style","from":0,"to":1,"attrs":{"b":null}}`),
		"rs no keys":      step(`{"r":0,"op":"removeStyle","from":0,"to":1}`),
		"rs empty keys":   step(`{"r":0,"op":"removeStyle","from":0,"to":1,"keys":[]}`),
		"rs int key":      step(`{"r":0,"op":"removeStyle","from":0,"to":1,"keys":[1]}`),
		"rs null key":     step(`{"r":0,"op":"removeStyle","from":0,"to":1,"keys":[null]}`),
		"gen no v":        step(`{"r":0,"op":"gen","k":"ins","f":0,"g":0,"h":0}`),
		"gen f of 1":      step(`{"r":0,"op":"gen","k":"ins","f":1,"g":0,"h":0,"v":"c"}`),
		"gen negative h":  step(`{"r":0,"op":"gen","k":"ins","f":0,"g":0,"h":-0.1,"v":"c"}`),
		"gen int v":       step(`{"r":0,"op":"gen","k":"ins","f":0,"g":0,"h":0,"v":1}`),
		"pull attrs":      step(`{"r":0,"op":"pull","attrs":{"b":"1"}}`),
		"undo from":       step(`{"r":0,"op":"undo","from":0}`),
	}
	maps.Copy(cases, rejectsV2)
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.json")
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			_, err := LoadScript(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), path)
		})
	}
}

// rejectsV2 are the malformed scripts of the format v2 rules. The JS
// runner's loader test holds the same cases.
var rejectsV2 = func() map[string]string {
	v1 := `"v":1,"id":"x","replicas":2`
	v2 := `"v":2,"id":"x","replicas":2`
	log := func(st string) string { return `{` + v2 + `,"init":"ab","steps":[` + st + `]}` }
	ex := func(st string) string {
		return `{` + v2 + `,"delivery":"exchange","init":"ab","steps":[` + st + `]}`
	}
	init := func(node string) string { return `{` + v2 + `,"init":` + node + `,"steps":[]}` }
	edit := `"op":"edit","from":1,"to":1`
	return map[string]string{
		"v1 expect":       `{` + v1 + `,"init":"ab","steps":[{"op":"expect","xml":["",""]}]}`,
		"v1 update":       `{` + v1 + `,"init":"ab","steps":[{"r":0,"op":"update","ops":[{` + edit + `}]}]}`,
		"v1 tree init":    `{` + v1 + `,"init":{"type":"doc"},"steps":[]}`,
		"v1 contents":     `{` + v1 + `,"init":"ab","steps":[{"r":0,` + edit + `,"contents":[{"type":"p"}]}]}`,
		"v1 delivery":     `{` + v1 + `,"delivery":"log","init":"ab","steps":[]}`,
		"delivery":        `{` + v2 + `,"delivery":"push","init":"ab","steps":[]}`,
		"null delivery":   `{` + v2 + `,"delivery":null,"init":"ab","steps":[]}`,
		"null init":       `{` + v2 + `,"init":null,"steps":[]}`,
		"number init":     init(`1`),
		"text root":       init(`{"type":"text","value":"a"}`),
		"no type":         init(`{"children":[]}`),
		"empty type":      init(`{"type":""}`),
		"element value":   init(`{"type":"doc","value":"a"}`),
		"element field":   init(`{"type":"doc","kids":[]}`),
		"text children":   init(`{"type":"doc","children":[{"type":"text","value":"a","children":[]}]}`),
		"text no value":   init(`{"type":"doc","children":[{"type":"text"}]}`),
		"text int value":  init(`{"type":"doc","children":[{"type":"text","value":1}]}`),
		"null child":      init(`{"type":"doc","children":[null]}`),
		"children object": init(`{"type":"doc","children":{}}`),
		"int attribute":   init(`{"type":"doc","attributes":{"b":1}}`),
		"null attribute":  init(`{"type":"doc","attributes":{"b":null}}`),
		"gen with tree init": `{` + v2 + `,"init":{"type":"doc"},"steps":[` +
			`{"r":0,"op":"gen","k":"ins","f":0,"g":0,"h":0,"v":"c"}]}`,
		"contents and text":     log(`{"r":0,` + edit + `,"text":"a","contents":[{"type":"p"}]}`),
		"empty contents":        log(`{"r":0,` + edit + `,"contents":[]}`),
		"contents object":       log(`{"r":0,` + edit + `,"contents":{"type":"p"}}`),
		"bad contents node":     log(`{"r":0,` + edit + `,"contents":[{"type":"p","extra":1}]}`),
		"update no ops":         log(`{"r":0,"op":"update"}`),
		"update empty ops":      log(`{"r":0,"op":"update","ops":[]}`),
		"update nested r":       log(`{"r":0,"op":"update","ops":[{"r":0,` + edit + `}]}`),
		"update nested undo":    log(`{"r":0,"op":"update","ops":[{"op":"undo"}]}`),
		"update nested update":  log(`{"r":0,"op":"update","ops":[{"op":"update","ops":[{` + edit + `}]}]}`),
		"update nested no to":   log(`{"r":0,"op":"update","ops":[{"op":"edit","from":1}]}`),
		"update nested no op":   log(`{"r":0,"op":"update","ops":[{"from":1,"to":1}]}`),
		"update nested null":    log(`{"r":0,"op":"update","ops":[null]}`),
		"update no r":           log(`{"op":"update","ops":[{` + edit + `}]}`),
		"pull in exchange":      ex(`{"r":0,"op":"pull"}`),
		"sync in exchange":      ex(`{"r":0,"op":"sync"}`),
		"exchange in log":       log(`{"op":"exchange"}`),
		"exchange r":            ex(`{"r":0,"op":"exchange"}`),
		"exchange null orders":  ex(`{"op":"exchange","orders":null}`),
		"orders count":          ex(`{"op":"exchange","orders":[[1]]}`),
		"orders self":           ex(`{"op":"exchange","orders":[[0],[0]]}`),
		"orders missing":        ex(`{"op":"exchange","orders":[[],[0]]}`),
		"orders duplicate":      ex(`{"op":"exchange","orders":[[1,1],[0]]}`),
		"orders out of range":   ex(`{"op":"exchange","orders":[[2],[0]]}`),
		"orders null order":     ex(`{"op":"exchange","orders":[[1],null]}`),
		"orders null replica":   ex(`{"op":"exchange","orders":[[null],[0]]}`),
		"orders fractional":     ex(`{"op":"exchange","orders":[[1.5],[0]]}`),
		"expect r":              log(`{"r":0,"op":"expect","xml":["",""]}`),
		"expect no xml":         log(`{"op":"expect"}`),
		"expect xml count":      log(`{"op":"expect","xml":[""]}`),
		"expect null xml entry": log(`{"op":"expect","xml":["",null]}`),
		"expect int label":      log(`{"op":"expect","xml":["",""],"label":1}`),
	}
}()

// TestExpect checks that an expect that does not hold fails the run without
// touching I1-I4, and that one that holds passes it.
func TestExpect(t *testing.T) {
	for _, tc := range []struct{ xml, result, checks string }{
		{"<doc><p>ab</p></doc>", ResultPass, "11111"},
		{"<doc><p>ba</p></doc>", ResultFail, "11110"},
	} {
		s, err := ParseScript([]byte(`{"v":2,"id":"x","replicas":2,"init":"ab","steps":[` +
			`{"op":"expect","xml":["<doc><p>ab</p></doc>","` + tc.xml + `"]}]}`))
		require.NoError(t, err)
		rec := Run(s)
		assert.Equal(t, tc.result, rec.Result)
		assert.Equal(t, tc.checks, rec.Checks.String())
	}
}

// exchangeScript is a three-replica exchange script in which every replica
// holds one local edit, followed by a single exchange step carrying orders.
func exchangeScript(t *testing.T, orders string) *Script {
	t.Helper()
	s, err := ParseScript([]byte(`{"v":2,"id":"orders","replicas":3,"delivery":"exchange",` +
		`"init":"abc","steps":[` +
		`{"r":0,"op":"edit","from":1,"to":1,"text":"A"},` +
		`{"r":1,"op":"edit","from":2,"to":2,"text":"B"},` +
		`{"r":2,"op":"edit","from":3,"to":3,"text":"C"},` +
		`{"op":"exchange"` + orders + `}]}`))
	require.NoError(t, err)
	return s
}

// TestExchangeOrders holds the exchange step to the arrival order its
// `orders` declares, both in the packs it applies and in its step line. A
// runner that parsed `orders` and then delivered in ascending index would
// converge on every corpus script all the same, so nothing else here would
// catch it: the whole point of the field is the order the packs arrive in.
func TestExchangeOrders(t *testing.T) {
	for _, tc := range []struct {
		name     string
		orders   string
		arrivals []string
		line     string
	}{{
		name:     "absent is ascending index",
		orders:   "",
		arrivals: []string{"d1<-d2", "d1<-d3", "d2<-d1", "d2<-d3", "d3<-d1", "d3<-d2"},
		line:     "exchange d1<-[d2,d3] d2<-[d1,d3] d3<-[d1,d2]",
	}, {
		name:     "spelling out the ascending order changes nothing",
		orders:   `,"orders":[[1,2],[0,2],[0,1]]`,
		arrivals: []string{"d1<-d2", "d1<-d3", "d2<-d1", "d2<-d3", "d3<-d1", "d3<-d2"},
		line:     "exchange d1<-[d2,d3] d2<-[d1,d3] d3<-[d1,d2]",
	}, {
		name:     "every replica reversed",
		orders:   `,"orders":[[2,1],[2,0],[1,0]]`,
		arrivals: []string{"d1<-d3", "d1<-d2", "d2<-d3", "d2<-d1", "d3<-d2", "d3<-d1"},
		line:     "exchange d1<-[d3,d2] d2<-[d3,d1] d3<-[d2,d1]",
	}, {
		name:     "the order the split-order-*-3 scripts use",
		orders:   `,"orders":[[2,1],[0,2],[1,0]]`,
		arrivals: []string{"d1<-d3", "d1<-d2", "d2<-d1", "d2<-d3", "d3<-d2", "d3<-d1"},
		line:     "exchange d1<-[d3,d2] d2<-[d1,d3] d3<-[d2,d1]",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			s := exchangeScript(t, tc.orders)
			rn, err := replay(s, false)
			require.NoError(t, err)
			// Two exchanges run: the step, then the end-of-run one, which
			// delivers nothing because the step handed over and acked
			// every replica's changes.
			require.Len(t, rn.arrivals, 2)
			assert.Equal(t, tc.arrivals, rn.arrivals[0])
			assert.Empty(t, rn.arrivals[1])

			rec := Run(s)
			require.Equal(t, ResultPass, rec.Result, "checks %s, err %q", rec.Checks, rec.Err)
			assert.Contains(t, rec.Steps, tc.line)
		})
	}
}

// TestExchangeOrdersCorpus holds every corpus script that declares `orders`
// to that arrival order. TestCorpus checks only the one-word result, which
// convergence makes blind to the field.
func TestExchangeOrdersCorpus(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(corpusDir, "*.json"))
	require.NoError(t, err)
	checked := 0
	for _, path := range paths {
		s, err := LoadScript(path)
		require.NoError(t, err)
		if !declaresOrders(s) {
			continue
		}
		checked++
		t.Run(strings.TrimSuffix(filepath.Base(path), ".json"), func(t *testing.T) {
			rn, err := replay(s, false)
			require.NoError(t, err)
			k := 0
			for i, st := range s.Steps {
				if st.Op != opExchange {
					continue
				}
				if st.Orders != nil {
					require.Less(t, k, len(rn.arrivals), "%s: step %d never ran", path, i)
					// A replica with nothing to hand over is skipped, so the
					// declared order is what the arrivals are a subsequence
					// of; any reordering breaks that.
					assert.Truef(t, subsequence(rn.arrivals[k], declaredArrivals(st)),
						"%s: step %d delivered %v, which is not in the declared order %v",
						path, i, rn.arrivals[k], declaredArrivals(st))
				}
				k++
			}
		})
	}
	require.NotZero(t, checked, "no corpus script declares orders")
}

func declaresOrders(s *Script) bool {
	for _, st := range s.Steps {
		if st.Op == opExchange && st.Orders != nil {
			return true
		}
	}
	return false
}

// declaredArrivals is every delivery an exchange step's orders calls for, as
// run.arrivals spells them.
func declaredArrivals(st Step) []string {
	var want []string
	for to, order := range st.Orders {
		for _, from := range order {
			want = append(want, fmt.Sprintf("d%d<-d%d", to+1, from+1))
		}
	}
	return want
}

// subsequence reports whether got appears in want in order, gaps allowed.
func subsequence(got, want []string) bool {
	i := 0
	for _, w := range want {
		if i < len(got) && got[i] == w {
			i++
		}
	}
	return i == len(got)
}

// TestParity regenerates every family's rows and compares them with the
// fixture both SDKs check. CONFORMANCE_UPDATE=1 rewrites the fixtures.
func TestParity(t *testing.T) {
	update := os.Getenv("CONFORMANCE_UPDATE") == "1"
	for _, f := range Families {
		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()
			got := runFamily(f, paritySeed, parityRuns)
			path := filepath.Join(parityDir, f.Name+".json")
			if update {
				writeJSON(t, path, got)
				return
			}

			raw, err := os.ReadFile(path) // #nosec G304 -- fixture path
			require.NoError(t, err, "run with CONFORMANCE_UPDATE=1 to create it")
			var want Fixture
			require.NoError(t, json.Unmarshal(raw, &want))
			require.Len(t, want.Rows, want.Runs, "%s: rows do not match runs", path)
			wantHead, gotHead := want, got
			wantHead.Rows, gotHead.Rows = nil, nil
			require.Equal(t, wantHead, gotHead, "%s: header differs from the regenerated one", path)
			for i, w := range want.Rows {
				assert.Equal(t, w, got.Rows[i], "seed %d of %s differs from the fixture", w.Seed, f.Name)
			}
		})
	}
}

// TestLargeRun runs a seed range of one family and writes its rows, for
// comparing against the JS runner. It is skipped unless CONFORMANCE_FAM is
// set; CONFORMANCE_RUNS, CONFORMANCE_SEED0 and CONFORMANCE_OUT tune it.
func TestLargeRun(t *testing.T) {
	name := os.Getenv("CONFORMANCE_FAM")
	if name == "" {
		t.Skip("CONFORMANCE_FAM is not set")
	}
	f, ok := FamilyByName(name)
	require.True(t, ok, "unknown family %q", name)
	runs, seed0 := envInt(t, "CONFORMANCE_RUNS", 5000), envInt(t, "CONFORMANCE_SEED0", 1)

	fx := runFamily(f, seed0, runs)
	counts := map[string]int{}
	for _, r := range fx.Rows {
		counts[r.Result]++
	}
	t.Logf("%s: %d runs from seed %d: %v", f.Name, runs, seed0, counts)
	if out := os.Getenv("CONFORMANCE_OUT"); out != "" {
		writeJSON(t, out, fx)
	}
}

func envInt(t *testing.T, k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	require.NoError(t, err, k)
	return n
}
