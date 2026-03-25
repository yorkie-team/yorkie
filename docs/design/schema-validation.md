---
title: schema-validation
target: yorkie
---

# Schema Validation

## Summary

Schema Validation is a system that allows users to declaratively define document
structures and validate changes in real time during collaborative editing.

The system consists of two main devices:

- **Device A (Schema Validator)**: Validates the schema definition itself (syntax
  and semantic analysis of the DSL).
- **Device B (Change Validator)**: Validates document changes against schema rules
  during `document.Update()`.

For Tree-level schema details, see [tree-schema.md](tree-schema.md).

### Goals

- Define document structures using a TypeScript Type Alias-based Schema DSL with
  support for primitive types and Yorkie built-in types.
- Validate schema definitions at authoring time (duplicate types, undefined
  references, circular references, unreachable types).
- Validate document changes at runtime using JSONPath-based rules.
- Manage schema versions immutably and bind schemas to documents.
- Use best-effort validation for concurrent editing: local edits are validated,
  remote changes are applied without validation.

### Non-Goals

- Server-side real-time change validation (client-only for now).
- Automatic schema migration when versions change.
- Client-side validation in iOS/Android SDKs (deferred).

## Proposal Details

### System Architecture

The overall system spans three repositories: `yorkie` (server), `yorkie-js-sdk`
(SDK), and `dashboard` (web UI).

```
User ──→ Dashboard ──→ Device A (Schema Validator) ──→ Server
                                                         │
                                                    Store Schema
                                                         │
User ──→ SDK ──→ client.attach(doc, {schema}) ←── Schema Rules
         │
         └──→ document.Update() ──→ Device B (Change Validator)
                                       │
                                  ┌────┴────┐
                                  │ Valid   │ Invalid
                                  ▼         ▼
                             Apply Change  Throw Error
```

**Sequence: Document Attach and Update with Schema**

```
Editor          SDK               Change Validator       Server
  │               │                     │                   │
  ├─ Attach ─────→│                     │                   │
  │               ├─ AttachDocument ────────────────────────→│
  │               │←── Document + Schema Rules ─────────────┤
  │               ├─ Build Ruleset ────→│                   │
  │               │←── Ruleset Ready ───┤                   │
  │←─ Attached ───┤                     │                   │
  │               │                     │                   │
  ├─ Update ─────→│                     │                   │
  │               ├─ Validate Change ──→│                   │
  │               │                     ├─ Check Rules      │
  │               │←── Pass / Fail ─────┤                   │
  │←─ Success / Error                   │                   │
```

### 1. Schema DSL

Schemas are defined using TypeScript Type Alias syntax, parsed by an ANTLR4
grammar (`YorkieSchema.g4` in `yorkie-js-sdk/packages/schema/`).

```typescript
type Document = {
  title: string;
  content: yorkie.Tree<{
    doc: { content: "block+"; };
    paragraph: { content: "text*"; marks: "bold italic"; group: "block"; };
    heading: { content: "text*"; marks: "bold"; group: "block"; };
    text: {};
  }>;
  tags: yorkie.Array<string>;
  counter: yorkie.Counter;
};
```

**Supported types:**

| Category | Types |
|----------|-------|
| Primitive | `string`, `boolean`, `integer`, `double`, `long`, `date`, `bytes`, `null` |
| Yorkie | `yorkie.Text`, `yorkie.Tree`, `yorkie.Counter`, `yorkie.Object`, `yorkie.Array` |
| Structural | Generic type arguments, union types, optional properties |

### 2. Device A: Schema Validator

The Schema Validator (`yorkie-js-sdk/packages/schema/src/validator.ts`) performs
syntax and semantic analysis on schema DSL input, reporting diagnostics with
line/column information.

**Validation checks:**

- Duplicate type declarations
- Duplicate property names within a type
- Undefined type references
- Circular type references (cycle detection)
- Unreachable types (types not connected to the `Document` root)

### 3. Device B: Change Validator

The Change Validator runs in the SDK after `document.Update()` completes. It
consists of two stages: Ruleset Building and Runtime Validation.

#### 3.1 Ruleset Builder

The Ruleset Builder (`yorkie-js-sdk/packages/schema/src/rulesets.ts`) converts
a parsed schema AST into a flat array of `Rule` objects.

**Rule types:**

| Rule | Description |
|------|-------------|
| `PrimitiveRule` | Type check at a JSONPath (e.g., `$.title` → `string`) |
| `ObjectRule` | Object with required/optional properties |
| `ArrayRule` | Array with item type constraint |
| `YorkieTypeRule` | Yorkie CRDT type, optionally with `TreeNodeRule[]` |
| `EnumRule` | Union of literal values |

Each rule has a `path` field using JSONPath notation (e.g., `$.content`,
`$.settings.theme[*].name`) and a `type` field.

#### 3.2 Element-Level Validation

The runtime validator (`ruleset_validator.ts` in JS SDK, `ruleset_validator.go`
in Go server) checks each rule against the document state:

1. Navigate to the target value using JSONPath (`getValueByPath`).
2. Check the value's type matches the rule's expected type.
3. Collect all validation errors with path and message.

```go
// Go server: pkg/schema/ruleset_validator.go
func ValidateYorkieRuleset(data *crdt.Object, rules []types.Rule) ValidationResult
```

```typescript
// JS SDK: packages/sdk/src/document/schema/ruleset_validator.ts
function validateYorkieRuleset(data: CRDTObject, ruleset: Rule[]): ValidationResult
```

#### 3.3 Tree-Level Validation

When a rule has `tree_nodes`, the Tree structure is additionally validated for
content expressions, allowed marks, and node type definitions. See
[tree-schema.md](tree-schema.md) for details.

