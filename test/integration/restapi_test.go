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
	"maps"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/test/helper"
)

// documentSummaries is a GetDocuments, ListDocuments or SearchDocuments
// response, decoded the way a client
// would: through protojson and the converter. The wire shape is the proto's —
// each presence is `{"data": {...}}` — which encoding/json cannot map onto
// types.DocumentSummary, and it fills in the map keys before failing, so a
// plain decode with its error dropped looked like it worked.
type documentSummaries struct {
	Documents []*types.DocumentSummary
}

// UnmarshalJSON decodes the `documents` of a GetDocuments, ListDocuments or
// SearchDocuments response. It decodes into SearchDocumentsResponse because
// that message's fields are a superset of the other two by JSON name, so the
// decode stays strict: an unknown field is still an error.
func (s *documentSummaries) UnmarshalJSON(data []byte) error {
	res := &api.SearchDocumentsResponse{}
	if err := protojson.Unmarshal(data, res); err != nil {
		return fmt.Errorf("unmarshal documents response: %w", err)
	}
	s.Documents = converter.FromDocumentSummaries(res.Documents)
	return nil
}

// documentSummary is a GetDocument response; see documentSummaries.
type documentSummary struct {
	Document *types.DocumentSummary
}

// UnmarshalJSON decodes a GetDocumentResponse.
func (s *documentSummary) UnmarshalJSON(data []byte) error {
	res := &api.GetDocumentResponse{}
	if err := protojson.Unmarshal(data, res); err != nil {
		return fmt.Errorf("unmarshal GetDocumentResponse: %w", err)
	}
	// The converter dereferences the summary; a response without one is an
	// assertion failure, not a panic in whichever goroutine decoded it.
	if res.Document == nil {
		return fmt.Errorf("unmarshal GetDocumentResponse: no document in %s", data)
	}
	s.Document = converter.FromDocumentSummary(res.Document)
	return nil
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

				// Each document gets a distinct root: GetDocumentSummaries fills
				// summaries[i] from a goroutine per document, so identical
				// fixtures would let a summary paired with the wrong document
				// pass unnoticed.
				var docs []*document.Document
				expectedRoots := make(map[key.Key]string, numDocs)
				for i := range numDocs {
					doc := document.New(helper.TestKey(t, i))
					assert.NoError(t, cli.Attach(ctx, doc,
						client.WithInitialRoot(yson.ParseObject(
							fmt.Sprintf(`{"counter": Counter(Long(%d))}`, i))),
						client.WithPresence(presence.Data{"key": cli.Key()}),
						client.WithRealtimeSync()))

					docs = append(docs, doc)
					expectedRoots[doc.Key()] = fmt.Sprintf(`{"counter":%d}`, i)
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
				assert.NoError(t, gojson.Unmarshal(res, summaries))
				assert.Len(t, summaries.Documents, numDocs)

				remaining := maps.Clone(expectedRoots)
				for _, docSummary := range summaries.Documents {
					expectedRoot, ok := remaining[docSummary.Key]
					assert.True(t, ok, "unexpected or duplicated key %s", docSummary.Key)
					delete(remaining, docSummary.Key)

					if tc.includeRoot {
						// The root must be the one belonging to THIS key, not
						// merely a well-formed root from some other document.
						assert.Equal(t, expectedRoot, docSummary.Root)
					} else {
						assert.Empty(t, docSummary.Root)
					}

					if tc.includePresences {
						assert.Len(t, docSummary.Presences, clientsPerDoc)
						// The value, not just the key: a decode that fails
						// after filling in keys passed the key check alone.
						assert.Equal(t, presence.Data{"key": cli.Key()}, docSummary.Presences[cli.ID().String()])
					} else {
						assert.Nil(t, docSummary.Presences)
					}
				}
				assert.Empty(t, remaining, "every requested key must appear exactly once")
			})
		}
	})

	t.Run("list documents test", func(t *testing.T) {
		project := helper.CreateProject(t, defaultServer, t.Name())
		cli1, err := client.Dial(defaultServer.RPCAddr(), client.WithAPIKey(project.PublicKey))
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli1.Close()) }()
		cli2, err := client.Dial(defaultServer.RPCAddr(), client.WithAPIKey(project.PublicKey))
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli2.Close()) }()

		ctx := context.Background()
		assert.NoError(t, cli1.Activate(ctx))
		assert.NoError(t, cli2.Activate(ctx))

		key1, key2 := helper.TestKey(t, 1), helper.TestKey(t, 2)
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
		defer func() { assert.NoError(t, cli.Close()) }()

		doc := document.New(docs[0].Key())
		assert.NoError(t, cli.Attach(ctx, doc))
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetYSON(yson.Object{"arr": yson.Array{}})
			return nil
		}))
		assert.NoError(t, cli.Sync(ctx))

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
		assert.NoError(t, cli.Sync(ctx))

		wg := sync.WaitGroup{}
		for range 10 {
			wg.Go(func() {
				res := post(
					t,
					project,
					fmt.Sprintf("http://%s/yorkie.v1.AdminService/GetDocument", defaultServer.RPCAddr()),
					fmt.Sprintf(`{"project_name": "%s", "document_key": "%s"}`, project.Name, docs[0].Key()),
				)
				summary := &documentSummary{}
				assert.NoError(t, gojson.Unmarshal(res, summary))
				assert.Equal(t, `{"arr":[1]}`, summary.Document.Root)
			})
		}
		wg.Wait()
	})

	t.Run("broadcast by admin test", func(t *testing.T) {
		ctx := context.Background()
		project := helper.CreateProject(t, defaultServer, t.Name())

		cli, err := client.Dial(
			defaultServer.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
		)
		require.NoError(t, err)
		require.NoError(t, cli.Activate(ctx))
		defer func() {
			assert.NoError(t, cli.Deactivate(ctx))
			assert.NoError(t, cli.Close())
		}()

		ch, err := channel.New(key.Key("room-1"))
		require.NoError(t, err)
		require.NoError(t, cli.Attach(ctx, ch))

		eventCh := make(chan []byte, 1)
		ch.SubscribeBroadcastEvent(
			"mention",
			func(_ string, _ string, payload []byte) error {
				eventCh <- payload
				return nil
			},
		)

		countCh, closeWatch, err := cli.WatchChannel(ctx, ch)
		require.NoError(t, err)
		defer closeWatch()

		select {
		case <-countCh:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for channel watch initialization")
		}

		res := post(
			t,
			project,
			fmt.Sprintf("http://%s/yorkie.v1.AdminService/BroadcastByAdmin", defaultServer.RPCAddr()),
			`{"channel_key":"room-1","topic":"mention","payload":"InlvcmtpZSI="}`,
		)

		var ack map[string]any
		assert.NoError(t, gojson.Unmarshal(res, &ack))
		assert.Empty(t, ack)

		select {
		case payload := <-eventCh:
			var value string
			assert.NoError(t, gojson.Unmarshal(payload, &value))
			assert.Equal(t, "yorkie", value)
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for admin broadcast")
		}
	})

	t.Run("auth scheme mismatch test", func(t *testing.T) {
		project := helper.CreateProject(t, defaultServer, helper.TestSlugName(t))
		token := logIn(t)

		// A user-scoped method takes the session token. Answering a secret key
		// with PermissionDenied is the point: before, the handler read a user
		// the API-Key branch never put in the context and panicked, so the
		// caller got a connection reset instead of any response.
		assertPermissionDenied(
			t,
			fmt.Sprintf("http://%s/yorkie.v1.AdminService/GetProject", defaultServer.RPCAddr()),
			fmt.Sprintf("%s %s", types.AuthSchemeAPIKey, project.SecretKey),
			fmt.Sprintf(`{"name": "%s"}`, project.Name),
		)

		// And the other direction: a project-scoped method takes the secret key.
		assertPermissionDenied(
			t,
			fmt.Sprintf("http://%s/yorkie.v1.AdminService/ListDocuments", defaultServer.RPCAddr()),
			fmt.Sprintf("%s %s", types.AuthSchemeBearer, token),
			`{"page_size": 1}`,
		)

		// The matching scheme still works; post asserts 200.
		post(
			t,
			project,
			fmt.Sprintf("http://%s/yorkie.v1.AdminService/ListDocuments", defaultServer.RPCAddr()),
			`{"page_size": 1}`,
		)

		// GetServerVersion reads neither scope, so both credentials keep
		// reaching it rather than being turned away by the scheme gate.
		versionURL := fmt.Sprintf("http://%s/yorkie.v1.AdminService/GetServerVersion", defaultServer.RPCAddr())
		post(t, project, versionURL, `{}`)
		assertOK(t, versionURL, fmt.Sprintf("%s %s", types.AuthSchemeBearer, token), `{}`)
	})
}

