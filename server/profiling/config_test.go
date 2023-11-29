/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package profiling_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/profiling"
)

func TestConfig(t *testing.T) {
	scenarios := []*struct {
		config   *profiling.Config
		expected error
	}{
		{config: &profiling.Config{Port: -1}, expected: profiling.ErrInvalidProfilingPort},
		{config: &profiling.Config{Port: 0}, expected: profiling.ErrInvalidProfilingPort},
		{config: &profiling.Config{Port: 8081}, expected: nil},
	}
	for _, scenario := range scenarios {
		assert.ErrorIs(t, scenario.config.Validate(), scenario.expected, "provided config: %#v", scenario.config)
	}
}
