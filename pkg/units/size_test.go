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

package units_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/units"
)

// decimalAbbrs and binaryAbbrs mirror the unexported abbreviation tables of the
// package, so that CustomSize can be exercised from outside it.
var (
	decimalAbbrs = []string{"B", "kB", "MB", "GB", "TB", "PB", "EB", "ZB", "YB"}
	binaryAbbrs  = []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB", "ZiB", "YiB"}
)

func TestFromHumanSize(t *testing.T) {
	tests := []struct {
		desc     string
		input    string
		expected int64
	}{
		{"zero", "0", 0},
		{"no suffix", "32", 32},
		{"lowercase byte suffix", "32b", 32},
		{"uppercase byte suffix", "32B", 32},
		{"one below the decimal boundary", "999", 999},
		{"exactly the decimal boundary", "1000", 1000},
		{"the binary boundary in plain bytes", "1024", 1024},
		{"kilo, bare", "32k", 32000},
		{"kilo, uppercase", "32K", 32000},
		{"kilo, with byte suffix", "32kb", 32000},
		{"kilo, SI spelling", "32kB", 32000},
		{"one kilobyte", "1kB", 1000},
		{"space separated", "32 kB", 32000},
		{"trailing space, no suffix", "32 ", 32},
		// The multiplication happens in float64 and the result is truncated,
		// not rounded, so 32.3 * 1000 == 32299.999999999996 lands one short of
		// 32300. See the PR body: the behaviour is pinned here, not fixed.
		{"fractional kilo truncates one short", "32.3kB", 32299},
		{"fraction truncated to an integer", "32.3", 32},
		{"mega", "32M", 32000000},
		{"giga", "2GB", 2000000000},
		{"tera", "1TB", 1000000000000},
		{"peta", "1PB", 1000000000000000},
		// Decimal and binary spellings are not distinguished: the suffix
		// selects the multiplier table, the "i" is only validated.
		{"binary spelling still uses decimal units", "32KiB", 32000},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			size, err := units.FromHumanSize(test.input)
			assert.NoError(t, err)
			assert.Equal(t, test.expected, size)
		})
	}
}

func TestFromHumanSizeWithInvalidInput(t *testing.T) {
	tests := []struct {
		desc  string
		input string
	}{
		{"empty", ""},
		{"no digit", "hello"},
		{"suffix only", "kB"},
		{"negative", "-32"},
		{"negative with a suffix", "-32kB"},
		{"two decimal points", "32.3.3"},
		{"digits after the suffix", "32kB4"},
		{"unknown suffix", "32X"},
		{"byte suffix with trailing characters", "32bb"},
		{"missing the unit letter", "32iB"},
		{"unit letter followed by a non-byte", "32ki"},
		{"doubled byte suffix", "32kbb"},
		{"suffix too long", "32kbbb"},
		{"exa is out of the table", "1EB"},
		{"space inside the number", "3 2kB"},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			size, err := units.FromHumanSize(test.input)
			assert.Error(t, err)
			assert.Equal(t, int64(-1), size)
		})
	}
}

func TestRAMInBytes(t *testing.T) {
	tests := []struct {
		desc     string
		input    string
		expected int64
	}{
		{"zero", "0", 0},
		{"no suffix", "32", 32},
		{"byte suffix", "32b", 32},
		{"one below the binary boundary", "1023", 1023},
		{"exactly the binary boundary", "1024", 1024},
		{"the decimal boundary in plain bytes", "1000", 1000},
		{"kibi, bare", "32k", 32768},
		{"kibi, uppercase", "32K", 32768},
		{"kibi, with byte suffix", "32kb", 32768},
		{"kibi, IEC spelling", "32KiB", 32768},
		{"kibi, IEC spelling lowercased", "32kib", 32768},
		{"one kibibyte", "1KiB", 1024},
		{"space separated", "32 KiB", 32768},
		{"fractional kibi truncates", "32.3KiB", 33075},
		{"mebi", "1MiB", 1048576},
		{"gibi", "1GiB", 1073741824},
		{"tebi", "1TiB", 1099511627776},
		{"pebi", "1PiB", 1125899906842624},
		// Decimal spellings are accepted too, with binary multipliers.
		{"decimal spelling still uses binary units", "32MB", 33554432},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			size, err := units.RAMInBytes(test.input)
			assert.NoError(t, err)
			assert.Equal(t, test.expected, size)
		})
	}
}

