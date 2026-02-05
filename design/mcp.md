# MCP (Model Context Protocol) Integration

## Summary

This document describes the MCP integration for Yorkie, which allows AI assistants (like Claude) to interact with Yorkie documents for tasks such as document migration, schema management, and inspection.

### Goals

The primary goal is to help developers migrate their documents when they change their editor models. With MCP, AI assistants can:

1. Inspect document structures to understand what needs to change
2. Check if documents are safe to migrate (no active editors)
3. Preview transformations before applying them
4. Generate migration code using the Yorkie Admin API
5. Suggest schemas based on document analysis

### Non-Goals

- Real-time collaborative editing via MCP
- Direct document mutations through MCP (migration code is generated instead)

## Proposal Details

### Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         Yorkie Cloud Server                             │
├─────────────────────────────────────────────────────────────────────────┤
│  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────────┐  │
│  │  YorkieService   │  │  AdminService    │  │  MCPService          │  │
│  │  /yorkie.v1.     │  │  /yorkie.v1.     │  │  /mcp/               │  │
│  │  YorkieService/  │  │  AdminService/   │  │                      │  │
│  └──────────────────┘  └──────────────────┘  └──────────────────────┘  │
│           ↑                    ↑                       ↑               │
│           │                    │                       │               │
│      API Key auth         API Key auth            API Key auth         │
└─────────────────────────────────────────────────────────────────────────┘
                                                         ↑
                                                         │
                                              Claude Desktop / AI Agents
```

### Protocol

MCP uses JSON-RPC 2.0 over HTTP:

```
POST /mcp/
Authorization: API-Key <project-secret-key>
Content-Type: application/json

{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "list_documents",
    "arguments": {"page_size": 10}
  }
}
```

### Available Tools

| Tool | Description | Use Case |
|------|-------------|----------|
| `list_documents` | List all documents with metadata | Find documents to migrate |
| `get_document` | Get document content (YSON format) | Inspect document structure |
| `search_documents` | Search documents by key pattern | Find specific documents |
| `list_schemas` | List all schemas in project | Check existing schemas |
| `get_schema` | Get schema definition and rules | Understand validation rules |
| `check_migration_safety` | Check if documents have active editors | Ensure safe migration |
| `get_document_history` | View document change history | Understand document evolution |
| `preview_transform` | Dry-run transformation | Test before applying |
| `infer_schema` | Suggest schema from documents | Create new schemas |
| `generate_migration_code` | Generate migration scripts | TypeScript/Go/curl code |

### Authentication

MCP uses the same API-Key authentication as the Admin API:

```
Authorization: API-Key <project-secret-key>
```

The secret key is available in the Yorkie Dashboard or via the Admin API.

### Usage with Claude Desktop

1. Add to Claude Desktop configuration (`~/.config/claude/claude_desktop_config.json` on Linux/macOS):

```json
{
  "mcpServers": {
    "yorkie": {
      "url": "https://api.yorkie.dev/mcp/",
      "headers": {
        "Authorization": "API-Key sk_your_project_secret_key"
      }
    }
  }
}
```

2. Ask Claude to help with migration:

```
User: "I need to migrate my editor documents. I'm adding an 'indent'
       attribute to paragraph blocks. Can you help?"

Claude:
- Calls list_documents to find documents
- Calls get_document to inspect structure
- Calls check_migration_safety to verify no active editors
- Calls preview_transform to show before/after
- Calls generate_migration_code to create a migration script
```

### Migration Workflow Example

```
Developer: "Migrate documents to add indent field to paragraphs"

AI Agent:
┌─────────────────────────────────────────────────────────────────┐
│ 1. Inspect current documents                                    │
│    MCP: list_documents() → Found 47 documents                   │
│    MCP: get_document("doc-001") → Shows Tree structure          │
│                                                                 │
│ 2. Check safety                                                 │
│    MCP: check_migration_safety(["doc-001", ...])                │
│    → 45 safe, 2 have active editors                             │
│                                                                 │
│ 3. Preview transformation                                       │
│    MCP: preview_transform("doc-001", operations)                │
│    → Shows before/after diff                                    │
│                                                                 │
│ 4. Generate migration code                                      │
│    MCP: generate_migration_code(operations, "typescript")       │
│    → Returns runnable migration script                          │
└─────────────────────────────────────────────────────────────────┘
```

### Transform Operations

The `preview_transform` tool accepts operations in this format:

```json
{
  "operations": [
    {"op": "set", "path": "$.field", "value": "new value"},
    {"op": "delete", "path": "$.oldField"},
    {"op": "rename", "path": "$.oldName", "to": "newName"}
  ]
}
```

Supported operations:
- `set`: Set a field to a new value
- `delete`: Remove a field
- `rename`: Rename a field

### Generated Migration Code

The `generate_migration_code` tool generates scripts in:
- TypeScript (default)
- Go
- curl/bash

Example TypeScript output:

```typescript
// Yorkie Migration Script (TypeScript)
// Generated for project: my-editor
//
// Usage:
//   YORKIE_API_URL=https://api.yorkie.dev \
//   YORKIE_SECRET_KEY=<your-secret-key> \
//   npx ts-node migration.ts

const API_URL = process.env.YORKIE_API_URL || 'https://api.yorkie.dev';
const SECRET_KEY = process.env.YORKIE_SECRET_KEY!;

// ... migration logic ...
```

## Implementation

### Files

```
server/rpc/mcp/
├── types.go        # JSON-RPC 2.0 and MCP protocol types
├── handler.go      # Main HTTP handler for MCP endpoint
├── tools.go        # Tool definitions and implementations
├── errors.go       # Custom errors
└── handler_test.go # Unit tests
```

### Registration

The MCP handler is registered in `server/rpc/server.go`:

```go
mux.Handle(mcp.NewHandler(be))  // Returns ("/mcp/", http.Handler)
```

## Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Documents modified while being migrated | `check_migration_safety` tool verifies no active editors |
| Invalid transformations | `preview_transform` allows dry-run testing |
| Schema mismatches | `infer_schema` helps create correct schemas |
| Code generation errors | Generated code includes error handling |

## Future Work

1. **Batch operations**: Add batch document update tool for atomic migrations
2. **Revision support**: Add tools to create/restore revisions for rollback
3. **Schema validation**: Add tool to validate documents against schemas
4. **Webhook integration**: Notify when migrations complete

## References

- [MCP Specification](https://modelcontextprotocol.io/specification)
- [Yorkie Admin API](https://yorkie.dev/docs/admin-api)
- [YSON Format](https://yorkie.dev/docs/advanced/yson)
