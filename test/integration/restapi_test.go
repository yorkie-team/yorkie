//go:build integration

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

package integration

import (
	"context"
	gojson "encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/test/helper"
)

// documentSummaries represents a list of document documentSummaries.
type documentSummaries struct {
	Documents []*types.DocumentSummary `json:"documents"`
}

// documentSummary represents a summary of a document.
type documentSummary struct {
	Document *types.DocumentSummary `json:"document"`
}

func TestRESTAPI(t *testing.T) {
	t.Run("document retrieval test", func(t *testing.T) {
		project, docs := helper.CreateProjectAndDocuments(t, defaultServer, 3)
		res := post(
			t,
			project,
			fmt.Sprintf("http://%s/yorkie.v1.AdminService/GetDocument", defaultServer.RPCAddr()),
			fmt.Sprintf(`{"project_name": "%s", "document_key": "%s"}`, project.Name, docs[0].Key()),
		)

		summary := &documentSummary{}
		assert.NoError(t, gojson.Unmarshal(res, summary))
		assert.Equal(t, docs[0].Key(), summary.Document.Key)
	})

	t.Run("bulk document retrieval test", func(t *testing.T) {
		project, docs := helper.CreateProjectAndDocuments(t, defaultServer, 3)
		res := post(
			t,
			project,
			fmt.Sprintf("http://%s/yorkie.v1.AdminService/GetDocuments", defaultServer.RPCAddr()),
			fmt.Sprintf(`{"project_name": "%s", "document_keys": ["%s", "%s"]}`, project.Name, docs[0].Key(), docs[1].Key()),
		)

		summaries := &documentSummaries{}
		assert.NoError(t, gojson.Unmarshal(res, summaries))
		assert.Len(t, summaries.Documents, 2)
	})

	t.Run("list documents test", func(t *testing.T) {
		project, _ := helper.CreateProjectAndDocuments(t, defaultServer, 3)
		res := post(
			t,
			project,
			fmt.Sprintf("http://%s/yorkie.v1.AdminService/ListDocuments", defaultServer.RPCAddr()),
			fmt.Sprintf(`{"project_name": "%s", "document_key": "test"}`, project.Name),
		)

		summaries := &documentSummaries{}
		assert.NoError(t, gojson.Unmarshal(res, summaries))
		assert.Len(t, summaries.Documents, 3)
	})

	t.Run("search documents test", func(t *testing.T) {
		project, docs := helper.CreateProjectAndDocuments(t, defaultServer, 3)
		res := post(
			t,
			project,
			fmt.Sprintf("http://%s/yorkie.v1.AdminService/SearchDocuments", defaultServer.RPCAddr()),
			fmt.Sprintf(`{"project_name": "%s", "query": "0-", "page_size": 3}`, project.Name),
		)
		summaries := &documentSummaries{}
		assert.NoError(t, gojson.Unmarshal(res, summaries))
		assert.Len(t, summaries.Documents, 1)

		_ = post(
			t,
			project,
			fmt.Sprintf("http://%s/yorkie.v1.AdminService/RemoveDocumentByAdmin", defaultServer.RPCAddr()),
			fmt.Sprintf(`{"project_name": "%s", "document_key": "%s", "force": true}`, project.Name, docs[0].Key()),
		)

		res = post(
			t,
			project,
			fmt.Sprintf("http://%s/yorkie.v1.AdminService/SearchDocuments", defaultServer.RPCAddr()),
			fmt.Sprintf(`{"project_name": "%s", "query": "0-", "page_size": 3}`, project.Name),
		)
		summaries = &documentSummaries{}
		assert.NoError(t, gojson.Unmarshal(res, summaries))
		assert.Len(t, summaries.Documents, 0)
	})

	t.Run("concurrent document retrieval test", func(t *testing.T) {
		project, docs := helper.CreateProjectAndDocuments(t, defaultServer, 1)

		res := post(
			t,
			project,
			fmt.Sprintf("http://%s/yorkie.v1.AdminService/GetDocument", defaultServer.RPCAddr()),
			fmt.Sprintf(`{"project_name": "%s", "document_key": "%s"}`, project.Name, docs[0].Key()),
		)
		summary := &documentSummary{}
		assert.NoError(t, gojson.Unmarshal(res, summary))
		assert.Equal(t, "{}", summary.Document.Root)

		ctx := context.Background()

		cli, err := client.Dial(defaultServer.RPCAddr(), client.WithAPIKey(project.PublicKey))
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(ctx))
		defer cli.Close()

		doc := document.New(docs[0].Key())
		assert.NoError(t, cli.Attach(ctx, doc))
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetYSON(yson.Object{"arr": yson.Array{}})
			return nil
		}))
		cli.Sync(ctx)

		res = post(
			t,
			project,
			fmt.Sprintf("http://%s/yorkie.v1.AdminService/GetDocument", defaultServer.RPCAddr()),
			fmt.Sprintf(`{"project_name": "%s", "document_key": "%s"}`, project.Name, docs[0].Key()),
		)
		assert.NoError(t, gojson.Unmarshal(res, summary))
		assert.Equal(t, `{"arr":[]}`, summary.Document.Root)

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetArray("arr").AddInteger(1)
			return nil
		}))
		cli.Sync(ctx)

		wg := sync.WaitGroup{}
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res := post(
					t,
					project,
					fmt.Sprintf("http://%s/yorkie.v1.AdminService/GetDocument", defaultServer.RPCAddr()),
					fmt.Sprintf(`{"project_name": "%s", "document_key": "%s"}`, project.Name, docs[0].Key()),
				)
				summary := &documentSummary{}
				assert.NoError(t, gojson.Unmarshal(res, summary))
				assert.Equal(t, `{"arr":[1]}`, summary.Document.Root)
			}()
		}
		wg.Wait()
	})

	t.Run("document api access control test", func(t *testing.T) {
		project1 := helper.CreateProject(t, defaultServer, t.Name()+"1")
		project2 := helper.CreateProject(t, defaultServer, t.Name()+"2")

		postWithUnauthorizedErrorCheck(
			t,
			project1,
			fmt.Sprintf("http://%s/yorkie.v1.AdminService/GetDocument", defaultServer.RPCAddr()),
			fmt.Sprintf(`{"project_name": "%s", "document_key": "%s"}`, project2.Name, ""),
		)
	})
}

// post sends a POST request to the given URL with the given body.
func post(t *testing.T, project *types.Project, url, body string) []byte {
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	assert.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", project.SecretKey)

	httpClient := http.Client{}
	res, err := httpClient.Do(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)

	resBody, err := io.ReadAll(res.Body)
	assert.NoError(t, err)
	return resBody
}

func postWithUnauthorizedErrorCheck(t *testing.T, project *types.Project, url, body string) {
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	assert.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", project.SecretKey)

	httpClient := http.Client{}
	res, err := httpClient.Do(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
}
