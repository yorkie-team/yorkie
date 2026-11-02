# Lessons: `version --output json` mixes the server-fetch error into stdout

## Cobra's `Print` is not stderr

`cmd.Print`/`cmd.Printf`/`cmd.Println` all resolve to `OutOrStderr()` — the
*out* writer when one is set, falling back to stderr only when it is not.
`cmd/yorkie/commands.go` sets `rootCmd.SetOut(os.Stdout)`, so in the real CLI
every one of those calls lands on stdout. The name reads like "print a
message", not "print to stdout", which is how the error ended up interleaved
with the structured payload. Only `PrintErr*` is unconditionally the *err*
writer.

## Failure injection without a server

The obvious way to test the error path is to point `--rpc-addr` at a dead port,
but `fetchServerVersion` never gets that far: `config.LoadAuth` returns
`auth for '<addr>' does not exist` before any dial. Setting `HOME` to a temp
dir makes that deterministic and keeps the test off the network entirely.

## Self review

Not run: this task was implemented in an autonomous headless run with no
reviewer subagent available. Review is left to CI, `@claude review`, and a
human — recorded here so the skipped round is not mistaken for a clean one.
