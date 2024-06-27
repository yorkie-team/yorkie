//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

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

func TestRESTAPI(t *testing.T) {
	t.Run("bulk document retrieval test", func(t *testing.T) {
		cli := http.Client{}

		url := "http://" + defaultServer.RPCAddr() + "/yorkie.v1.AdminService/GetDocument"
		reader := strings.NewReader(`{"project_name": "test", "document_key": "test"}`)
		res, err := cli.Post(url, "application/json", reader)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, res.StatusCode)
	})
}
