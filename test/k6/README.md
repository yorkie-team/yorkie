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
k6 run -e DOC_KEY_PREFIX=even-1 -e TEST_MODE=even -e CONCURRENCY=500 -e VU_PER_DOCS=10 presence.ts
```

This will run the test with 50 documents, each with 10 virtual users, for a total of 500 concurrent users.

- `DOC_KEY_PREFIX`: Prefix for the document keys. This is used to create unique document keys for each test run.
- `TEST_MODE`: Set to `skew` or `even` to choose the load distribution mode.
- `CONCURRENCY`: Number of concurrent users to simulate.
- `VU_PER_DOCS`: Number of virtual users per document. This is only applicable in `even` mode.

If you want to run the test in `skew` mode, use the following command:

```bash
k6 run -e DOC_KEY_PREFIX=skew-1 -e TEST_MODE=skew -e CONCURRENCY=500 presence.ts
```

This will run the test with a single document with 500 virtual users.

#### Profiling the Yorkie Server with pprof

The Yorkie server is running with pprof enabled on port 8081.
To access the profiling reports, you can:

```bash
curl http://localhost:8081/debug/pprof/profile\?seconds\=60 --output cpu.out
```

This will download 60 seconds of CPU profile.
Then open interactive pprof web tool with:

```bash
go tool pprof -http=:9090 cpu.prof
```
