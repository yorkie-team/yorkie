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

// bench-ops is a write-heavy load generator for comparing storage backends.
// Unlike the k6 presence test, every iteration produces *operation* changes
// (text edits) that hit the changes-table write path — i.e., the path that's
// supposed to benefit from ScyllaDB. There are no artificial sleeps; the
// bench drives at the closed-loop saturation the client and server allow.
//
// Usage:
//
//	bench-ops \
//	    --server=localhost:28080 \
//	    --api-key=<publicKey> \
//	    --concurrency=200 \
//	    --duration=60s \
//	    --docs=50 \
//	    --ops-per-sync=5 \
//	    --edit-len=8
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/key"
)

var (
	serverAddr  = flag.String("server", "localhost:28080", "Yorkie server RPC address")
	apiKey      = flag.String("api-key", "", "Project public API key")
	concurrency = flag.Int("concurrency", 100, "Number of concurrent VUs (one Yorkie client per VU)")
	duration    = flag.Duration("duration", 30*time.Second, "Steady-state bench duration")
	docCount    = flag.Int("docs", 50, "Number of distinct documents VUs round-robin across")
	opsPerSync  = flag.Int("ops-per-sync", 5, "Number of text-edit operations applied between each Sync call")
	editLen     = flag.Int("edit-len", 8, "Length of inserted text per edit operation")
	docPrefix   = flag.String("doc-prefix", "", "Document key prefix (default: bench-ops-<unix>)")
	warmup      = flag.Duration("warmup", 5*time.Second, "Warmup duration excluded from results")
)

type stats struct {
	syncCount   int64
	opCount     int64
	syncErrors  int64
	syncLatency []time.Duration
	mu          sync.Mutex
}

func (s *stats) record(d time.Duration, ops int64) {
	atomic.AddInt64(&s.syncCount, 1)
	atomic.AddInt64(&s.opCount, ops)
	s.mu.Lock()
	s.syncLatency = append(s.syncLatency, d)
	s.mu.Unlock()
}

