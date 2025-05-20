# Yorkie Load Test and Profiling with k6 and pprof

## Overview

This document provides an overview of the Yorkie load test script and how to profile the Yorkie server using pprof.

### How to Run the Load Test

#### Prerequisites

**Install k6**: Ensure you have k6 installed on your machine. You can download it from [k6.io](https://k6.io/docs/getting-started/installation/).

#### Run the Yorkie Server

```bash
# Start the Yorkie server
yorkie server --mongo-connection-uri mongodb://localhost:27017 --enable-pprof
```

#### Run the Load Test

To run the load test, use the following command:

```bash
k6 run -e TEST_MODE=even -e CONCURRENCY=500 -e VU_PER_DOCS=10 presence.ts
```

This will run the test with 50 documents, each with 10 virtual users, for a total of 500 concurrent users.

- `TEST_MODE`: Set to `skew` or `even` to choose the load distribution mode.
- `CONCURRENCY`: Number of concurrent users to simulate.
- `VU_PER_DOCS`: Number of virtual users per document. This is only applicable in `even` mode.

If you want to run the test in `skew` mode, use the following command:

```bash
k6 run -e TEST_MODE=skew -e CONCURRENCY=500 presence.ts
```

This will run the test with a single document with 500 virtual users.

#### Profiling the Yorkie Server with pprof

The Yorkie server is running with pprof enabled on port 8081.
To access the profiling reports, you can:

- CPU profile: `go tool pprof http://localhost:8081/debug/pprof/profile`
- Heap allocation: `go tool pprof http://localhost:8081/debug/pprof/heap`
- Blocking profile: `go tool pprof http://localhost:8081/debug/pprof/block`

Example: go tool pprof http://localhost:8081/debug/pprof/profile?seconds=150
This will download 150 seconds of CPU profile and open interactive pprof tool.

After running a pprof command, use:

- 'top' - Show top consumers
- 'web' - Generate a graph visualization (requires graphviz)
- 'pdf' - Generate PDF report
