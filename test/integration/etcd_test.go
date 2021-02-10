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

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/test/helper"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync/etcd"
)

func TestETCD(t *testing.T) {
	t.Run("new and close test", func(t *testing.T) {
		cli, err := etcd.Dial(&etcd.Config{
			Endpoints: helper.ETCDEndpoints,
		})
		assert.NoError(t, err)

		defer func() {
			err := cli.Close()
			assert.NoError(t, err)
		}()
	})
}
