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
	// Shift out the index bits; rho = position of leftmost 1-bit, 1-indexed.
	w := hv << hllPrecision
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

	return uint64(math.Round(estimate))
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

// Restore loads register data from bytes.
func (h *HLL) Restore(data []byte) {
	copy(h.registers[:], data)
}
