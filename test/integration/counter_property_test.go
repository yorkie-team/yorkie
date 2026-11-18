//go:build integration

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

package integration

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

const (
	// counterPropertyClientCount is the number of replicas that increase the
	// counter concurrently.
	counterPropertyClientCount = 3

	// counterPropertySeedCount is the number of independent random event
	// sequences to run.
	counterPropertySeedCount = 10

	// counterPropertyEventCount is the length of a generated event sequence.
	counterPropertyEventCount = 16

	// counterPropertyMaxDelta bounds the magnitude of a generated increment, so
	// that the sum of a whole sequence stays far from the Integer counter's
	// range and the properties below test convergence, not overflow.
	counterPropertyMaxDelta = 10

	// counterPropertySyncRatio makes one generated event in this many a Sync.
	// Syncs have to be sparse: increments only become concurrent while they sit
	// unsynchronized on their own replica.
	counterPropertySyncRatio = 3

	// counterPropertyKey is the key of the shared Counter under the root object.
	counterPropertyKey = "cnt"

	// counterPropertyInitialValue is the value every replica starts from.
	counterPropertyInitialValue = 100

	// counterPropertyShrinkBudget caps how many sequences minimize may replay.
	// Each replay is a full round of server round trips, so the search for a
	// small reproducer must not run unbounded.
	counterPropertyShrinkBudget = 40
)

// counterEventKind distinguishes the two things a generated event can do.
type counterEventKind int

const (
	counterIncrease counterEventKind = iota
	counterSync
)

// counterEvent is one generated step: a client, and either an increment by
// delta or a synchronization through the server.
type counterEvent struct {
	kind  counterEventKind
	cli   int
	delta int
}

// String renders an event the way the test would have to call it, so a printed
// sequence can be transcribed into a regular test case.
func (e counterEvent) String() string {
	if e.kind == counterSync {
		return fmt.Sprintf("c%d.Sync()", e.cli)
	}
	return fmt.Sprintf("c%d.Increase(%d)", e.cli, e.delta)
}

// formatCounterEvents renders a sequence one event per line.
func formatCounterEvents(events []counterEvent) string {
	var sb strings.Builder
	for i, e := range events {
		fmt.Fprintf(&sb, "  %2d: %s\n", i, e)
	}
	return sb.String()
}

// generateCounterEvents builds a random event sequence. The sequence is fully
// determined by r, so a failing case can be reproduced from its seed.
func generateCounterEvents(r *rand.Rand, clientCount, eventCount int) []counterEvent {
	events := make([]counterEvent, 0, eventCount)
	for range eventCount {
		e := counterEvent{cli: r.Intn(clientCount)}
		if r.Intn(counterPropertySyncRatio) == 0 {
			e.kind = counterSync
		} else {
			// Deltas may be negative: a counter that converges under addition
			// alone could still lose a subtraction.
			e.delta = r.Intn(2*counterPropertyMaxDelta+1) - counterPropertyMaxDelta
		}
		events = append(events, e)
	}
	return events
}

// counterPropertyRunner replays generated event sequences against the server.
// Each replay attaches its own document, so a shrunk candidate starts from the
// same initial state as the sequence it was shrunk from.
type counterPropertyRunner struct {
	t       *testing.T
	clients []*client.Client
	attempt int
}

// drain synchronizes every replica twice. The first round pushes each replica's
// local changes to the server, the second pulls their union back, so after it
// every replica holds the same set of changes whatever order the events ran in.
func (ru *counterPropertyRunner) drain() {
	ctx := context.Background()
	for range 2 {
		for _, cli := range ru.clients {
			require.NoError(ru.t, cli.Sync(ctx))
		}
	}
}

