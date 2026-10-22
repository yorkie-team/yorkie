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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/cespare/xxhash/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
)

const (
	hllVectorsPath = "hll/vectors.json"

	// hllPrecision, hllRegisterCount and hllMaxRegisterValue mirror the
	// unexported constants of pkg/document/crdt/hll.go. The fixture carries
	// them so the JS runner holds the same parameters.
	hllPrecision        = 14
	hllRegisterCount    = 1 << hllPrecision
	hllMaxRegisterValue = 64 - hllPrecision + 1
)

// HLLVectors is the dedup-counter hash fixture both SDKs check. Its format is
// specified in hll/README.md.
type HLLVectors struct {
	V                int              `json:"v"`
	Hash             string           `json:"hash"`
	Precision        int              `json:"precision"`
	RegisterCount    int              `json:"registerCount"`
	MaxRegisterValue int              `json:"maxRegisterValue"`
	Vectors          []HLLVector      `json:"vectors"`
	Families         []HLLFamily      `json:"families"`
	Aggregate        HLLDigest        `json:"aggregate"`
	Restore          []HLLRestoreCase `json:"restore"`
}

// HLLVector is one string and the register update a single Add derives from it.
type HLLVector struct {
	ID    string `json:"id"`
	Value string `json:"value"`
	UTF8  string `json:"utf8"`
	Hash  string `json:"hash"`
	Index int    `json:"index"`
	Rho   int    `json:"rho"`
}

// HLLFamily is a generated run of strings, pinned by the digest of the
// registers they fill rather than one entry each.
type HLLFamily struct {
	ID     string    `json:"id"`
	Prefix string    `json:"prefix"`
	UTF8   string    `json:"utf8"`
	Count  int       `json:"count"`
	Digest HLLDigest `json:"digest"`
}

// HLLDigest pins a filled register array: its estimated cardinality, how many
// registers it left non-zero, and the sha256 of the array itself.
type HLLDigest struct {
	Count     uint64 `json:"count"`
	NonZero   int    `json:"nonZero"`
	Registers string `json:"registers"`
}

// HLLRestoreCase is a register payload both SDKs must accept or reject alike.
// Registers maps a register index, in decimal, to the value the payload holds
// there; every other register is zero. Count is the cardinality an accepted
// payload reports.
type HLLRestoreCase struct {
	ID        string         `json:"id"`
	Length    int            `json:"length"`
	Registers map[string]int `json:"registers,omitempty"`
	Accept    bool           `json:"accept"`
	Count     uint64         `json:"count,omitempty"`
}

// hllValues are the strings whose register update the fixture pins one by one.
// They cover the cases the cross-SDK audit of yorkie#2274 compared: the empty
// string, lengths around xxhash64's 32-byte block, and the UTF-8 shapes a JS
// string encodes differently from its code units — Hangul, astral emoji, a ZWJ
// sequence, a combining mark against its precomposed form, and a NUL.
var hllValues = []struct{ id, value string }{
	{"empty", ""},
	{"ascii-1", "a"},
	{"actor-24", "000000000000000000000001"},
	{"len-31", "0123456789abcdef0123456789abcde"},
	{"len-32", "0123456789abcdef0123456789abcdef"},
	{"len-33", "0123456789abcdef0123456789abcdef0"},
	{"len-100", "0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789"},
	{"hangul", "한글 사용자"},
	{"emoji", "🙂🚀"},
	{"emoji-zwj", "👨‍👩‍👧"},
	{"combining", "e\u0301"},
	{"precomposed", "\u00e9"},
	{"nul", "a\x00b"},
}

// hllFamilies are the generated runs. A family's i-th string is its prefix
// followed by i in decimal, for i in [0, count).
var hllFamilies = []struct {
	id, prefix string
	count      int
}{
	{"user", "user-", 10000},
	{"hangul", "사용자-", 5000},
	{"emoji", "🙂-", 5000},
}

// hllRestoreCases are hand-written, not derived from the implementation: they
// are what the bound in Restore is held to, so regenerating the fixture must
// not be able to move them.
var hllRestoreCases = []HLLRestoreCase{
	{ID: "zeros", Length: hllRegisterCount, Accept: true, Count: 0},
	{ID: "one-register", Length: hllRegisterCount, Registers: map[string]int{"0": 1}, Accept: true, Count: 1},
	{
		ID:        "max-register",
		Length:    hllRegisterCount,
		Registers: map[string]int{"0": hllMaxRegisterValue, "16383": hllMaxRegisterValue},
		Accept:    true,
		Count:     2,
	},
	{ID: "above-max", Length: hllRegisterCount, Registers: map[string]int{"0": hllMaxRegisterValue + 1}},
	{ID: "reported-200", Length: hllRegisterCount, Registers: map[string]int{"0": 200}},
	{ID: "max-byte", Length: hllRegisterCount, Registers: map[string]int{"16383": 255}},
	{ID: "empty-payload", Length: 0},
	{ID: "one-short", Length: hllRegisterCount - 1},
	{ID: "one-long", Length: hllRegisterCount + 1},
}

// hllDigest fills a fresh HLL with values and pins what they left behind.
func hllDigest(values func(yield func(string) bool)) HLLDigest {
	hll := crdt.NewHLL()
	for value := range values {
		hll.Add(value)
	}
	return hllDigestOf(hll)
}

// hllDigestOf pins a filled HLL.
func hllDigestOf(hll *crdt.HLL) HLLDigest {
	registers := hll.Bytes()
	nonZero := 0
	for _, val := range registers {
		if val != 0 {
			nonZero++
		}
	}
	sum := sha256.Sum256(registers)
	return HLLDigest{Count: hll.Count(), NonZero: nonZero, Registers: hex.EncodeToString(sum[:])}
}

