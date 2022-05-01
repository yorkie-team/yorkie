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

	"github.com/yorkie-team/yorkie/test/helper"
	"github.com/yorkie-team/yorkie/yorkie/profiling"
)

var (
	// to avoid conflict with profiling port used for client test
	testProfilingPort = helper.ProfilingPort + 100
)

func TestMetricsServer(t *testing.T) {
	t.Run("new server test", func(t *testing.T) {
		server := profiling.NewServer(&profiling.Config{Port: testProfilingPort}, nil)
		assert.NotNil(t, server)
		server.Shutdown(true)
	})
}
