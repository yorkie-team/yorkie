# Yorkie Load Test and Profiling with k6 and pprof

## Overview

This document provides an overview of the Yorkie load test script and how to profile the Yorkie server using pprof.

### How to Run the Load Test

#### Prerequisites

**Install k6**: Ensure you have k6 installed on your machine. You can download it from [k6.io](https://grafana.com/docs/k6/latest/set-up/install-k6/).

#### Available Load Tests

There are two types of load tests available:

1. **Basic Presence Test** (`presence.ts`)

   - Tests basic presence functionality without streaming
   - Simulates users updating their presence data
   - Ideal for testing presence update latency and throughput in `PushPull`

2. **Presence with Stream Test** (`presence-with-stream.ts`)

   - Tests real-time presence streaming capabilities
   - Simulates both watchers (who observe changes) and updaters (who make changes)
   - Tests watch stream performance and resource usage

#### Run the Yorkie Server

```bash
# Start the Yorkie server
yorkie server --mongo-connection-uri mongodb://localhost:27017 --pprof-enabled
```

#### Run Load Test

##### Basic Presence Test

To run the basic presence load test, use the following command:

```bash
k6 run -e DOC_KEY_PREFIX=even-1 -e TEST_MODE=even -e CONCURRENCY=500 -e VU_PER_DOCS=10 presence.ts
```

This will run the test with 50 documents, each with 10 virtual users, for a total of 500 concurrent users.

**Parameters:**

- `DOC_KEY_PREFIX`: Prefix for the document keys. This is used to create unique document keys for each test run.
- `TEST_MODE`: Set to `skew` or `even` to choose the load distribution mode.
- `CONCURRENCY`: Number of concurrent users to simulate.
- `VU_PER_DOCS`: Number of virtual users per document. This is only applicable in `even` mode.

If you want to run the test in `skew` mode, use the following command:

```bash
k6 run -e DOC_KEY_PREFIX=skew-1 -e TEST_MODE=skew -e CONCURRENCY=500 presence.ts
```

This will run the test with a single document with 500 virtual users.

##### Presence with Stream Test

To run the more advanced presence test with streaming capabilities, use the following command:

```bash
k6 run -e DOC_KEY_PREFIX=stream-1 -e TEST_MODE=even -e CONCURRENCY=500 -e VU_PER_DOCS=10 -e WATCHER_RATIO=0.5 presence-with-stream.ts
```

This test simulates a more realistic scenario with both watchers and updaters:

- **Watchers**: Connect to documents and watch for presence changes via streaming
- **Updaters**: Continuously make presence updates to documents

**Additional Parameters for Stream Test:**

- `WATCHER_RATIO`: Ratio of watchers to total concurrent users (0.0 to 1.0). For example, 0.5 means 50% watchers and 50% updaters.

For a `skew` mode stream test (all users on single document):

```bash
k6 run -e DOC_KEY_PREFIX=stream-skew-1 -e TEST_MODE=skew -e CONCURRENCY=500 -e WATCHER_RATIO=0.3 presence-with-stream.ts
```

This creates a high-contention scenario with 150 watchers and 350 updaters all on the same document.

#### Profiling the Yorkie Server with pprof

The Yorkie server is running with pprof enabled on port 8081.

##### CPU Profiling

To collect CPU profile data, you can:

```bash
curl http://localhost:8081/debug/pprof/profile\?seconds\=150 --output cpu.out
```

This will download 150 seconds of CPU profile.
Then open interactive pprof web tool with:

```bash
go tool pprof -http=:9090 cpu.out
```

##### Memory Profiling

To collect heap memory profile data(In the middle of the load test):

```bash
curl http://localhost:8081/debug/pprof/heap --output mem.out
```

This will download current heap memory profile.
Then open interactive pprof web tool with:

```bash
go tool pprof -http=:9090 mem.out
```
