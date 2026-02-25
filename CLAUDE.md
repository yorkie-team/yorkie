# Yorkie — CRDT-based Collaboration Server

Go server providing document synchronization via CRDTs with MongoDB or in-memory storage.

## Development Commands

```sh
make build          # Build binary to bin/yorkie
make test           # Run unit tests
make lint           # Run golangci-lint
make fmt            # Format code (gofmt + goimports)
make proto          # Generate protobuf code via buf
make test -tags integration  # Integration tests (requires MongoDB)
make bench          # Run benchmarks
```

## After Making Changes

Always run before submitting:
```sh
make lint && make test
```

For protobuf changes, regenerate first: `make proto`

## Key Architecture

- **Three-layer data model**: JSON-like (user-facing) -> CRDT (conflict resolution) -> Common (shared primitives)
- **Design docs**: Always consult `design/` for architectural context. Keep design docs updated after changes.
- **Entry point**: `cmd/yorkie/`
- **Generated code**: `api/yorkie/v1/` (protobuf)

## Gotchas

- Build tags `integration` and `bench` separate test types — don't forget the tag when running integration tests
- Apache 2.0 license header required on all files
- Follow [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md)
- Package comment required on every package (`// Package xxx provides...`)
