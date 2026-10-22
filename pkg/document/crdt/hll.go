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

package crdt

import (
	"fmt"
	"math"
	"math/bits"

	"github.com/cespare/xxhash/v2"
)

const (
	// hllPrecision is the number of bits used for register indexing.
	// 2^14 = 16384 registers, ~16KB memory, ~2% standard error.
	hllPrecision = 14

	// hllRegisterCount is the number of registers.
	hllRegisterCount = 1 << hllPrecision

	// hllMaxRegisterValue is the largest value Add can write into a register:
	// the sentinel bit in Add caps rho at 64-hllPrecision+1. A payload holding
	// a larger value cannot come from an HLL, so Restore clamps it to this
	// bound rather than carrying a register the estimator never produces.
	hllMaxRegisterValue = 64 - hllPrecision + 1
)

// HLL is a HyperLogLog probabilistic cardinality estimator.
// It supports Add, Count, Merge (max-merge), and serialization.
type HLL struct {
	registers [hllRegisterCount]uint8
}

// NewHLL creates a new HLL instance.
func NewHLL() *HLL {
	return &HLL{}
}

// Add inserts a value into the HLL. Returns true if the value changed a
// register (likely new), false otherwise (likely duplicate).
func (h *HLL) Add(value string) bool {
	hv := xxhash.Sum64String(value)
	idx := hv >> (64 - hllPrecision)
	// Shift out the index bits and set a sentinel bit to cap rho at 64-hllPrecision+1.
	w := (hv << hllPrecision) | (uint64(1) << (hllPrecision - 1))
	rho := uint8(bits.LeadingZeros64(w) + 1)

	if rho > h.registers[idx] {
		h.registers[idx] = rho
		return true
	}
	return false
}

// Count returns the estimated cardinality.
func (h *HLL) Count() uint64 {
	m := float64(hllRegisterCount)
	alpha := 0.7213 / (1.0 + 1.079/m)

	sum := 0.0
	zeros := 0
	for _, val := range h.registers {
		sum += math.Pow(2, -float64(val))
		if val == 0 {
			zeros++
		}
	}

	estimate := alpha * m * m / sum

	// Small range correction: linear counting when many registers are empty.
	if estimate <= 2.5*m && zeros > 0 {
		estimate = m * math.Log(m/float64(zeros))
	}

	// A payload whose registers are all at hllMaxRegisterValue drives the
	// estimate past what uint64 holds (~2.7e19), where the float-to-integer
	// conversion is implementation-defined and two replicas on different CPUs
	// could read the same registers as different counts. Saturate instead.
	rounded := math.Round(estimate)
	if rounded >= float64(math.MaxUint64) {
		return math.MaxUint64
	}
	return uint64(rounded)
}

// Merge performs max-merge with another HLL. This operation is commutative,
// associative, and idempotent — suitable as a CRDT merge function.
func (h *HLL) Merge(other *HLL) {
	for i := range h.registers {
		if other.registers[i] > h.registers[i] {
			h.registers[i] = other.registers[i]
		}
	}
}

// Bytes returns the raw register array for serialization.
func (h *HLL) Bytes() []byte {
	return h.registers[:]
}

// Restore loads register data from bytes. Returns an error if the data length
// does not match the expected register count.
//
// A register above hllMaxRegisterValue cannot come from Add, so it is clamped
// to that bound instead of rejected: Restore also decodes snapshots and
// operations already in storage, which a client may have written before the
// bound existed, and rejecting those would make the document unloadable rather
// than repair it. Clamping is deterministic and idempotent, so replicas that
// restore the same payload still converge, and re-serializing the counter
// writes the repaired registers back.
func (h *HLL) Restore(data []byte) error {
	if len(data) != hllRegisterCount {
		return fmt.Errorf("invalid HLL register payload: got %d bytes, want %d", len(data), hllRegisterCount)
	}
	for i, val := range data {
		h.registers[i] = min(val, hllMaxRegisterValue)
	}
	return nil
}
