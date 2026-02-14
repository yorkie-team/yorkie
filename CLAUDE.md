# Yorkie — CRDT-based Collaboration Server

Open-source document store for building real-time collaborative applications. Provides document synchronization via CRDTs with MongoDB or in-memory storage.

## Tech Stack

- Go 1.24, Protocol Buffers (buf), ConnectRPC
- MongoDB (production) / MemDB (testing)
- Docker, Helm charts, Prometheus/Grafana monitoring

## Development Commands

```sh
make build          # Build binary to bin/yorkie
make test           # Run unit tests
make test -tags integration  # Run integration tests (requires MongoDB)
make bench          # Run benchmarks
make lint           # Run golangci-lint
make fmt            # Format code (gofmt + goimports)
make proto          # Generate protobuf code via buf
make docker         # Build Docker image
```

## Project Structure

```
cmd/yorkie/         # CLI entry point
server/             # Server implementation
  rpc/              # gRPC/ConnectRPC handlers
  backend/          # Core backend (DB, sync, background tasks)
    database/       # DB interface + mongo/memory implementations
  packs/            # Document change pack processing
  clients/          # Client session management
  documents/        # Document management
  logging/          # Structured logging
  profiling/        # pprof endpoints
  authz/            # Authorization
pkg/
  document/         # Document model (JSON-like, CRDT, operations)
  index/            # Tree indexing
api/
  converter/        # Protobuf <-> domain converters
  types/            # Shared API types
  yorkie/v1/        # Generated protobuf code
admin/              # Admin client library
client/             # SDK client library
design/             # 20+ architecture design docs
test/
  integration/      # Integration tests
  bench/            # Benchmark tests
  complex/          # Complex scenario tests
  helper/           # Test utilities
build/              # Docker, Helm charts, build scripts
```

## Code Conventions

- Apache 2.0 license header on all files
- Follow [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md)
- Package comment on every package (`// Package xxx provides...`)
- Build tags for test separation: `integration`, `bench`
- Commit messages: subject line max 70 chars, body wrapped at 80 chars

## Architecture Notes

- **CRDT engine**: Conflict-free replicated data types for Text, Tree, Counter, Object, Array
- **Three-layer data model**: JSON-like (user-facing) -> CRDT (conflict resolution) -> Common (shared primitives)
- **Version Vector**: Lamport Synced Version Vector for causal ordering
- **Garbage Collection**: Via minVersionVector across attached clients
- **Document lifecycle**: nil -> Attaching -> Attached -> Detached -> Removed
- **PubSub**: WatchDocument gRPC streaming for real-time sync
- **Cluster mode**: Consistent hashing (Maglev) for document sharding
- **Housekeeping**: Background client deactivation + document compaction
- See `design/` folder for detailed architecture documents
