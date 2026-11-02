**Created**: 2026-10-25

# `version --output json` mixes the server-fetch error into stdout

## Problem

yorkie#2127. When `yorkie version` cannot reach the server, `printServerError`
writes through `cmd.Print`/`cmd.Println`. Cobra routes both to the command's
*out* writer, which is the same stream `printVersionInfo` uses for JSON and
YAML. So

```sh
yorkie version --output json --rpc-addr localhost:1
```

emits a valid JSON document followed by `Error fetching server version: ...`
on the same stream. A consumer piping stdout into `jq` gets a parse error; the
YAML case either fails to parse or silently gains a junk field.

The data itself is already correct — `info.ServerVersion` stays nil and is
`omitempty` in both encodings — so only the stream choice is wrong.

## Options explored

| Option | Verdict |
|---|---|
| Write the server-fetch error with `cmd.PrintErr`/`cmd.PrintErrln` | Chosen: one-line fix, matches the convention that diagnostics go to stderr, and the human-readable table output is unaffected because a terminal shows both streams |
| Suppress the error entirely when `--output` is json/yaml | Loses the only signal that the server half is missing; a script checking `serverVersion` would see nothing explaining why |
| Fold the error into the structured payload as an `error` field | Changes the documented shape of `VersionInfo` for every consumer to serve a failure case, and the table output would still need a separate path |

## Plan

- [x] `printServerError` writes to stderr via `cmd.PrintErr`/`cmd.PrintErrln`
- [x] `cmd/yorkie/version_test.go`: run the version command with separate
      stdout/stderr buffers for both `json` and `yaml`, asserting stdout holds
      only the version document and the error appears only on stderr
- [x] Cover both `printServerError` branches (generic error and the
      `CodeUnimplemented` message)
- [x] `make lint`, `go test ./cmd/...`

## Review

- The test forces the failure without a network round trip: `fetchServerVersion`
  calls `config.LoadAuth` first, which errors for an address with no stored
  auth entry. `HOME` is pointed at `t.TempDir()` so the real CLI config is
  never read or written.
- `viper` keys are process-global; the test restores `output` and `rpcAddr`
  after each case.
