/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

package housekeeping_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
)

func TestConfig(t *testing.T) {
	t.Run("validate test", func(t *testing.T) {
		validConf := housekeeping.Config{
			Interval:                  "1m",
			CandidatesLimitPerProject: 100,
			ProjectFetchSize:          100,
		}
		assert.NoError(t, validConf.Validate())

		conf1 := validConf
		conf1.Interval = "hour"
		assert.Error(t, conf1.Validate())

		conf2 := validConf
		conf2.CandidatesLimitPerProject = 0
		assert.Error(t, conf2.Validate())

		conf3 := validConf
		conf3.ProjectFetchSize = -1
		assert.Error(t, conf3.Validate())
	})
}
