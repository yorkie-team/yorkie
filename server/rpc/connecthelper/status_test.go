/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

package connecthelper

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/yorkie-team/yorkie/api/converter"
)

func TestStatus(t *testing.T) {
	t.Run("errorToConnectCode test", func(t *testing.T) {
		status, ok := errorToConnectError(converter.ErrPackRequired)
		assert.True(t, ok)
		assert.Equal(t, status.Code(), connect.CodeInvalidArgument)

		status, ok = errorToConnectError(mongo.CommandError{})
		assert.False(t, ok)
		assert.Nil(t, status)
	})
}
