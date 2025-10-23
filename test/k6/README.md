# Yorkie Load Test and Profiling with k6 and pprof

## Overview

This document provides an overview of the Yorkie load test script and how to profile the Yorkie server using pprof.

## Prerequisites

**Install k6**: Ensure you have k6 installed on your machine. You can download it from [k6.io](https://grafana.com/docs/k6/latest/set-up/install-k6/).

## Available Load Tests

There are three types of load tests available:

### Basic Presence Test (`presence.ts`)

- Tests basic presence functionality without streaming
- Simulates users updating their presence data via Document API
- Ideal for testing presence update latency and throughput in `PushPull`

### Presence with Stream Test (`presence-with-stream.ts`)

- Tests real-time presence streaming capabilities via Document API
- Simulates both watchers (who observe changes) and updaters (who make changes)
- Tests watch stream performance and resource usage

### Dedicated Presence Test (`dedicated-presence.ts`)

- Tests the dedicated Presence API (AttachPresence, DetachPresence)
- Simulates clients repeatedly attaching and detaching from presence counters
- Ideal for testing presence counter accuracy and attach/detach performance
- Verifies that presence count increments/decrements correctly under load

## Running the Yorkie Server

Start the Yorkie server with pprof enabled:

```bash
yorkie server --mongo-connection-uri mongodb://localhost:27017 --pprof-enabled
```

## Running Load Tests

### Basic Presence Test

Run the basic presence load test in **even mode** (distributed across multiple documents):

```bash
k6 run -e DOC_KEY_PREFIX=even-1 -e TEST_MODE=even -e CONCURRENCY=500 -e VU_PER_DOCS=10 presence.ts
```

This runs the test with 50 documents, each with 10 virtual users, for a total of 500 concurrent users.

Run the test in **skew mode** (all users on a single document):

```bash
k6 run -e DOC_KEY_PREFIX=skew-1 -e TEST_MODE=skew -e CONCURRENCY=500 presence.ts
```

This runs the test with a single document with 500 virtual users.

### Presence with Stream Test

Run the advanced presence test with streaming in **even mode**:

```bash
k6 run -e DOC_KEY_PREFIX=stream-1 -e TEST_MODE=even -e CONCURRENCY=500 -e VU_PER_DOCS=10 -e WATCHER_RATIO=0.5 presence-with-stream.ts
```

This test simulates a realistic scenario with both watchers and updaters:

- **Watchers**: Connect to documents and watch for presence changes via streaming
- **Updaters**: Continuously make presence updates to documents

Run the stream test in **skew mode** (high-contention scenario):

```bash
k6 run -e DOC_KEY_PREFIX=stream-skew-1 -e TEST_MODE=skew -e CONCURRENCY=500 -e WATCHER_RATIO=0.3 presence-with-stream.ts
```

This creates a high-contention scenario with 150 watchers and 350 updaters all on the same document.

### Dedicated Presence Test

Run the dedicated presence API test in **even mode** (distributed across multiple presence counters):

```bash
k6 run -e PRESENCE_KEY_PREFIX=presence-1 -e TEST_MODE=even -e CONCURRENCY=500 -e VU_PER_PRESENCE=10 -e ATTACH_ITERATIONS=5 dedicated-presence.ts
```

This runs the test with 50 presence counters, each with 10 virtual users, where each user performs 5 attach/detach cycles.

Run the test in **skew mode** (all users on a single presence counter for high contention):

```bash
k6 run -e PRESENCE_KEY_PREFIX=presence-skew-1 -e TEST_MODE=skew -e CONCURRENCY=500 -e ATTACH_ITERATIONS=5 dedicated-presence.ts
```

This creates a high-contention scenario with 500 users all attaching/detaching from the same presence counter, ideal for testing counter accuracy under heavy concurrent access.

## Test Parameters

### Common Parameters

- `DOC_KEY_PREFIX`: Prefix for document keys (creates unique keys for each test run)
- `TEST_MODE`: Choose between `skew` (single document) or `even` (multiple documents)
- `CONCURRENCY`: Number of concurrent users to simulate

### Even Mode Parameters

- `VU_PER_DOCS`: Number of virtual users per document (only in `even` mode)

### Stream Test Parameters

- `WATCHER_RATIO`: Ratio of watchers to total users (0.0 to 1.0). Example: 0.5 = 50% watchers, 50% updaters

### Dedicated Presence Test Parameters

- `PRESENCE_KEY_PREFIX`: Prefix for presence keys (creates unique keys for each test run)
- `VU_PER_PRESENCE`: Number of virtual users per presence counter (only in `even` mode)
- `ATTACH_ITERATIONS`: Number of attach/detach cycles each user performs (default: 5)

## Profiling with pprof

The Yorkie server runs with pprof enabled on port 8081.

### CPU Profiling

Collect CPU profile data:

```bash
curl http://localhost:8081/debug/pprof/profile\?seconds\=150 --output cpu.out
```

Open the interactive pprof web tool:

```bash
go tool pprof -http=:9090 cpu.out
```

### Memory Profiling

Collect heap memory profile data (during load test):

```bash
curl http://localhost:8081/debug/pprof/heap --output mem.out
```

Open the interactive pprof web tool:

```bash
go tool pprof -http=:9090 mem.out
```