// hllFamilyValues yields a family's strings in order.
func hllFamilyValues(prefix string, count int) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for i := range count {
			if !yield(prefix + strconv.Itoa(i)) {
				return
			}
		}
	}
}

// buildHLLVectors derives the fixture from the implementation. A vector's
// index and rho are observed — the string is added to an empty HLL and the one
// register it changed is read back — rather than recomputed from the index and
// rho formula, so the fixture checks Add instead of restating it.
func buildHLLVectors(t *testing.T) HLLVectors {
	t.Helper()

	got := HLLVectors{
		V:                1,
		Hash:             "xxhash64",
		Precision:        hllPrecision,
		RegisterCount:    hllRegisterCount,
		MaxRegisterValue: hllMaxRegisterValue,
		Restore:          hllRestoreCases,
	}

	all := crdt.NewHLL()
	for _, v := range hllValues {
		hll := crdt.NewHLL()
		require.True(t, hll.Add(v.value), "%s: the first Add must change a register", v.id)
		index, rho := -1, 0
		for i, val := range hll.Bytes() {
			if val != 0 {
				require.Equal(t, -1, index, "%s: one Add changed more than one register", v.id)
				index, rho = i, int(val)
			}
		}
		all.Add(v.value)

		got.Vectors = append(got.Vectors, HLLVector{
			ID:    v.id,
			Value: v.value,
			UTF8:  hex.EncodeToString([]byte(v.value)),
			Hash:  fmt.Sprintf("%016x", xxhash.Sum64String(v.value)),
			Index: index,
			Rho:   rho,
		})
	}

	for _, f := range hllFamilies {
		values := hllFamilyValues(f.prefix, f.count)
		for value := range values {
			all.Add(value)
		}
		got.Families = append(got.Families, HLLFamily{
			ID:     f.id,
			Prefix: f.prefix,
			UTF8:   hex.EncodeToString([]byte(f.prefix)),
			Count:  f.count,
			Digest: hllDigest(values),
		})
	}
	got.Aggregate = hllDigestOf(all)

	return got
}

// TestHLLVectors pins the dedup counter's hashing: the xxhash64 of each test
// string, the register index and rho a single Add derives from it, and the
// registers, non-zero count and estimate a whole family fills. The JS runner
// replays the same file, so a change to either SDK's hashing, UTF-8 handling
// or register mapping fails here instead of diverging a counter silently.
// CONFORMANCE_UPDATE=1 rewrites the file.
func TestHLLVectors(t *testing.T) {
	got := buildHLLVectors(t)

	if os.Getenv("CONFORMANCE_UPDATE") == "1" {
		writeJSON(t, hllVectorsPath, got)
		return
	}

	raw, err := os.ReadFile(hllVectorsPath)
	require.NoError(t, err, "run with CONFORMANCE_UPDATE=1 to create it")
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var want HLLVectors
	require.NoError(t, dec.Decode(&want), hllVectorsPath)

	assert.Equal(t, want.V, got.V, "%s: format version", hllVectorsPath)
	assert.Equal(t, want.Hash, got.Hash, "%s: hash function", hllVectorsPath)
	assert.Equal(t, want.Precision, got.Precision, "%s: precision", hllVectorsPath)
	assert.Equal(t, want.RegisterCount, got.RegisterCount, "%s: register count", hllVectorsPath)
	assert.Equal(t, want.MaxRegisterValue, got.MaxRegisterValue, "%s: max register value", hllVectorsPath)
	assert.Equal(t, want.Vectors, got.Vectors, "%s: the register update of a string moved", hllVectorsPath)
	assert.Equal(t, want.Families, got.Families, "%s: a generated family's registers moved", hllVectorsPath)
	assert.Equal(t, want.Aggregate, got.Aggregate, "%s: the aggregate registers moved", hllVectorsPath)
	assert.Equal(t, want.Restore, got.Restore, "%s: the restore cases differ from the Go ones", hllVectorsPath)

	canon, err := json.MarshalIndent(want, "", "  ")
	require.NoError(t, err)
	require.Equal(t, string(append(canon, '\n')), string(raw),
		"%s is not in the form CONFORMANCE_UPDATE=1 writes", hllVectorsPath)
}

// TestHLLRestore holds Restore to the fixture's cases: both SDKs accept and
// reject the same register payloads, and an accepted one reports the same
// count.
func TestHLLRestore(t *testing.T) {
	raw, err := os.ReadFile(hllVectorsPath)
	require.NoError(t, err)
	var want HLLVectors
	require.NoError(t, json.Unmarshal(raw, &want), hllVectorsPath)
	require.NotEmpty(t, want.Restore)

	for _, c := range want.Restore {
		t.Run(c.ID, func(t *testing.T) {
			data := make([]byte, c.Length)
			for key, val := range c.Registers {
				i, err := strconv.Atoi(key)
				require.NoError(t, err, "%s: register index %q", c.ID, key)
				require.Less(t, i, c.Length, "%s: register index %d is past the payload", c.ID, i)
				require.GreaterOrEqual(t, val, 0, "%s: register value %d", c.ID, val)
				require.LessOrEqual(t, val, 255, "%s: register value %d", c.ID, val)
				data[i] = byte(val)
			}

			hll := crdt.NewHLL()
			err := hll.Restore(data)
			if !c.Accept {
				assert.Error(t, err, "%s: must be rejected", c.ID)
				return
			}
			require.NoError(t, err, "%s: must be accepted", c.ID)
			assert.Equal(t, c.Count, hll.Count(), "%s: count", c.ID)
		})
	}
}