#### 3.4 Validation Timing

Validation runs after `document.Update()` completes, before changes are applied
to the Root:

```typescript
// JS SDK: packages/sdk/src/document/document.ts
update(updater, message?) {
  // 1. Apply edits to Clone, generate Changes
  // 2. Validate Clone against schema rules
  const result = validateYorkieRuleset(this.clone.root, rules);
  if (!result.valid) {
    this.clone = undefined;  // Discard changes
    throw new YorkieError(ErrDocumentSchemaValidationFailed, ...);
  }
  // 3. Apply Changes to Root
}
```

```go
// Go server: pkg/document/document.go
func (d *Document) Update(...) {
    // ... apply changes to clone ...
    result := schema.ValidateYorkieRuleset(cloneRoot, d.schemaRules)
    if !result.Valid {
        return ErrSchemaValidationFailed
    }
    // ... apply to root ...
}
```

### 4. Concurrent Editing Policy

Schema validation follows a **best-effort** approach:

- **Local edits**: Validated during `document.Update()`. Violations reject the
  update with `ErrSchemaValidationFailed`.
- **Remote changes**: Applied without validation. Temporary schema violations may
  exist after applying remote changes.
- **Recovery**: When the local tree is in a violated state due to remote changes,
  the user must fix the structure before their next local edit succeeds.

### 5. Protobuf Representation

```protobuf
// api/yorkie/v1/resources.proto
message Rule {
  string path = 1;
  string type = 2;
  repeated TreeNodeRule tree_nodes = 3;
}

message TreeNodeRule {
  string node_type = 1;
  string content = 2;    // ProseMirror-compatible content expression
  string marks = 3;      // Space-separated allowed mark types
  string group = 4;      // Group name for content expressions
}
```

**Go types** (`api/types/schema.go`):

```go
type Rule struct {
    Path      string
    Type      string
    TreeNodes []TreeNodeRule
}

type TreeNodeRule struct {
    NodeType string
    Content  string
    Marks    string
    Group    string
}

type Schema struct {
    ID        types.ID
    Name      string
    Version   int
    Body      string
    Rules     []Rule
    CreatedAt time.Time
}
```

### 6. Schema Lifecycle

Schemas are managed through Admin RPCs defined in `api/yorkie/v1/admin.proto`:

| RPC | Description |
|-----|-------------|
| `CreateSchema` | Create a new schema with name, body, and rules |
| `GetSchema` | Get a specific schema version |
| `GetSchemas` | Get all versions of a schema by name |
| `ListSchemas` | List latest version of all schemas in a project |
| `RemoveSchema` | Remove a specific schema version |

**Version management:**

- Schemas are immutable; updates create new versions (v1, v2, v3, ...).
- Version numbers start at 1.
- Reserved schema names (e.g., `"new"`) are rejected at creation time.
- `IsSchemaAttached` checks referential integrity before deletion.

**Storage** (`server/backend/database/schema_info.go`):

- `SchemaInfo` struct stored in MongoDB with project scoping.
- In-memory database implementation for testing.

### 7. Document-Schema Binding

Schemas are bound to documents at attach time:

```typescript
// JS SDK
const doc = new yorkie.Document('doc1');
await client.attach(doc, {
  schema: 'doc-schema@1',  // optional: "name@version"
});
```

**Attach flow:**

1. Client sends `AttachDocumentRequest` with `schemaKey` to server.
2. Server looks up the schema, retrieves `Rule[]`.
3. Server validates the existing document root against the new schema rules.
4. Server returns `AttachDocumentResponse` with schema rules.
5. Client calls `doc.setSchemaRules(rules)` to enable validation.

**Constraints:**

- Schema attachment is validated against the current document state.
- Schema conflicts are detected when multiple clients attempt different schemas.
- Schema updates on already-attached documents are rejected.

### 8. Dashboard Integration

The Dashboard (`yorkie-team/dashboard`) provides a web UI for schema management:

- **Schema CRUD**: Create, view versions, edit (as new version), and delete schemas.
- **Schema editor**: CodeMirror-based DSL editor with syntax highlighting.
- **Document association**: Documents display their linked `schemaKey` as a badge
  in list and detail views.

## Current Status

| Component | Status |
|-----------|--------|
| Schema DSL grammar (ANTLR) | Done |
| Schema Validator (Device A) | Done |
| Ruleset Builder | Done |
| Element-Level Validation (all types) | Done |
| Tree-Level Schema Definition | Done |
| Tree-Level Validation | Done |
| Schema Admin RPCs | Done |
| Schema Version Management | Done |
| Document-Schema Binding | Done |
| Dashboard Schema CRUD | Done |
| Dashboard: documents display schema badge | Done |
| Dashboard: schema page shows linked documents | Not started |
| Error Event Handler for schema conflicts | Not started |
| Wafflebase real-document testing | Not started |
| User Documentation | Not started |
| JS SDK Examples | Not started |

### Risks and Mitigation

- **Performance**: Recursive tree validation on every `document.Update()` could
  be slow for large documents. Mitigation: validate only the affected subtree
  (not yet implemented).
- **Cross-SDK consistency**: Only the JS SDK currently implements client-side
  validation. iOS/Android SDKs would need their own content expression parsers.
- **Offline support**: The SDK includes validation logic locally, but a
  comprehensive offline strategy (temporary validation, sync reconciliation)
  is not yet defined.
- **Schema migration**: No automated migration process exists for updating
  existing documents when schema versions change. A backward compatibility plan
  is needed.
- **Error handling**: Schema violations during concurrent editing are currently
  surfaced only as thrown exceptions. An event-based error handler would allow
  custom conflict resolution strategies.
