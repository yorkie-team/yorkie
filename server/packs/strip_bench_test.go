//go:build bench

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

package packs

import (
	"testing"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence/inner"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// BenchmarkStripPresenceChanges pins the allocation profile of the
// PushPull entry-side strip across the four input shapes the helper
// receives in practice: presence-only (the worst case for a
// high-fan-out, presence-free Counter document), mixed, ops-only, and
// empty.
func BenchmarkStripPresenceChanges(b *testing.B) {
	id := change.NewID(0, 0, 0, time.InitialActorID, time.NewVersionVector())
	pc := func() *inner.Change {
		return &inner.Change{ChangeType: inner.Put, Presence: inner.Presence{"k": "v"}}
	}
	op := func() operations.Operation {
		return operations.NewRemove(time.InitialTicket, time.InitialTicket, time.InitialTicket)
	}

	shapes := map[string]func(n int) []*change.Change{
		"presence_only": func(n int) []*change.Change {
			out := make([]*change.Change, n)
			for i := range out {
				out[i] = change.New(id, "", nil, pc())
			}
			return out
		},
		"mixed": func(n int) []*change.Change {
			out := make([]*change.Change, n)
			for i := range out {
				out[i] = change.New(id, "", []operations.Operation{op()}, pc())
			}
			return out
		},
		"ops_only": func(n int) []*change.Change {
			out := make([]*change.Change, n)
			for i := range out {
				out[i] = change.New(id, "", []operations.Operation{op()}, nil)
			}
			return out
		},
	}

	for _, n := range []int{0, 1, 8, 64} {
		for name, gen := range shapes {
			if n == 0 && name != "presence_only" {
				continue
			}
			b.Run(name+"/n="+itoa(n), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					input := gen(n)
					b.StartTimer()
					_ = stripPresenceChanges(input)
				}
			})
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
