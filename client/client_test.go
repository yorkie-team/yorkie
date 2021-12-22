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

package client_test

import (
	"testing"

	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/types"
)

func TestClient(t *testing.T) {
	t.Run("create instance test", func(t *testing.T) {
		opt := client.Option{
			Token:    xid.New().String(),
			Metadata: types.Metadata{"Name": "ClientName"},
		}
		assert.Empty(t, opt.Key)

		cli, err := client.NewClient(opt)
		assert.NoError(t, err)

		assert.Equal(t, opt.Metadata, cli.Metadata())
		assert.Empty(t, opt.Key)
	})
}
