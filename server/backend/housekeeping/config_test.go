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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
)

func TestConfig(t *testing.T) {
	t.Run("validate test", func(t *testing.T) {
		validConf := housekeeping.Config{
			Interval:             "1m",
			CandidatesLimit:      100,
			CompactionMinChanges: 100,
		}
		assert.NoError(t, validConf.Validate())

		conf1 := validConf
		conf1.Interval = "hour"
		assert.Error(t, conf1.Validate())

		conf2 := validConf
		conf2.CandidatesLimit = 0
		assert.Error(t, conf2.Validate())

		conf3 := validConf
		conf3.CompactionMinChanges = 0
		assert.Error(t, conf3.Validate())

		conf4 := validConf
		conf4.LeadershipLeaseDuration = "5d"
		err := conf4.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(),
			fmt.Sprintf(`invalid argument %s for "--housekeeping-leadership-lease-duration"`, conf4.LeadershipLeaseDuration))

		conf6 := validConf
		conf6.LeadershipRenewalInterval = "5"
		err = conf6.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(),
			fmt.Sprintf(`invalid argument %s for "--housekeeping-leadership-renewal-interval"`, conf6.LeadershipRenewalInterval))
	})

	t.Run("parse test", func(t *testing.T) {
		validConf := housekeeping.Config{
			Interval: "50s",
		}
		duration, err := validConf.ParseInterval()
		assert.NoError(t, err)
		assert.Equal(t, "50s", duration.String())

		duration, err = validConf.ParseLeadershipLeaseDuration()
		assert.NoError(t, err)
		assert.Equal(t, "15s", duration.String())

		duration, err = validConf.ParseLeadershipRenewalInterval()
		assert.NoError(t, err)
		assert.Equal(t, "5s", duration.String())

		validConf.LeadershipLeaseDuration = "10s"
		duration, err = validConf.ParseLeadershipLeaseDuration()
		assert.NoError(t, err)
		assert.Equal(t, "10s", duration.String())

		validConf.LeadershipRenewalInterval = "3s"
		duration, err = validConf.ParseLeadershipRenewalInterval()
		assert.NoError(t, err)
		assert.Equal(t, "3s", duration.String())

		conf1 := validConf
		conf1.Interval = "1 hour"
		_, err = conf1.ParseInterval()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse interval")

		conf2 := validConf
		conf2.LeadershipLeaseDuration = "5"
		_, err = conf2.ParseLeadershipLeaseDuration()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse leadership lease duration")

		conf3 := validConf
		conf3.LeadershipRenewalInterval = "3"
		_, err = conf3.ParseLeadershipRenewalInterval()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse leadership renewal interval")
	})
}
