// +build integration

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

package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/test/helper"
)

func TestClusterMode(t *testing.T) {
	t.Run("member list test", func(t *testing.T) {
		agentA := helper.TestYorkie(100)
		agentB := helper.TestYorkie(200)

		assert.NoError(t, agentA.Start())
		assert.NoError(t, agentB.Start())

		time.Sleep(time.Second)

		assert.Len(t, defaultYorkie.Members(), 3)
		assert.Equal(t, agentA.Members(), agentB.Members())

		assert.NoError(t, agentA.Shutdown(true))
		assert.Len(t, defaultYorkie.Members(), 2)
		assert.NoError(t, agentB.Shutdown(true))
		assert.Len(t, defaultYorkie.Members(), 1)
	})
}