// run replays events on freshly attached replicas and returns the first
// property violation it observes, or an empty string when every property holds.
// Violations are returned instead of asserted because minimize has to ask
// whether a shrunk sequence still fails, which a fatal assertion would prevent.
// Transport and lifecycle errors are not properties and still abort the test.
func (ru *counterPropertyRunner) run(events []counterEvent) string {
	t, ctx := ru.t, context.Background()
	ru.attempt++

	docs := make([]*document.Document, 0, len(ru.clients))
	for _, cli := range ru.clients {
		doc := document.New(helper.TestKey(t, ru.attempt))
		require.NoError(t, cli.Attach(ctx, doc))
		docs = append(docs, doc)
	}
	defer func() {
		for i, cli := range ru.clients {
			require.NoError(t, cli.Detach(ctx, docs[i]))
		}
	}()

	// Create the shared Counter on one replica and propagate it, so every
	// replica increases the same CRDT. A per-replica SetNewCounter would create
	// distinct counters that resolve by last-writer-wins and drop increments.
	require.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewCounter(counterPropertyKey, counterPropertyInitialValue)
		return nil
	}))
	ru.drain()

	total := counterPropertyInitialValue
	for _, e := range events {
		switch e.kind {
		case counterIncrease:
			require.NoError(t, docs[e.cli].Update(func(root *json.Object, p *presence.Presence) error {
				root.GetCounter(counterPropertyKey).Increase(e.delta)
				return nil
			}))
			total += e.delta
		case counterSync:
			require.NoError(t, ru.clients[e.cli].Sync(ctx))
		}
	}

	// The generated sequence leaves an arbitrary amount unsynchronized, so the
	// properties are checked after a deterministic drain rather than wherever
	// the last generated Sync happened to land.
	ru.drain()

	// P1: every replica converged to the same state.
	converged := docs[0].Marshal()
	for i, doc := range docs[1:] {
		if v := doc.Marshal(); v != converged {
			return fmt.Sprintf("replica 0 and replica %d diverged: %s != %s", i+1, converged, v)
		}
	}

	// P2: the converged value is the initial value plus every increment.
	expected := fmt.Sprintf(`{"%s":%d}`, counterPropertyKey, total)
	if converged != expected {
		return fmt.Sprintf("converged state is %s, want %s", converged, expected)
	}

	// P3: the converged state is stable under further synchronization.
	ru.drain()
	for i, doc := range docs {
		if v := doc.Marshal(); v != converged {
			return fmt.Sprintf("replica %d changed on an extra sync: %s != %s", i, v, converged)
		}
	}

	return ""
}

// minimize greedily drops events while the sequence still violates a property,
// so a failure reports a small reproducible scenario instead of the whole
// generated sequence. Dropping an increment lowers the expected total too, as
// run recomputes it from the candidate sequence it is given.
func (ru *counterPropertyRunner) minimize(
	events []counterEvent,
	violation string,
) ([]counterEvent, string) {
	budget := counterPropertyShrinkBudget
	for shrunk := true; shrunk && budget > 0; {
		shrunk = false
		for i := range events {
			if budget == 0 {
				break
			}
			budget--

			candidate := slices.Delete(slices.Clone(events), i, i+1)
			if v := ru.run(candidate); v != "" {
				events, violation, shrunk = candidate, v, true
				break
			}
		}
	}
	return events, violation
}

// TestCounterPropertyConvergence verifies that replicas converge to the same
// Counter, and to the arithmetically expected value, after randomly generated
// sequences of increments and syncs, rather than the fixed interleavings
// counter_test.go relies on.
func TestCounterPropertyConvergence(t *testing.T) {
	clients := activeClients(t, counterPropertyClientCount)
	defer deactivateAndCloseClients(t, clients)

	for seed := range counterPropertySeedCount {
		t.Run(fmt.Sprintf("counter converges with seed %d", seed), func(t *testing.T) {
			//nolint:gosec // A weak PRNG is intended: the seed reproduces failures.
			r := rand.New(rand.NewSource(int64(seed)))
			events := generateCounterEvents(r, counterPropertyClientCount, counterPropertyEventCount)

			runner := &counterPropertyRunner{t: t, clients: clients}
			violation := runner.run(events)
			if violation == "" {
				return
			}

			minimized, violation := runner.minimize(events, violation)
			t.Fatalf("counter property violated: %s\nminimized to %d of %d events:\n%s",
				violation, len(minimized), len(events), formatCounterEvents(minimized))
		})
	}
}
