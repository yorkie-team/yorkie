---
title: tree-schema
target: yorkie
date: 2026-03-06
---

# Tree-Level Schema

## Summary

Extend Yorkie's existing Element-Level schema system to support structural
constraints inside `yorkie.Tree`. This enables ProseMirror-compatible node type
definitions, content expressions, and mark constraints so that Tree.Edit() and
Tree.Style() operations can be validated against a declared schema.

## Goals

- Define allowed node types, content models, and marks for Tree nodes via the
  existing Yorkie schema DSL.
- Validate Tree structure at the client side during `document.Update()`.
- Maintain backward compatibility with existing schemas that use `yorkie.Tree`
  without internal constraints.
- Use best-effort validation for concurrent editing: local edits are validated,
  remote changes are applied without validation.

## Non-Goals

- Server-side Tree schema validation (client-only for now).
- Full ProseMirror NodeSpec support (attrs, isolating, selectable, etc.).
- Strict rejection of concurrent-edit schema violations.

## Motivation

The current schema system validates document-level field types via JSONPath
rules (e.g., `$.content` must be `yorkie.Tree`). However, it cannot express
constraints on the internal structure of a Tree: which node types are allowed,
what children each node type can contain, or which marks can be applied.

Rich-text editors like ProseMirror define schemas with content expressions and
marks. Without Tree-level schema support in Yorkie, structural validation can
only happen in the editor layer, with no shared definition between clients.

## Design

### 1. Schema Definition Syntax

Extend the ANTLR grammar for `yorkie.Tree` to accept a `TreeSchema` type
argument defining node types:

```typescript
type Document = {
  content: yorkie.Tree<TreeSchema>;
};

type TreeSchema = {
  doc: {
    content: "paragraph+";
  };
  paragraph: {
    content: "text*";
    marks: "bold italic link";
    group: "block";
  };
  heading: {
    content: "text*";
    marks: "bold";
    group: "block";
  };
  blockquote: {
    content: "block+";
    group: "block";
  };
  text: {};
};
```

Each node definition has:
- `content`: ProseMirror-compatible content expression defining allowed children.
- `marks`: Space-separated list of allowed mark types.
- `group`: Group name(s) used in content expressions of other nodes.

#### ANTLR Grammar Extension

```antlr
yorkieType
    : 'yorkie.Object' typeArguments?
    | 'yorkie.Array' typeArguments?
    | 'yorkie.Counter'
    | 'yorkie.Text'
    | 'yorkie.Tree' ('<' treeSchemaBody '>')?
    ;

treeSchemaBody
    : treeNodeDef (',' treeNodeDef)*
    ;

treeNodeDef
    : Identifier ':' '{' treeNodeProps '}'
    ;

treeNodeProps
    : treeNodeProp (';' treeNodeProp)* ';'?
    ;

treeNodeProp
    : 'content' ':' StringLiteral
    | 'marks' ':' StringLiteral
    | 'group' ':' StringLiteral
    ;
```

### 2. Rule Representation

Extend the protobuf `Rule` message with a repeated `TreeNodeRule` field:

```protobuf
message Rule {
  string path = 1;
  string type = 2;
  repeated TreeNodeRule tree_nodes = 3;
}

message TreeNodeRule {
  string node_type = 1;  // e.g. "paragraph"
  string content = 2;    // content expression: "text*"
  string marks = 3;      // "bold italic"
  string group = 4;      // "block"
}
```

When `tree_nodes` is empty, behavior is identical to today (type-only check).
When populated, the client uses these rules to validate Tree internal structure.

Example rule output:

```json
{
  "path": "$.content",
  "type": "yorkie.Tree",
  "tree_nodes": [
    { "node_type": "doc", "content": "paragraph+", "marks": "", "group": "" },
    { "node_type": "paragraph", "content": "text*", "marks": "bold italic", "group": "block" },
    { "node_type": "heading", "content": "text*", "marks": "bold", "group": "block" },
    { "node_type": "text", "content": "", "marks": "", "group": "" }
  ]
}
```

### 3. Content Expression Parser

A parser for ProseMirror-compatible content expressions, implemented in the JS
SDK at `packages/schema/src/content-expression.ts`.

#### Grammar

```
expr       → sequence ('|' sequence)*
sequence   → element+
element    → atom quantifier?
atom       → name | '(' expr ')'
quantifier → '+' | '*' | '?' | '{' count (',' count?)? '}'
name       → identifier (matched against node types or group names)
```

#### Interface

```typescript
interface ContentExpr {
  type: 'sequence' | 'alternative' | 'repeat' | 'node';
  children?: ContentExpr[];
  nodeType?: string;
  min?: number;
  max?: number;  // Infinity for unbounded
}

function parseContentExpression(expr: string): ContentExpr;

function matchContentExpression(
  expr: ContentExpr,
  childTypes: string[],
  groupResolver: (name: string) => string[]
): { valid: boolean; error?: string };
```

#### Group Resolution

```typescript
function buildGroupResolver(treeNodes: TreeNodeRule[]): (name: string) => string[] {
  const groups = new Map<string, string[]>();
  for (const node of treeNodes) {
    if (node.group) {
      for (const g of node.group.split(' ')) {
        if (!groups.has(g)) groups.set(g, []);
        groups.get(g)!.push(node.nodeType);
      }
    }
  }
  return (name) => groups.get(name) ?? [name];
}
```

