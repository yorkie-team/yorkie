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
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
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
		assert.Nil(t, summary.Document.Presences)
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

	t.Run("bulk document retrieval with options test", func(t *testing.T) {
		numDocs, clientsPerDoc := 3, 1

		testCases := []struct {
			name             string
			includeRoot      bool
			includePresences bool
		}{
			{"include_root=0,include_presences=0", false, false},
			{"include_root=1,include_presences=0", true, false},
			{"include_root=0,include_presences=1", false, true},
			{"include_root=1,include_presences=1", true, true},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				ctx := context.Background()
				project, err := defaultServer.CreateProject(ctx, t.Name())
				assert.NoError(t, err)

				cli, err := client.Dial(defaultServer.RPCAddr(), client.WithAPIKey(project.PublicKey))
				assert.NoError(t, err)
				assert.NoError(t, cli.Activate(ctx))

				var docs []*document.Document
				for i := range numDocs {
					doc := document.New(helper.TestDocKey(t, i))
					assert.NoError(t, cli.Attach(ctx, doc,
						client.WithInitialRoot(yson.ParseObject(`{"counter": Counter(Long(0))}`)),
						client.WithPresence(innerpresence.Presence{"key": cli.Key()}),
						client.WithRealtimeSync()))

					docs = append(docs, doc)
				}

				defer func() {
					for _, doc := range docs {
						assert.NoError(t, cli.Detach(ctx, doc))
					}
					assert.NoError(t, cli.Close())
				}()

				assert.NoError(t, cli.Sync(ctx))
				res := post(
					t,
					project,
					fmt.Sprintf("http://%s/yorkie.v1.AdminService/GetDocuments", defaultServer.RPCAddr()),
					fmt.Sprintf(`{"project_name": "%s", "document_keys": ["%s", "%s", "%s"], "include_root": %t, "include_presences": %t}`,
						project.Name, docs[0].Key(), docs[1].Key(), docs[2].Key(), tc.includeRoot, tc.includePresences),
				)

				summaries := &documentSummaries{}
				gojson.Unmarshal(res, summaries)
				assert.Len(t, summaries.Documents, numDocs)

				for _, docSummary := range summaries.Documents {
					if tc.includeRoot {
						assert.Equal(t, `{"counter":0}`, docSummary.Root)
					} else {
						assert.Empty(t, docSummary.Root)
					}

					if tc.includePresences {
						assert.Len(t, docSummary.Presences, clientsPerDoc)
						assert.Contains(t, docSummary.Presences, cli.ID().String())
					} else {
						assert.Nil(t, docSummary.Presences)
					}
				}
			})
		}
	})

	t.Run("list documents test", func(t *testing.T) {
		project := helper.CreateProject(t, defaultServer, t.Name())
		cli1, err := client.Dial(defaultServer.RPCAddr(), client.WithAPIKey(project.PublicKey))
		assert.NoError(t, err)
		defer cli1.Close()
		cli2, err := client.Dial(defaultServer.RPCAddr(), client.WithAPIKey(project.PublicKey))
		assert.NoError(t, err)
		defer cli2.Close()

		ctx := context.Background()
		assert.NoError(t, cli1.Activate(ctx))
		assert.NoError(t, cli2.Activate(ctx))

		key1, key2 := helper.TestDocKey(t, 1), helper.TestDocKey(t, 2)
		doc1, doc2 := document.New(key1), document.New(key1)
		assert.NoError(t, cli1.Attach(ctx, doc1))
		assert.NoError(t, cli2.Attach(ctx, doc2))
		doc3 := document.New(key2)
		assert.NoError(t, cli1.Attach(ctx, doc3))

		assert.NoError(t, cli1.Sync(ctx))
		{
			res := post(
				t,
				project,
				fmt.Sprintf("http://%s/yorkie.v1.AdminService/ListDocuments", defaultServer.RPCAddr()),
				fmt.Sprintf(`{"project_name": "%s"}`, project.Name),
			)

			summaries := &documentSummaries{}
			assert.NoError(t, gojson.Unmarshal(res, summaries))
			assert.Len(t, summaries.Documents, 2)
			for _, doc := range summaries.Documents {
				assert.Contains(t, []key.Key{key1, key2}, doc.Key)
				if doc.Key == key1 {
					assert.Equal(t, 2, doc.AttachedClients)
				} else {
					assert.Equal(t, 1, doc.AttachedClients)
				}
			}
		}
		assert.NoError(t, cli1.Deactivate(ctx))
		{
			res := post(
				t,
				project,
				fmt.Sprintf("http://%s/yorkie.v1.AdminService/ListDocuments", defaultServer.RPCAddr()),
				fmt.Sprintf(`{"project_name": "%s"}`, project.Name),
			)

			summaries := &documentSummaries{}
			assert.NoError(t, gojson.Unmarshal(res, summaries))
			assert.Len(t, summaries.Documents, 2)
			for _, doc := range summaries.Documents {
				assert.Contains(t, []key.Key{key1, key2}, doc.Key)
				if doc.Key == key1 {
					assert.Equal(t, 1, doc.AttachedClients)
				} else {
					assert.Equal(t, 0, doc.AttachedClients)
				}
			}
		}
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
}

// post sends a POST request to the given URL with the given body.
func post(t *testing.T, project *types.Project, url, body string) []byte {
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	assert.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(
		types.AuthorizationKey,
		fmt.Sprintf("%s %s", types.AuthSchemeAPIKey, project.SecretKey),
	)

	httpClient := http.Client{}
	res, err := httpClient.Do(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)

	resBody, err := io.ReadAll(res.Body)
	assert.NoError(t, err)
	return resBody
}