func (s *stats) recordError() {
	atomic.AddInt64(&s.syncErrors, 1)
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

func main() {
	flag.Parse()
	if *apiKey == "" {
		log.Fatal("--api-key is required")
	}
	if *docPrefix == "" {
		*docPrefix = fmt.Sprintf("bench-ops-%d", time.Now().Unix())
	}

	log.Printf("config: server=%s docs=%d concurrency=%d duration=%s ops-per-sync=%d edit-len=%d",
		*serverAddr, *docCount, *concurrency, *duration, *opsPerSync, *editLen)

	measure := &stats{}

	// Phase 1: setup all VU clients and attach to their assigned docs.
	type vu struct {
		cli *client.Client
		doc *document.Document
	}
	vus := make([]*vu, *concurrency)
	var setupWG sync.WaitGroup
	setupErr := make(chan error, *concurrency)
	for i := 0; i < *concurrency; i++ {
		setupWG.Add(1)
		go func(idx int) {
			defer setupWG.Done()
			cli, err := client.Dial(*serverAddr, client.WithAPIKey(*apiKey))
			if err != nil {
				setupErr <- fmt.Errorf("vu %d dial: %w", idx, err)
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := cli.Activate(ctx); err != nil {
				setupErr <- fmt.Errorf("vu %d activate: %w", idx, err)
				return
			}
			docKey := key.Key(fmt.Sprintf("%s-%d", *docPrefix, idx%*docCount))
			doc := document.New(docKey)
			if err := cli.Attach(ctx, doc); err != nil {
				setupErr <- fmt.Errorf("vu %d attach %s: %w", idx, docKey, err)
				return
			}
			// Initialize the text root once. The first attach for a fresh doc
			// uploads this; subsequent VUs see it via pull.
			if err := doc.Update(func(root *json.Object, p *presence.Presence) error {
				if root.Get("text") == nil {
					root.SetNewText("text")
				}
				return nil
			}); err != nil {
				setupErr <- fmt.Errorf("vu %d init text: %w", idx, err)
				return
			}
			if err := cli.Sync(ctx); err != nil {
				setupErr <- fmt.Errorf("vu %d initial sync: %w", idx, err)
				return
			}
			vus[idx] = &vu{cli: cli, doc: doc}
		}(i)
	}
	setupWG.Wait()
	close(setupErr)
	for err := range setupErr {
		log.Fatalf("setup failed: %v", err)
	}
	log.Printf("setup complete: %d VUs ready", *concurrency)

	// Phase 2: warmup (writes counted but discarded).
	runPhase := func(label string, dur time.Duration, m *stats) {
		log.Printf("%s starting (%s)...", label, dur)
		ctx, cancel := context.WithTimeout(context.Background(), dur+10*time.Second)
		defer cancel()
		deadline := time.Now().Add(dur)

		var wg sync.WaitGroup
		for i, v := range vus {
			wg.Add(1)
			go func(idx int, v *vu) {
				defer wg.Done()
				rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(idx)))
				editChars := []rune("abcdefghijklmnopqrstuvwxyz")
				for time.Now().Before(deadline) {
					// Apply N text edits locally — always prepend at position 0
					// so we don't need to track the rolling length per VU. Each
					// edit still produces a distinct CRDT operation that must
					// be persisted in the changes table.
					err := v.doc.Update(func(root *json.Object, p *presence.Presence) error {
						text := root.GetText("text")
						for op := 0; op < *opsPerSync; op++ {
							buf := make([]rune, *editLen)
							for k := range buf {
								buf[k] = editChars[rng.Intn(len(editChars))]
							}
							text.Edit(0, 0, string(buf))
						}
						return nil
					})
					if err != nil {
						m.recordError()
						continue
					}
					start := time.Now()
					if err := v.cli.Sync(ctx); err != nil {
						m.recordError()
						continue
					}
					m.record(time.Since(start), int64(*opsPerSync))
				}
			}(i, v)
		}
		wg.Wait()
	}

	if *warmup > 0 {
		runPhase("warmup", *warmup, &stats{})
	}
	start := time.Now()
	runPhase("steady-state", *duration, measure)
	elapsed := time.Since(start)

	// Phase 3: teardown.
	log.Printf("teardown: detaching %d VUs...", *concurrency)
	var teardownWG sync.WaitGroup
	for _, v := range vus {
		teardownWG.Add(1)
		go func(v *vu) {
			defer teardownWG.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = v.cli.Detach(ctx, v.doc)
			_ = v.cli.Deactivate(ctx)
			_ = v.cli.Close()
		}(v)
	}
	teardownWG.Wait()

	// Phase 4: report.
	measure.mu.Lock()
	sort.Slice(measure.syncLatency, func(i, j int) bool {
		return measure.syncLatency[i] < measure.syncLatency[j]
	})
	lat := measure.syncLatency
	measure.mu.Unlock()

	var total time.Duration
	for _, d := range lat {
		total += d
	}
	var avg time.Duration
	if len(lat) > 0 {
		avg = total / time.Duration(len(lat))
	}

	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "=== bench-ops result ===")
	fmt.Fprintf(os.Stdout, "concurrency:       %d\n", *concurrency)
	fmt.Fprintf(os.Stdout, "docs:              %d\n", *docCount)
	fmt.Fprintf(os.Stdout, "ops/sync:          %d\n", *opsPerSync)
	fmt.Fprintf(os.Stdout, "duration:          %s\n", elapsed.Round(time.Millisecond))
	fmt.Fprintf(os.Stdout, "syncs:             %d\n", measure.syncCount)
	fmt.Fprintf(os.Stdout, "sync errors:       %d\n", measure.syncErrors)
	fmt.Fprintf(os.Stdout, "operations pushed: %d\n", measure.opCount)
	fmt.Fprintf(os.Stdout, "syncs/s:           %.1f\n", float64(measure.syncCount)/elapsed.Seconds())
	fmt.Fprintf(os.Stdout, "ops/s:             %.1f\n", float64(measure.opCount)/elapsed.Seconds())
	fmt.Fprintf(os.Stdout, "sync latency:      avg=%s p50=%s p90=%s p95=%s p99=%s max=%s\n",
		avg.Round(time.Microsecond),
		percentile(lat, 0.50).Round(time.Microsecond),
		percentile(lat, 0.90).Round(time.Microsecond),
		percentile(lat, 0.95).Round(time.Microsecond),
		percentile(lat, 0.99).Round(time.Microsecond),
		percentile(lat, 1.0).Round(time.Microsecond),
	)
}
