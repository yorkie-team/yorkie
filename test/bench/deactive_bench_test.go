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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/test/helper"
)

func benchmarkDeactivate(
	b *testing.B,
	ctx context.Context,
	svr *server.Yorkie,
	totalDocCount int,
	attachedDocCount int,
) {
	b.StopTimer()
	c, err := client.Dial(svr.RPCAddr())
	assert.NoError(b, err)
	defer c.Close()

	err = c.Activate(ctx)
	assert.NoError(b, err)
	for i := range totalDocCount {
		d := document.New(helper.TestDocKey(b, i))
		err = c.Attach(ctx, d)
		assert.NoError(b, err)
		if i >= attachedDocCount {
			err = c.Detach(ctx, d)
			assert.NoError(b, err)
		}
	}

	b.StartTimer()
	err = c.Deactivate(ctx)
	b.StopTimer()
	assert.NoError(b, err)
}

func BenchmarkDeactivate(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	svr := helper.TestServer()
	assert.NoError(b, svr.Start())

	b.Cleanup(func() {
		if err := svr.Shutdown(true); err != nil {
			b.Fatal(err)
		}
	})

	ctx := context.Background()
	b.Run("deactivate with 100 total 10 attached", func(b *testing.B) {
		for range b.N {
			benchmarkDeactivate(b, ctx, svr, 100, 10)
		}
	})

	b.Run("deactivate with 100 total 100 attached", func(b *testing.B) {
		for range b.N {
			benchmarkDeactivate(b, ctx, svr, 100, 100)
		}
	})
}
