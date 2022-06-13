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

package mongo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestClient(t *testing.T) {
	ctx := context.Background()
	config := &mongo.Config{
		ConnectionTimeout: "5s",
		ConnectionURI:     "mongodb://localhost:27017",
		YorkieDatabase:    helper.TestDBName(),
		PingTimeout:       "5s",
	}
	assert.NoError(t, config.Validate())

	cli, err := mongo.Dial(config)
	assert.NoError(t, err)

	t.Run("UpdateProjectInfo test", func(t *testing.T) {
		info, err := cli.CreateProjectInfo(ctx, t.Name())
		assert.NoError(t, err)
		existName := "already"
		_, err = cli.CreateProjectInfo(ctx, existName)
		assert.NoError(t, err)

		id := info.ID
		newName := "changed-name"
		newAuthWebhookURL := "newWebhookURL"
		newAuthWebhookMethods := []string{
			string(types.AttachDocument),
			string(types.WatchDocuments),
		}

		//TODO
		//1. validate 함수가 잘 작동하는지 테스트 - 일부러 에러 내고 의도한 에러 나오는지 확인 ->  api/types/project_field_test.go 에다가 만든다!
		//2. 하나의 필드만 바꿨을 때 하나만 바는지 - 다른 필드는 변경되지 않았는지
		//3. 한번에 여러 필드 변경도 잘 되는지 확인

		// name
		field := &types.ProjectField{
			Name: newName,
		}
		err = field.Validate()
		assert.NoError(t, err)
		res, err := cli.UpdateProjectInfo(ctx, id, field)
		assert.NoError(t, err)

		updateInfo, err := cli.FindProjectInfoByID(ctx, id)
		assert.NoError(t, err)

		// TODO: 다른 필드는 변경되지 않음 확인
		assert.Equal(t, res.Name, newName)
		assert.Equal(t, updateInfo.Name, newName)

		// total
		field = &types.ProjectField{
			Name:               newName,
			AuthWebhookURL:     newAuthWebhookURL,
			AuthWebhookMethods: newAuthWebhookMethods,
		}

		err = field.Validate()
		assert.NoError(t, err)
		res, err = cli.UpdateProjectInfo(ctx, id, field)
		assert.NoError(t, err)

		updateInfo, err = cli.FindProjectInfoByID(ctx, id)
		assert.NoError(t, err)

		assert.Equal(t, res.AuthWebhookMethods, newAuthWebhookMethods)
		assert.Equal(t, updateInfo.AuthWebhookMethods, newAuthWebhookMethods)

		// update exist name
		dupField := &types.ProjectField{Name: existName}
		_, err = cli.UpdateProjectInfo(ctx, id, dupField)
		assert.ErrorIs(t, err, database.ErrProjectNameAlreadyExists)
	})
}
