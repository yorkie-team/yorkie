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
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	corpusDir     = "tree/corpus"
	corpusDigests = "tree/corpus-digests.json"
	parityDir     = "tree/parity"
	paritySeed    = 1
	sdk           = "go"
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
	Observer bool   `json:"observer,omitempty"`
	Seed0    int    `json:"seed0"`
	Runs     int    `json:"runs"`
	Diverged int    `json:"diverged"`
	Rows     []Row  `json:"rows"`
}

// cores bounds the seeds the whole package replays at once. TestParity
// runs every family in parallel and each family fans its seeds out again,
// so without a shared bound the suite keeps every core busy for its whole
// run. `go test ./...` runs package binaries side by side, and the CPU the
// suite leaves free is the CPU the timing-sensitive tests of the other
// packages — cluster leadership, housekeeping — need to meet their
// deadlines. Half the cores is enough to replay a family quickly and
// leaves the other half for them.
var cores = make(chan struct{}, max(1, runtime.NumCPU()/2))

// runFamily runs seeds seed0..seed0+runs-1 of a family in parallel.
func runFamily(f Family, seed0, runs int) Fixture {
	return runFamilyWith(f, seed0, runs, Options{})
}

// runFamilyWith is runFamily with run options.
func runFamilyWith(f Family, seed0, runs int, opts Options) Fixture {
	rows := make([]Row, runs)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range cap(cores) {
		wg.Go(func() {
			for k := range jobs {
				cores <- struct{}{}
				seed := seed0 + k
				rec := RunWith(f.Gen(uint32(seed)), opts)
				rows[k] = Row{
					Seed:      seed,
					Result:    rec.Result,
					Checks:    rec.Checks.String(),
					Digest:    rec.Digest,
					XMLDigest: rec.XMLDigest,
				}
				<-cores
			}
		})
	}
	for k := range runs {
		jobs <- k
	}
	close(jobs)
	wg.Wait()

	fx := Fixture{Fam: f.Fam, N: f.N, Steps: f.Steps, Observer: f.Observer, Seed0: seed0, Runs: runs, Rows: rows}
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

// CorpusDigest is one corpus script's record in corpus-digests.json.
type CorpusDigest struct {
	Result    string `json:"result"`
	Checks    string `json:"checks"`
	Digest    string `json:"digest"`
	XMLDigest string `json:"xmlDigest"`
}

// TestCorpus runs every corpus script and holds it to its ledger status:
// the result must be exactly the one the status maps to. It then holds every
// record to corpus-digests.json, which both runners check, so the step lines,
// xml and shapes of the ops only the corpus reaches are pinned byte for byte.
// CONFORMANCE_UPDATE=1 rewrites the file.
func TestCorpus(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(corpusDir, "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)

	got := map[string]CorpusDigest{}
	for _, path := range paths {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".json"), func(t *testing.T) {
			s, err := LoadScript(path)
			require.NoError(t, err)
			status, ok := s.Status[sdk]
			require.True(t, ok, "%s: status.%s is missing", path, sdk)
			want := statuses[status]
			rec := Run(s)
			got[filepath.Base(path)] = CorpusDigest{
				Result:    rec.Result,
				Checks:    rec.Checks.String(),
				Digest:    rec.Digest,
				XMLDigest: rec.XMLDigest,
			}
			assert.Equal(t, want, rec.Result,
				"%s: status.%s is %q but the result is %q (checks %s, xml %q, err %q, expects %q): "+
					"update status.%s in the ledger if the change is intended\n%s",
				path, sdk, status, rec.Result, rec.Checks, rec.XML, rec.Err, rec.Expects, sdk,
				strings.Join(rec.Steps, "\n"))
		})
	}

	if os.Getenv("CONFORMANCE_UPDATE") == "1" {
		writeJSON(t, corpusDigests, got)
		return
	}
	checkCorpusDigests(t, paths, got)
}