// assertOK sends a POST request with the given authorization header and
// asserts that it is answered with 200.
func assertOK(t *testing.T, url, authHeader, body string) {
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	assert.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(types.AuthorizationKey, authHeader)

	res, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	defer func() { assert.NoError(t, res.Body.Close()) }()
	assert.Equal(t, http.StatusOK, res.StatusCode)
}

// logIn logs the default admin in over the REST API and returns its token.
func logIn(t *testing.T) string {
	req, err := http.NewRequest(
		"POST",
		fmt.Sprintf("http://%s/yorkie.v1.AdminService/LogIn", defaultServer.RPCAddr()),
		strings.NewReader(fmt.Sprintf(`{"username": "%s", "password": "%s"}`, helper.AdminUser, helper.AdminPassword)),
	)
	assert.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	defer func() { assert.NoError(t, res.Body.Close()) }()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	body, err := io.ReadAll(res.Body)
	assert.NoError(t, err)

	var decoded struct {
		Token string `json:"token"`
	}
	assert.NoError(t, gojson.Unmarshal(body, &decoded))
	assert.NotEmpty(t, decoded.Token)
	return decoded.Token
}

// assertPermissionDenied sends a POST request with the given authorization
// header and asserts that it is answered with a permission_denied error.
func assertPermissionDenied(t *testing.T, url, authHeader, body string) {
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	assert.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(types.AuthorizationKey, authHeader)

	res, err := http.DefaultClient.Do(req)
	// A reset connection fails here, before the status is read.
	assert.NoError(t, err)
	defer func() { assert.NoError(t, res.Body.Close()) }()
	assert.Equal(t, http.StatusForbidden, res.StatusCode)

	resBody, err := io.ReadAll(res.Body)
	assert.NoError(t, err)

	var decoded struct {
		Code string `json:"code"`
	}
	assert.NoError(t, gojson.Unmarshal(resBody, &decoded))
	assert.Equal(t, "permission_denied", decoded.Code)
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
