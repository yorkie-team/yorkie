# Remove the MCP server

**Created**: 2026-10-22

## Why

The `/mcp/` endpoint (#1659) has no route on Yorkie Cloud: the Istio
VirtualService matches only `/auth` and `/yorkie.v1`, so the gateway answers
`404 NR route_not_found` for `/mcp/`. Pod and gateway logs show no MCP
traffic. The handler is registered unconditionally with no flag, so it is
dead surface that still authenticates with the project secret key.

## Plan

- [x] Delete `server/rpc/mcp/` and `test/integration/mcp_test.go`
- [x] Drop the handler registration in `server/rpc/server.go`
- [x] Delete `docs/design/mcp.md` and its index entry
- [x] Update comments and design docs that list MCP as a user of the secret
      key (`config.go`, `projects.go`, `admin-credentials.md`,
      `document-size-limit.md`)
- [x] Add an Unreleased `Removed` entry to `CHANGELOG.md`
- [x] `make verify`
- [ ] Open PR; homepage docs (`docs/tools/mcp.mdx`) removed in a paired PR

## Out of scope

- Helpers in `server/documents` and `server/schemas` that MCP called: each
  still has AdminService callers, so none become dead.
- `scripts/agent/` and the agent workflows: their MCP references are the
  Agent SDK's in-process tools, unrelated to the server endpoint.
- Released CHANGELOG entries and archived task docs stay as history.