// checkCorpusDigests holds the records of the corpus scripts at paths to
// corpus-digests.json: the file lists exactly those scripts, each entry
// matches its record, and the file is byte for byte the one
// CONFORMANCE_UPDATE=1 writes.
func checkCorpusDigests(t *testing.T, paths []string, got map[string]CorpusDigest) {
	t.Helper()
	raw, err := os.ReadFile(corpusDigests)
	require.NoError(t, err, "run with CONFORMANCE_UPDATE=1 to create it")
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var want map[string]CorpusDigest
	require.NoError(t, dec.Decode(&want), corpusDigests)

	names := make([]string, 0, len(paths))
	for _, path := range paths {
		names = append(names, filepath.Base(path))
	}
	require.Equal(t, names, slices.Sorted(maps.Keys(want)),
		"%s must list exactly the corpus scripts", corpusDigests)
	for _, name := range names {
		assert.Equal(t, want[name], got[name], "%s: %s differs from the Go record", corpusDigests, name)
	}

	canon, err := json.MarshalIndent(want, "", "  ")
	require.NoError(t, err)
	require.Equal(t, string(append(canon, '\n')), string(raw),
		"%s is not in the form CONFORMANCE_UPDATE=1 writes", corpusDigests)
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
		"span gen text init":    log(`{"r":0,"op":"gen","k":"sins","f":0,"g":0,"h":0,"v":"c"}`),
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

// TestElementWithoutChildren checks that an element node without
// `children`, the init root or a node of an edit's contents, has none. The
// JS runner's test holds the same case.
func TestElementWithoutChildren(t *testing.T) {
	s, err := ParseScript([]byte(`{"v":2,"id":"x","replicas":2,"init":{"type":"doc"},"steps":[` +
		`{"op":"expect","xml":["<doc></doc>","<doc></doc>"]},` +
		`{"r":0,"op":"edit","from":0,"to":0,"contents":[{"type":"p"}]},` +
		`{"r":1,"op":"sync"}]}`))
	require.NoError(t, err)
	rec := Run(s)
	require.Equal(t, ResultPass, rec.Result, "checks %s, err %q", rec.Checks, rec.Err)
	assert.Equal(t, []string{"<doc><p></p></doc>", "<doc><p></p></doc>"}, rec.XML)
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
// `orders` declares, both in the packs it applies, in off and minvv mode,
// and in its step line. A runner that parsed `orders` and then delivered in
// ascending index would converge on every corpus script all the same, so
// nothing else here would catch it: the whole point of the field is the
// order the packs arrive in.
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
			for _, minVVMode := range []bool{false, true} {
				rn, err := replay(s, minVVMode)
				require.NoError(t, err)
				// Two exchanges run: the step, then the end-of-run one,
				// which delivers nothing because the step handed over and
				// acked every replica's changes.
				require.Len(t, rn.arrivals, 2)
				assert.Equal(t, tc.arrivals, rn.arrivals[0], "minvv %v", minVVMode)
				assert.Empty(t, rn.arrivals[1], "minvv %v", minVVMode)
			}

			rec := Run(s)
			require.Equal(t, ResultPass, rec.Result, "checks %s, err %q", rec.Checks, rec.Err)
			assert.Contains(t, rec.Steps, tc.line)
		})
	}
}

// TestExchangeOrdersCorpus holds every exchange of the corpus scripts with
// `exchange` delivery, in off and minvv mode, to exactly the deliveries its
// orders call for. TestCorpus checks only the one-word result, which
// convergence makes blind to the field. A script whose ledger status is
// known-error is held to the exchanges that completed before the error.
func TestExchangeOrdersCorpus(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(corpusDir, "*.json"))
	require.NoError(t, err)
	declared, delivered := 0, 0
	for _, path := range paths {
		s, err := LoadScript(path)
		require.NoError(t, err)
		if s.Delivery != DeliveryExchange {
			continue
		}
		var orders [][][]int
		for _, st := range s.Steps {
			if st.Op == opExchange {
				orders = append(orders, st.Orders)
				if st.Orders != nil {
					declared++
				}
			}
		}
		// The end-of-run exchange, with default orders.
		orders = append(orders, nil)

		t.Run(strings.TrimSuffix(filepath.Base(path), ".json"), func(t *testing.T) {
			for _, minVVMode := range []bool{false, true} {
				rn, err := replay(s, minVVMode)
				if s.Status[sdk] != "known-error" {
					require.NoError(t, err, "minvv %v", minVVMode)
					require.Len(t, rn.arrivals, len(orders), "minvv %v", minVVMode)
				}
				for k, got := range rn.arrivals {
					assert.Equal(t, wantArrivals(orders[k], s.Replicas, rn.senders[k]), got,
						"%s: exchange %d (minvv %v)", path, k, minVVMode)
					delivered += len(got)
				}
			}
		})
	}
	require.NotZero(t, declared, "no corpus script declares orders")
	require.NotZero(t, delivered, "no corpus exchange delivered anything")
}

