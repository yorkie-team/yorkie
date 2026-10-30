# Remove the MCP server — lessons

**Created**: 2026-10-22

## Self review

- Round 1 (correctness, completeness): no blocking findings. No leftover
  `/mcp/` or `server/rpc/mcp` reference; every helper MCP called keeps an
  AdminService caller; `go mod tidy -diff` is empty; integration-tagged
  packages build and vet. One non-blocking finding: the task index lacked a
  row for this task — fixed by running `scripts/tasks-index.sh`.

## Lessons

- The MCP handler sat outside the connect interceptors, so successful calls
  left no log line or RPC metric. "No MCP lines in the pod logs" was weak
  evidence of no use; the stronger evidence was the Istio VirtualService,
  which never routed `/mcp/`, so the gateway answered `404 NR`.
- A CORS preflight to `/mcp/` still gets 204 from `rs/cors` before the mux;
  only the actual request is a 404.
