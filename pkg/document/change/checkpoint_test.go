/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package change_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/change"
)

func TestCheckPoint(t *testing.T) {
	t.Run("check point test", func(t *testing.T) {
		initialServerSeq := change.NewServerSeq(change.InitialServerSeq, change.InitialServerSeq)
		cp := change.NewCheckpoint(initialServerSeq, change.InitialClientSeq)
		assert.True(t, cp.Equals(change.NewCheckpoint(change.NewServerSeq(0, 0), 0)))
		assert.False(t, cp.Equals(change.NewCheckpoint(change.NewServerSeq(1, 1), 1)))
		assert.False(t, cp.Equals(change.NewCheckpoint(change.NewServerSeq(1, 0), 0)))
		assert.False(t, cp.Equals(change.NewCheckpoint(change.NewServerSeq(0, 1), 1)))
		assert.False(t, cp.Equals(change.NewCheckpoint(change.NewServerSeq(1, 1), 1)))
		assert.Equal(t, "opSeq=0, prSeq=0, clientSeq=0", cp.String())

		assert.Equal(t, cp, cp.NextServerSeq(change.NewServerSeq(0, 0)))
		assert.Equal(t, change.NewCheckpoint(change.NewServerSeq(5, 5), 0), cp.NextServerSeq(change.NewServerSeq(5, 5)))
		assert.Equal(t, change.NewCheckpoint(change.NewServerSeq(0, 0), 1), cp.NextClientSeq())
		assert.Equal(t, change.NewCheckpoint(change.NewServerSeq(0, 0), 0), cp.IncreaseClientSeq(0))
		assert.Equal(t, change.NewCheckpoint(change.NewServerSeq(0, 0), 5), cp.IncreaseClientSeq(5))

		cp = change.NewCheckpoint(change.NewServerSeq(10, 10), 20)
		assert.Equal(t, change.NewCheckpoint(change.NewServerSeq(10, 10), 20), cp.SyncClientSeq(5))
		assert.Equal(t, change.NewCheckpoint(change.NewServerSeq(10, 10), 30), cp.SyncClientSeq(30))

		assert.Equal(t, cp, cp.Forward(change.NewCheckpoint(change.NewServerSeq(1, 2), 2)))
		assert.Equal(t, change.NewCheckpoint(change.NewServerSeq(20, 30), 30),
			cp.Forward(change.NewCheckpoint(change.NewServerSeq(20, 30), 30)))
		assert.Equal(t, change.NewCheckpoint(change.NewServerSeq(10, 30), 30),
			cp.Forward(change.NewCheckpoint(change.NewServerSeq(5, 30), 30)))
		assert.Equal(t, change.NewCheckpoint(change.NewServerSeq(20, 10), 20),
			cp.Forward(change.NewCheckpoint(change.NewServerSeq(20, 5), 5)))
	})
}