func TestRAMInBytesWithInvalidInput(t *testing.T) {
	tests := []struct {
		desc  string
		input string
	}{
		{"empty", ""},
		{"no digit", "hello"},
		{"suffix only", "KiB"},
		{"negative", "-32"},
		{"negative with a suffix", "-32KiB"},
		{"two decimal points", "32.3.3"},
		{"unknown suffix", "32X"},
		{"missing the unit letter", "32iB"},
		{"unit letter followed by a non-byte", "32ki"},
		{"doubled byte suffix", "32kbb"},
		{"suffix too long", "32kbbb"},
		{"exa is out of the table", "1EiB"},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			size, err := units.RAMInBytes(test.input)
			assert.Error(t, err)
			assert.Equal(t, int64(-1), size)
		})
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		desc     string
		input    float64
		expected string
	}{
		{"zero", 0, "0B"},
		{"bytes", 32, "32B"},
		{"one below the decimal boundary", 999, "999B"},
		{"exactly the decimal boundary", 1000, "1kB"},
		{"the binary boundary in decimal units", 1024, "1.024kB"},
		{"mega", 1000000, "1MB"},
		{"a mebibyte in decimal units", 1048576, "1.049MB"},
		{"capped at four significant digits", 1000000000, "1GB"},
		{"tera", 1000000000000, "1TB"},
		// Rounding happens after the unit is picked, so a value just under the
		// next unit is reported as "1000kB" rather than "1MB".
		{"rounds up within the unit", 999999, "1000kB"},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			assert.Equal(t, test.expected, units.HumanSize(test.input))
		})
	}
}

func TestHumanSizeWithPrecision(t *testing.T) {
	tests := []struct {
		desc      string
		input     float64
		precision int
		expected  string
	}{
		{"zero at any precision", 0, 3, "0B"},
		{"bytes keep their unit", 999, 3, "999B"},
		{"three digits", 1024, 3, "1.02kB"},
		{"two digits", 1024, 2, "1kB"},
		{"one digit", 1024, 1, "1kB"},
		{"five digits", 1024, 5, "1.024kB"},
		{"a mebibyte at three digits", 1048576, 3, "1.05MB"},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			assert.Equal(t, test.expected, units.HumanSizeWithPrecision(test.input, test.precision))
		})
	}
}

func TestBytesSize(t *testing.T) {
	tests := []struct {
		desc     string
		input    float64
		expected string
	}{
		{"zero", 0, "0B"},
		{"bytes", 32, "32B"},
		{"the decimal boundary stays in bytes", 1000, "1000B"},
		{"one below the binary boundary", 1023, "1023B"},
		{"exactly the binary boundary", 1024, "1KiB"},
		{"mebi", 1048576, "1MiB"},
		{"gibi", 1073741824, "1GiB"},
		{"fractional gibi", 3.5 * 1073741824, "3.5GiB"},
		{"tebi", 1099511627776, "1TiB"},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			assert.Equal(t, test.expected, units.BytesSize(test.input))
		})
	}
}

func TestCustomSize(t *testing.T) {
	tests := []struct {
		desc     string
		format   string
		input    float64
		base     float64
		abbrs    []string
		expected string
	}{
		{"decimal base", "%.2f %s", 1000, 1000.0, decimalAbbrs, "1.00 kB"},
		{"binary base", "%.2f %s", 1024, 1024.0, binaryAbbrs, "1.00 KiB"},
		{"one below the base keeps the first unit", "%.2f %s", 999, 1000.0, decimalAbbrs, "999.00 B"},
		{"no space in the format", "%.4g%s", 1048576, 1024.0, binaryAbbrs, "1MiB"},
		// The unit index stops at the last abbreviation, so the magnitude
		// spills into the number instead of picking a larger unit.
		{"runs out of units", "%.0f %s", 1000000, 1000.0, []string{"B", "kB"}, "1000 kB"},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			assert.Equal(t, test.expected, units.CustomSize(test.format, test.input, test.base, test.abbrs))
		})
	}
}