### 4. Validation Logic

#### Validation Timing

Validation runs at the same point as existing Element-Level validation: after
`document.Update()` completes, the entire document is validated against schema
rules. If a rule has `tree_nodes`, the referenced Tree is additionally validated
for structural conformance.

#### Validation Flow

```
ValidateYorkieRuleset(data, rules)
  │
  ├─ For each rule:
  │   ├─ Existing: check type at path ($.content → yorkie.Tree ✓)
  │   └─ New: if rule.tree_nodes is non-empty:
  │       └─ validateTreeSchema(tree, rule.tree_nodes)
  │
  └─ Return combined ValidationResult
```

#### Tree Validation Implementation

```typescript
function validateTreeAgainstSchema(
  tree: CRDTTree,
  treeNodes: TreeNodeRule[]
): ValidationResult {
  const nodeMap = new Map(treeNodes.map(n => [n.nodeType, n]));
  const groupResolver = buildGroupResolver(treeNodes);
  return validateNode(tree.getRoot(), nodeMap, groupResolver);
}

function validateNode(
  node: TreeNode,
  nodeMap: Map<string, TreeNodeRule>,
  groupResolver: (name: string) => string[]
): ValidationResult {
  // 1. Node type must be defined in schema
  const rule = nodeMap.get(node.type);
  if (!rule) {
    return { valid: false, error: `Unknown node type: ${node.type}` };
  }

  // 2. Validate children against content expression
  if (rule.content) {
    const expr = parseContentExpression(rule.content);
    const childTypes = node.children
      .filter(c => !c.isRemoved())
      .map(c => c.type);
    const match = matchContentExpression(expr, childTypes, groupResolver);
    if (!match.valid) return match;
  }

  // 3. Validate marks on text children
  if (rule.marks !== undefined) {
    const allowedMarks = new Set(rule.marks.split(' ').filter(Boolean));
    for (const child of node.children) {
      if (!child.isText()) continue;
      for (const mark of child.marks()) {
        if (!allowedMarks.has(mark)) {
          return { valid: false, error: `Mark "${mark}" not allowed in ${node.type}` };
        }
      }
    }
  }

  // 4. Recurse into child element nodes
  for (const child of node.children) {
    if (child.isText()) continue;
    const result = validateNode(child, nodeMap, groupResolver);
    if (!result.valid) return result;
  }

  return { valid: true };
}
```

#### Concurrent Editing Policy (Best-Effort)

- **Local edits**: Validated via `document.Update()` → schema violation rejects
  the update with `ErrSchemaValidationFailed`.
- **Remote changes**: Applied without validation. Temporary schema violations
  may exist after applying remote changes.
- **Next local edit**: Validated against the current tree state, which may
  include remote changes. If the tree is in a violated state, the user must fix
  the structure before their next edit succeeds.

### 5. Server-Side Changes

The Go server stores and transmits `TreeNodeRule` data but does not validate
Tree structure. Changes required:

- **Protobuf**: Add `TreeNodeRule` message and `tree_nodes` field to `Rule`.
- **Go types**: Add `TreeNodeRule` struct to `api/types/schema.go` and
  `TreeNodes` field to `Rule`.
- **Converter**: Update `from_pb.go` / `to_pb.go` to handle `tree_nodes`.
- **Database**: Store `tree_nodes` as part of Rule in MongoDB schema documents.
- **Validation** (`ruleset_validator.go`): No changes needed (type-check only).

### 6. JS SDK Changes

- **ANTLR grammar** (`YorkieSchema.g4`): Add tree schema syntax.
- **RulesetBuilder** (`rulesets.ts`): Parse `TreeSchema` type argument, emit
  `TreeNodeRule` array in the rule output.
- **Content expression parser** (`content-expression.ts`): New file implementing
  the parser and matcher.
- **Validation hook**: After `document.Update()`, if the rule has `tree_nodes`,
  call `validateTreeAgainstSchema()`.
- **Semantic validator** (`validator.ts`): Validate tree schema definitions
  (e.g., all referenced node types and groups exist, content expressions parse
  correctly).

## Affected Components

| Component | Repository | Changes |
|-----------|-----------|---------|
| Protobuf definitions | yorkie | Add TreeNodeRule message |
| Go types & converter | yorkie | Add TreeNodeRule struct, update converters |
| MongoDB schema | yorkie | Store tree_nodes in rule documents |
| ANTLR grammar | yorkie-js-sdk | Extend yorkie.Tree syntax |
| RulesetBuilder | yorkie-js-sdk | Parse tree schema, emit TreeNodeRule |
| Content expression parser | yorkie-js-sdk | New parser + matcher |
| Document validation | yorkie-js-sdk | Tree structure validation |
| Semantic validator | yorkie-js-sdk | Validate tree schema definitions |

## Risks

- **Content expression complexity**: The parser must handle ProseMirror's full
  content expression syntax including groups, sequences, and alternatives.
  Incremental implementation is possible (start with simple expressions).
- **Performance**: Recursive tree validation on every `document.Update()` could
  be slow for large trees. Optimization: only validate affected subtree.
- **Cross-SDK support**: iOS/Android SDKs would need their own content
  expression parsers to support client-side validation. This is deferred.