// wantArrivals is every delivery an exchange with the given orders (nil:
// ascending index) calls for, as run.arrivals spells them, keeping only the
// packs of senders, the replicas that had changes to hand over.
func wantArrivals(orders [][]int, n int, senders []int) []string {
	sent := map[int]bool{}
	for _, j := range senders {
		sent[j] = true
	}
	want := []string{}
	for to := range n {
		var order []int
		if orders != nil {
			order = orders[to]
		} else {
			for j := range n {
				if j != to {
					order = append(order, j)
				}
			}
		}
		for _, from := range order {
			if sent[from] {
				want = append(want, fmt.Sprintf("d%d<-d%d", to+1, from+1))
			}
		}
	}
	return want
}

// TestParity regenerates every family's rows and compares them with the
// fixture both SDKs check. CONFORMANCE_UPDATE=1 rewrites the fixtures.
func TestParity(t *testing.T) {
	update := os.Getenv("CONFORMANCE_UPDATE") == "1"
	for _, f := range Families {
		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()
			got := runFamily(f, paritySeed, f.ParityRuns())
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

// TestLaggingMinVV pins the lagging minVV option: a script that converges
// under the protocol's GC throws once the one push still on its way to d1
// is covered by the minVV d1 collects with (yorkie#2110). Off mode is the
// same in both runs.
func TestLaggingMinVV(t *testing.T) {
	s, err := ParseScript([]byte(`{"v":1,"id":"lagging","replicas":2,"init":"abcd","steps":[` +
		`{"r":1,"op":"edit","from":2,"to":2,"split":1},` +
		`{"r":0,"op":"edit","from":1,"to":4},` +
		`{"r":1,"op":"edit","from":5,"to":6},` +
		`{"r":1,"op":"pull"}]}`))
	require.NoError(t, err)

	rec := Run(s)
	require.Equal(t, ResultPass, rec.Result, "checks %s, err %q", rec.Checks, rec.Err)
	lag := RunWith(s, Options{LaggingMinVV: true})
	assert.Equal(t, ResultError, lag.Result, "checks %s", lag.Checks)
	assert.Equal(t, "10101", lag.Checks.String())
	assert.Equal(t, rec.Steps, lag.Steps)
}

// TestObserverOnlySyncs checks that the observer of an observer family
// never takes a local op, and that it does sync.
func TestObserverOnlySyncs(t *testing.T) {
	for _, f := range Families {
		if !f.Observer {
			continue
		}
		synced := false
		for seed := uint32(1); seed <= 50; seed++ {
			for _, st := range f.Gen(seed).Steps {
				if st.R != f.N-1 {
					continue
				}
				require.Equal(t, opSync, st.Op, "%s seed %d", f.Name, seed)
				synced = true
			}
		}
		assert.True(t, synced, f.Name)
	}
}

// TestASubtreeASplitFedStaysForItsAuthor pins merge seed 528, where the
// detached state of a purge must outlive more than one collection. d1
// deletes a paragraph while d2 splits it, carrying text into a product the
// delete's cascade takes; d1, which never saw the split, then merges across
// that text, naming it where d1 last saw it. d2 has purged the product by
// then, at a vector that covers the delete and not the split, so a drop that
// waited only for a later vector failed d1's change with "node not found".
func TestASubtreeASplitFedStaysForItsAuthor(t *testing.T) {
	f, ok := FamilyByName("merge")
	require.True(t, ok)
	for _, opts := range []Options{{}, {LaggingMinVV: true}} {
		fx := runFamilyWith(f, 528, 1, opts)
		assert.NotEqual(t, "error", fx.Rows[0].Result, "lagging %v", opts.LaggingMinVV)
	}
}

// TestLargeRun runs a seed range of one family and writes its rows, for
// comparing against the JS runner. It is skipped unless CONFORMANCE_FAM is
// set; CONFORMANCE_RUNS, CONFORMANCE_SEED0 and CONFORMANCE_OUT tune it, and
// CONFORMANCE_GC=lagging runs the minvv mode with the lagging minVV of
// yorkie#2110 (see Options).
func TestLargeRun(t *testing.T) {
	name := os.Getenv("CONFORMANCE_FAM")
	if name == "" {
		t.Skip("CONFORMANCE_FAM is not set")
	}
	f, ok := FamilyByName(name)
	require.True(t, ok, "unknown family %q", name)
	runs, seed0 := envInt(t, "CONFORMANCE_RUNS", 5000), envInt(t, "CONFORMANCE_SEED0", 1)

	var opts Options
	switch gc := os.Getenv("CONFORMANCE_GC"); gc {
	case "":
	case "lagging":
		opts.LaggingMinVV = true
	default:
		t.Fatalf("unknown CONFORMANCE_GC %q", gc)
	}

	fx := runFamilyWith(f, seed0, runs, opts)
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
