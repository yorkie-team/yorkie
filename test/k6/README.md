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
k6 run -e TEST_MODE=even -e DOC_COUNT=10 -e USE_CONTROL_DOC=true presence.ts
```

- `TEST_MODE`: Set to `skew` or `even` to choose the load distribution mode.
- `DOC_COUNT`: Number of documents to be used in the test. This is only applicable in `even` mode.
- `USE_CONTROL_DOC`: Set to `true` to use a control document for presence data.

If you want to run the test in `skew` mode, use the following command:

```bash
k6 run -e TEST_MODE=skew presence.ts
```

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
