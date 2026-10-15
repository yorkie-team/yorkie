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
				rows[k] = Row{Seed: seed, Result: rec.Result, Digest: rec.Digest, XMLDigest: rec.XMLDigest}
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

// TestCorpus runs every corpus script and holds it to its ledger status,
// strictly both ways.
func TestCorpus(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(corpusDir, "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)

	for _, path := range paths {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".json"), func(t *testing.T) {
			s, err := LoadScript(path)
			require.NoError(t, err)
			status := s.Status[sdk]
			rec := Run(s)
			switch status {
			case "pass":
				assert.Equal(t, ResultPass, rec.Result,
					"%s regressed: checks %+v, xml %q, err %s", s.ID, rec.Checks, rec.XML, rec.Err)
			case "known-diverge":
				assert.NotEqual(t, ResultPass, rec.Result,
					"%s now passes: update status.%s to \"pass\" in %s", s.ID, sdk, path)
			default:
				t.Fatalf("%s: status.%s must be \"pass\" or \"known-diverge\", got %q", path, sdk, status)
			}
		})
	}
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
			require.Equal(t, want.Runs, len(got.Rows))
			for i, w := range want.Rows {
				assert.Equal(t, w, got.Rows[i], "seed %d of %s differs from the fixture", w.Seed, f.Name)
			}
			assert.Equal(t, want.Diverged, got.Diverged, "%s divergence count moved", f.Name)
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
