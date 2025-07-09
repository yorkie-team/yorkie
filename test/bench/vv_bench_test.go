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
	"fmt"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/test/helper"
)

func benchmarkVV(b *testing.B, svr *server.Yorkie, clientCnt int) {
	for i := range b.N {
		ctx := context.Background()
		docKey := key.Key(fmt.Sprintf("vv-bench-%d-%d", i, gotime.Now().UnixMilli()))

		// 1. Activate n clients and attach all clients to the document.
		clients, docs, err := helper.ClientsAndAttachedDocs(ctx, svr.RPCAddr(), docKey, clientCnt)
		assert.NoError(b, err)
		c1, cN := clients[0], clients[clientCnt-1]
		d1, dN := docs[0], docs[clientCnt-1]

		// 2.Initialize the text.
		c1.Sync(ctx)
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text")
			return nil
		})
		assert.NoError(b, err)
		c1.Sync(ctx)
		cN.Sync(ctx)
		assert.Equal(b, `{"text":[]}`, d1.Marshal())
		assert.Equal(b, `{"text":[]}`, dN.Marshal())

		// 3. Multi-Client Edit Test
		//  - With n clients connected
		//  - c1 performs text edits
		// Measurements:
		//  - ChangePack size
		//  - Total document(snapshot) size
		//  - PushPull time
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(0, 0, "a")
			return nil
		})
		assert.NoError(b, err)
		assert.Equal(b, `{"text":[{"val":"a"}]}`, d1.Marshal())

		pack := d1.CreateChangePack()
		pbPack, err := converter.ToChangePack(pack)
		assert.NoError(b, err)
		changePackSize := proto.Size(pbPack)
		b.ReportMetric(float64(changePackSize), "1_changepack(bytes)")

		snapshot, err := converter.SnapshotToBytes(d1.RootObject(), d1.AllPresences())
		assert.NoError(b, err)
		snapshotSize := len(snapshot)
		b.ReportMetric(float64(snapshotSize), "2_snapshot(bytes)")

		start := gotime.Now()
		assert.NoError(b, c1.Sync(ctx))
		assert.NoError(b, cN.Sync(ctx))
		duration := gotime.Since(start).Milliseconds()
		assert.Equal(b, `{"text":[{"val":"a"}]}`, dN.Marshal())
		b.ReportMetric(float64(duration), "3_pushpull(ms)")

		// 4. All clients detach from the document.
		helper.CleanupClients(b, clients)

		// 5. New clients attach to the document.
		// Measurements:
		//  - Attach time (to load the existing document)
		start = gotime.Now()
		cN1, dN1, err := helper.ClientAndAttachedDoc(ctx, svr.RPCAddr(), docKey)
		assert.NoError(b, err)
		duration = gotime.Since(start).Milliseconds()
		assert.Equal(b, `{"text":[{"val":"a"}]}`, dN1.Marshal())
		b.ReportMetric(float64(duration), "4_attach(ms)")

		cN2, dN2, err := helper.ClientAndAttachedDoc(ctx, svr.RPCAddr(), docKey)
		assert.NoError(b, err)
		assert.Equal(b, `{"text":[{"val":"a"}]}`, dN2.Marshal())

		// 6. The new client edits the text.
		// Measurements:
		//  - ChangePack size
		//  - Total document(snapshot) size
		//  - PushPull time
		err = dN1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(0, 0, "b")
			return nil
		})
		assert.NoError(b, err)
		assert.Equal(b, `{"text":[{"val":"b"},{"val":"a"}]}`, dN1.Marshal())

		pack = dN1.CreateChangePack()
		pbPack, err = converter.ToChangePack(pack)
		assert.NoError(b, err)
		changePackSize = proto.Size(pbPack)
		b.ReportMetric(float64(changePackSize), "5_changepack_after_detach(bytes)")

		snapshot, err = converter.SnapshotToBytes(dN1.RootObject(), dN1.AllPresences())
		assert.NoError(b, err)
		snapshotSize = len(snapshot)
		b.ReportMetric(float64(snapshotSize), "6_snapshot_after_detach(bytes)")

		start = gotime.Now()
		assert.NoError(b, cN1.Sync(ctx))
		assert.NoError(b, cN2.Sync(ctx))
		duration = gotime.Since(start).Milliseconds()
		assert.Equal(b, `{"text":[{"val":"b"},{"val":"a"}]}`, dN2.Marshal())
		b.ReportMetric(float64(duration), "7_pushpull_after_detach(ms)")
	}
}

func BenchmarkVersionVector(b *testing.B) {
	assert.NoError(b, logging.SetLogLevel("error"))

	// NOTE(hackerwins): To prevent the snapshot from being created, we set
	// snapshot threshold and snapshot interval to very large values.
	svr, err := helper.TestServerWithSnapshotCfg(100_000, 100_000)
	if err != nil {
		b.Fatal(err)
	}

	b.Cleanup(func() {
		if err := svr.Shutdown(true); err != nil {
			b.Fatal(err)
		}
	})

	b.Run("clients 10", func(b *testing.B) {
		benchmarkVV(b, svr, 10)
	})

	b.Run("clients 100", func(b *testing.B) {
		benchmarkVV(b, svr, 100)
	})

	b.Run("clients 1000", func(b *testing.B) {
		benchmarkVV(b, svr, 1000)
	})
}
