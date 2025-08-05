//go:build bench

/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

package bench

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/admin"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/test/helper"
)

func benchmarkGetDocuments(
	b *testing.B,
	ctx context.Context,
	cnt int,
	docKeys []string,
	adminClient v1connect.AdminServiceClient,
	includeRoot bool,
	includePresences bool,
) {
	b.StopTimer()
	docKeys = docKeys[:cnt]
	docRequest := &api.GetDocumentsRequest{
		ProjectName:      "default",
		DocumentKeys:     docKeys,
		IncludeRoot:      includeRoot,
		IncludePresences: includePresences,
	}
	b.StartTimer()
	resp, err := adminClient.GetDocuments(
		ctx,
		connect.NewRequest(docRequest),
	)
	b.StopTimer()

	assert.NoError(b, err)
	assert.NotNil(b, resp.Msg)
	assert.Len(b, resp.Msg.Documents, cnt)
}

func BenchmarkGetDocuments(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	svr := helper.TestServer()
	assert.NoError(b, svr.Start())

	b.Cleanup(func() {
		if err := svr.Shutdown(true); err != nil {
			b.Fatal(err)
		}
	})

	adminConn := http.DefaultClient
	testAdminAuthInterceptor := admin.NewAuthInterceptor("")
	testAdminClient := v1connect.NewAdminServiceClient(
		adminConn,
		"http://"+svr.RPCAddr(),
		connect.WithInterceptors(testAdminAuthInterceptor),
	)

	ctx := context.Background()

	resp, err := testAdminClient.LogIn(
		ctx,
		connect.NewRequest(&api.LogInRequest{
			Username: helper.AdminUser,
			Password: helper.AdminPassword,
		},
		))
	assert.NoError(b, err)
	testAdminAuthInterceptor.SetToken(resp.Msg.Token)

	var docKeys []string
	for i := range 1000 {
		_, doc, err := helper.ClientAndAttachedDoc(ctx, svr.RPCAddr(), helper.TestDocKey(b, i))
		assert.NoError(b, err)
		docKeys = append(docKeys, doc.Key().String())
	}

	b.Run("without root presence 10", func(b *testing.B) {
		for range b.N {
			benchmarkGetDocuments(b, ctx, 10, docKeys, testAdminClient, false, false)
		}
	})

	b.Run("with root presence 10", func(b *testing.B) {
		for range b.N {
			benchmarkGetDocuments(b, ctx, 10, docKeys, testAdminClient, true, true)
		}
	})

	b.Run("without root presence 100", func(b *testing.B) {
		for range b.N {
			benchmarkGetDocuments(b, ctx, 100, docKeys, testAdminClient, false, false)
		}
	})

	b.Run("with root presence 100", func(b *testing.B) {
		for range b.N {
			benchmarkGetDocuments(b, ctx, 100, docKeys, testAdminClient, true, true)
		}
	})

	b.Run("without root presence 1000", func(b *testing.B) {
		for range b.N {
			benchmarkGetDocuments(b, ctx, 1000, docKeys, testAdminClient, false, false)
		}
	})

	b.Run("with root presence 1000", func(b *testing.B) {
		for range b.N {
			benchmarkGetDocuments(b, ctx, 1000, docKeys, testAdminClient, true, true)
		}
	})
}
