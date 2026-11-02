/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

// The push/pull model shared by gc_rga_fuzz_test.go and
// text_undo_fuzz_test.go. It carries no build tag so that the Text undo
// harness can run in every test pass.

package document_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// advServer models server/packs/pushpull.go: one ordered change log, one stored
// VersionVector row per client (the VV carried in that client's push request),
// and minVV = element-wise min over the stored rows (memory DB
// GetMinVersionVector). A client only ever sees minVV as the VersionVector of a
// pull response, and Document.ApplyChangePack is what runs GC with it -- so GC
// here can never be called at a moment the protocol would not produce.
type advServer struct {
	log    []*change.Change
	actors []string
	rows   map[string]time.VersionVector
}

type advClient struct {
	doc    *document.Document
	id     time.ActorID
	cursor int
}

func newAdvServer() *advServer {
	return &advServer{rows: map[string]time.VersionVector{}}
}

func (s *advServer) minVV() time.VersionVector {
	var vectors []time.VersionVector
	for _, vv := range s.rows {
		vectors = append(vectors, vv)
	}
	return time.MinVersionVector(vectors...)
}

// sync is one PushPull round trip: push local changes, store the request's VV,
// pull everything the server holds, and hand the response (changes + minVV) to
// ApplyChangePack, which applies then collects.
func (s *advServer) sync(c *advClient, gcOn bool) error {
	p := c.doc.CreateChangePack()

	// 01. push: append to the log, then record the row the request carried.
	var lastSeq uint32
	for _, ch := range p.Changes {
		s.log = append(s.log, ch)
		s.actors = append(s.actors, ch.ID().ActorID().String())
		lastSeq = ch.ClientSeq()
	}
	s.rows[c.id.String()] = p.VersionVector.DeepCopy()

	// 02. pull: everything on the log this client has not seen.
	var chs []*change.Change
	for i := c.cursor; i < len(s.log); i++ {
		if s.actors[i] == c.id.String() {
			continue
		}
		chs = append(chs, s.log[i])
	}
	c.cursor = len(s.log)

	vv := s.minVV()
	if !gcOn {
		vv = time.InitialVersionVector
	}

	return c.doc.ApplyChangePack(change.NewPack(
		c.doc.Key(), change.NewCheckpoint(0, lastSeq), chs, vv, nil,
	))
}

func newAdvClients(t *testing.T, n int) []*advClient {
	t.Helper()
	cs := make([]*advClient, 0, n)
	for i := range n {
		id, err := time.ActorIDFromHex(fmt.Sprintf("%024d", i+1))
		require.NoError(t, err)
		d := document.New("adv-doc")
		d.SetActor(id)
		cs = append(cs, &advClient{doc: d, id: id})
	}
	return cs
}
