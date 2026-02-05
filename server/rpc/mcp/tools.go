/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/schemas"
)

// registerTools registers all available MCP tools.
func (h *Handler) registerTools() {
	// Document inspection tools
	h.registerTool(&ToolDefinition{
		Tool: Tool{
			Name:        "list_documents",
			Description: "List all documents in the project. Use this to discover documents that may need migration.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"page_size": {
						Type:        "integer",
						Description: "Number of documents to return (default: 20, max: 100)",
						Default:     20,
					},
					"include_root": {
						Type:        "boolean",
						Description: "Include document content in response (default: false). Set to true to inspect document structure.",
						Default:     false,
					},
				},
			},
		},
		Handler: h.listDocuments,
	})

	h.registerTool(&ToolDefinition{
		Tool: Tool{
			Name:        "get_document",
			Description: "Get a document's full content and metadata for migration inspection.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"document_key": {
						Type:        "string",
						Description: "The document key to fetch",
					},
				},
				Required: []string{"document_key"},
			},
		},
		Handler: h.getDocument,
	})

	h.registerTool(&ToolDefinition{
		Tool: Tool{
			Name:        "search_documents",
			Description: "Search for documents by key pattern. Useful for finding documents that match certain criteria.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"query": {
						Type:        "string",
						Description: "Search query to match document keys",
					},
					"page_size": {
						Type:        "integer",
						Description: "Number of results to return (default: 20)",
						Default:     20,
					},
				},
				Required: []string{"query"},
			},
		},
		Handler: h.searchDocuments,
	})

	// Schema management tools
	h.registerTool(&ToolDefinition{
		Tool: Tool{
			Name:        "list_schemas",
			Description: "List all schemas defined in the project. Schemas define document structure validation rules.",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]Property{},
			},
		},
		Handler: h.listSchemas,
	})

	h.registerTool(&ToolDefinition{
		Tool: Tool{
			Name:        "get_schema",
			Description: "Get a schema's definition and validation rules.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"schema_name": {
						Type:        "string",
						Description: "Name of the schema",
					},
					"version": {
						Type:        "integer",
						Description: "Schema version (optional, defaults to latest)",
					},
				},
				Required: []string{"schema_name"},
			},
		},
		Handler: h.getSchema,
	})

	// Migration helper tools
	h.registerTool(&ToolDefinition{
		Tool: Tool{
			Name:        "check_migration_safety",
			Description: "Check if documents are safe to migrate (no active editors).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"document_keys": {
						Type:        "array",
						Description: "List of document keys to check",
						Items:       &Property{Type: "string"},
					},
				},
				Required: []string{"document_keys"},
			},
		},
		Handler: h.checkMigrationSafety,
	})

	h.registerTool(&ToolDefinition{
		Tool: Tool{
			Name:        "get_document_history",
			Description: "Get the change history of a document. Useful for understanding how the document evolved.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"document_key": {
						Type:        "string",
						Description: "The document key",
					},
					"limit": {
						Type:        "integer",
						Description: "Maximum number of changes to return (default: 50)",
						Default:     50,
					},
				},
				Required: []string{"document_key"},
			},
		},
		Handler: h.getDocumentHistory,
	})

	// Advanced migration tools
	h.registerTool(&ToolDefinition{
		Tool: Tool{
			Name:        "preview_transform",
			Description: "Preview a transformation on a document without applying (dry-run).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"document_key": {
						Type:        "string",
						Description: "The document key to transform",
					},
					"operations": {
						Type:        "array",
						Description: "List of transform operations to apply",
						Items: &Property{
							Type:        "object",
							Description: "Operation: {op, path, value}",
						},
					},
				},
				Required: []string{"document_key", "operations"},
			},
		},
		Handler: h.previewTransform,
	})

	h.registerTool(&ToolDefinition{
		Tool: Tool{
			Name:        "infer_schema",
			Description: "Analyze documents and suggest a schema based on their structure.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"document_keys": {
						Type:        "array",
						Description: "Document keys to analyze (samples)",
						Items:       &Property{Type: "string"},
					},
					"max_samples": {
						Type:        "integer",
						Description: "Max documents to analyze (default: 5)",
						Default:     5,
					},
				},
			},
		},
		Handler: h.inferSchema,
	})

	h.registerTool(&ToolDefinition{
		Tool: Tool{
			Name:        "generate_migration_code",
			Description: "Generate migration code for transforming documents.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"document_key": {
						Type:        "string",
						Description: "Sample document to base migration on",
					},
					"operations": {
						Type:        "array",
						Description: "Transform operations to generate code for",
						Items: &Property{
							Type:        "object",
							Description: "Operation: {op, path, value}",
						},
					},
					"language": {
						Type:        "string",
						Description: "Target language: typescript, go, curl (default: typescript)",
						Default:     "typescript",
						Enum:        []string{"typescript", "go", "curl"},
					},
				},
				Required: []string{"operations"},
			},
		},
		Handler: h.generateMigrationCode,
	})
}

// registerTool adds a tool definition to the handler.
func (h *Handler) registerTool(def *ToolDefinition) {
	h.tools[def.Tool.Name] = def
}

// Tool handler implementations

func (h *Handler) listDocuments(ctx context.Context, project *types.Project, params json.RawMessage) (any, error) {
	var input struct {
		PageSize    int  `json:"page_size"`
		IncludeRoot bool `json:"include_root"`
	}
	input.PageSize = 20 // default

	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
	}

	if input.PageSize <= 0 {
		input.PageSize = 20
	}
	if input.PageSize > 100 {
		input.PageSize = 100
	}

	docs, err := documents.ListDocumentSummaries(
		ctx,
		h.backend,
		project,
		types.Paging[types.ID]{
			PageSize:  input.PageSize,
			IsForward: true,
		},
		input.IncludeRoot,
	)
	if err != nil {
		return nil, err
	}

	// Transform to MCP-friendly format
	result := make([]map[string]any, 0, len(docs))
	for _, doc := range docs {
		item := map[string]any{
			"key":              doc.Key.String(),
			"id":               doc.ID.String(),
			"attached_clients": doc.AttachedClients,
			"safe_to_migrate":  doc.AttachedClients == 0,
			"schema_key":       doc.SchemaKey,
			"created_at":       doc.CreatedAt,
			"updated_at":       doc.UpdatedAt,
		}
		if input.IncludeRoot && doc.Root != "" {
			item["root"] = doc.Root
		}
		result = append(result, item)
	}

	return map[string]any{
		"documents":    result,
		"count":        len(result),
		"project_name": project.Name,
	}, nil
}

func (h *Handler) getDocument(ctx context.Context, project *types.Project, params json.RawMessage) (any, error) {
	var input struct {
		DocumentKey string `json:"document_key"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}

	if input.DocumentKey == "" {
		return nil, fmt.Errorf("document_key is required")
	}

	doc, err := documents.GetDocumentSummary(
		ctx,
		h.backend,
		project,
		key.Key(input.DocumentKey),
	)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"key":              doc.Key.String(),
		"id":               doc.ID.String(),
		"root":             doc.Root,
		"attached_clients": doc.AttachedClients,
		"safe_to_migrate":  doc.AttachedClients == 0,
		"schema_key":       doc.SchemaKey,
		"doc_size": map[string]any{
			"live": doc.DocSize.Live,
			"gc":   doc.DocSize.GC,
		},
		"created_at":  doc.CreatedAt,
		"accessed_at": doc.AccessedAt,
		"updated_at":  doc.UpdatedAt,
	}, nil
}

func (h *Handler) searchDocuments(ctx context.Context, project *types.Project, params json.RawMessage) (any, error) {
	var input struct {
		Query    string `json:"query"`
		PageSize int    `json:"page_size"`
	}
	input.PageSize = 20

	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}

	if input.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	result, err := documents.SearchDocumentSummaries(
		ctx,
		h.backend,
		project,
		input.Query,
		input.PageSize,
	)
	if err != nil {
		return nil, err
	}

	docs := make([]map[string]any, 0, len(result.Elements))
	for _, doc := range result.Elements {
		docs = append(docs, map[string]any{
			"key":        doc.Key.String(),
			"id":         doc.ID.String(),
			"schema_key": doc.SchemaKey,
			"created_at": doc.CreatedAt,
			"updated_at": doc.UpdatedAt,
		})
	}

	return map[string]any{
		"documents":   docs,
		"total_count": result.TotalCount,
	}, nil
}

func (h *Handler) listSchemas(ctx context.Context, project *types.Project, params json.RawMessage) (any, error) {
	schemaList, err := schemas.ListSchemas(ctx, h.backend, project.ID)
	if err != nil {
		return nil, err
	}

	result := make([]map[string]any, 0, len(schemaList))
	for _, s := range schemaList {
		result = append(result, map[string]any{
			"name":       s.Name,
			"version":    s.Version,
			"created_at": s.CreatedAt,
		})
	}

	return map[string]any{
		"schemas": result,
		"count":   len(result),
	}, nil
}

func (h *Handler) getSchema(ctx context.Context, project *types.Project, params json.RawMessage) (any, error) {
	var input struct {
		SchemaName string `json:"schema_name"`
		Version    int    `json:"version"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}

	if input.SchemaName == "" {
		return nil, fmt.Errorf("schema_name is required")
	}

	var schema *types.Schema
	var err error

	if input.Version > 0 {
		schema, err = schemas.GetSchema(ctx, h.backend, project.ID, input.SchemaName, input.Version)
	} else {
		// Get latest version
		schemaList, err := schemas.GetSchemas(ctx, h.backend, project.ID, input.SchemaName)
		if err != nil {
			return nil, err
		}
		if len(schemaList) == 0 {
			return nil, fmt.Errorf("schema not found: %s", input.SchemaName)
		}
		// Find the latest version
		for _, s := range schemaList {
			if schema == nil || s.Version > schema.Version {
				schema = s
			}
		}
	}

	if err != nil {
		return nil, err
	}

	return map[string]any{
		"name":       schema.Name,
		"version":    schema.Version,
		"body":       schema.Body,
		"rules":      schema.Rules,
		"created_at": schema.CreatedAt,
	}, nil
}

func (h *Handler) checkMigrationSafety(
	ctx context.Context,
	project *types.Project,
	params json.RawMessage,
) (any, error) {
	var input struct {
		DocumentKeys []string `json:"document_keys"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}

	if len(input.DocumentKeys) == 0 {
		return nil, fmt.Errorf("document_keys is required")
	}

	safe := make([]string, 0)
	unsafe := make([]map[string]any, 0)

	for _, docKey := range input.DocumentKeys {
		docInfo, err := documents.FindDocInfoByKey(ctx, h.backend, project, key.Key(docKey))
		if err != nil {
			unsafe = append(unsafe, map[string]any{
				"key":    docKey,
				"reason": "not found: " + err.Error(),
			})
			continue
		}

		count, err := documents.FindAttachedClientCount(ctx, h.backend, docInfo.RefKey())
		if err != nil {
			unsafe = append(unsafe, map[string]any{
				"key":    docKey,
				"reason": "error checking: " + err.Error(),
			})
			continue
		}

		if count > 0 {
			unsafe = append(unsafe, map[string]any{
				"key":              docKey,
				"reason":           "has active editors",
				"attached_clients": count,
			})
		} else {
			safe = append(safe, docKey)
		}
	}

	return map[string]any{
		"safe":         safe,
		"safe_count":   len(safe),
		"unsafe":       unsafe,
		"unsafe_count": len(unsafe),
		"total":        len(input.DocumentKeys),
	}, nil
}

func (h *Handler) getDocumentHistory(ctx context.Context, project *types.Project, params json.RawMessage) (any, error) {
	var input struct {
		DocumentKey string `json:"document_key"`
		Limit       int    `json:"limit"`
	}
	input.Limit = 50

	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}

	if input.DocumentKey == "" {
		return nil, fmt.Errorf("document_key is required")
	}

	docInfo, err := documents.FindDocInfoByKey(ctx, h.backend, project, key.Key(input.DocumentKey))
	if err != nil {
		return nil, err
	}

	// Fetch changes from database
	changes, err := h.backend.DB.FindChangeInfosBetweenServerSeqs(
		ctx,
		docInfo.RefKey(),
		0,
		docInfo.ServerSeq,
	)
	if err != nil {
		return nil, err
	}

	// Limit results (take the most recent changes)
	if len(changes) > input.Limit {
		changes = changes[len(changes)-input.Limit:]
	}

	result := make([]map[string]any, 0, len(changes))
	for _, c := range changes {
		result = append(result, map[string]any{
			"server_seq":       c.ServerSeq,
			"client_seq":       c.ClientSeq,
			"lamport":          c.Lamport,
			"message":          c.Message,
			"operations_count": len(c.Operations),
		})
	}

	return map[string]any{
		"document_key":  input.DocumentKey,
		"changes":       result,
		"count":         len(result),
		"current_seq":   docInfo.ServerSeq,
		"total_changes": docInfo.ServerSeq,
	}, nil
}

// TransformOperation represents a single transformation operation.
type TransformOperation struct {
	Op    string `json:"op"`    // "set", "delete", "rename"
	Path  string `json:"path"`  // JSON path like "$.content.blocks[*].attributes"
	Value any    `json:"value"` // Value for set operations
	To    string `json:"to"`    // New name for rename operations
}

func (h *Handler) previewTransform(
	ctx context.Context,
	project *types.Project,
	params json.RawMessage,
) (any, error) {
	var input struct {
		DocumentKey string               `json:"document_key"`
		Operations  []TransformOperation `json:"operations"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}

	if input.DocumentKey == "" {
		return nil, fmt.Errorf("document_key is required")
	}

	// Get the document
	doc, err := documents.GetDocumentSummary(ctx, h.backend, project, key.Key(input.DocumentKey))
	if err != nil {
		return nil, err
	}

	// Parse the document root as JSON
	var original map[string]any
	if err := json.Unmarshal([]byte(doc.Root), &original); err != nil {
		return nil, fmt.Errorf("failed to parse document: %w", err)
	}

	// Deep copy for transformation
	transformedBytes, err := json.Marshal(original)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal document for copy: %w", err)
	}
	var transformed map[string]any
	if err := json.Unmarshal(transformedBytes, &transformed); err != nil {
		return nil, fmt.Errorf("failed to copy document: %w", err)
	}

	// Apply operations (simplified implementation)
	appliedOps := make([]map[string]any, 0)
	for _, op := range input.Operations {
		result := applyOperation(transformed, op)
		appliedOps = append(appliedOps, map[string]any{
			"op":      op.Op,
			"path":    op.Path,
			"success": result == nil,
			"error":   errorString(result),
		})
	}

	// Generate diff description
	originalJSON, _ := json.MarshalIndent(original, "", "  ")
	transformedJSON, _ := json.MarshalIndent(transformed, "", "  ")

	return map[string]any{
		"document_key":    input.DocumentKey,
		"original":        string(originalJSON),
		"transformed":     string(transformedJSON),
		"operations":      appliedOps,
		"safe_to_migrate": doc.AttachedClients == 0,
		"warning": func() string {
			if doc.AttachedClients > 0 {
				return fmt.Sprintf("Document has %d active editors", doc.AttachedClients)
			}
			return ""
		}(),
	}, nil
}

func (h *Handler) inferSchema(
	ctx context.Context,
	project *types.Project,
	params json.RawMessage,
) (any, error) {
	var input struct {
		DocumentKeys []string `json:"document_keys"`
		MaxSamples   int      `json:"max_samples"`
	}
	input.MaxSamples = 5

	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
	}

	// If no document keys specified, get some samples
	var docKeys []key.Key
	if len(input.DocumentKeys) == 0 {
		docs, err := documents.ListDocumentSummaries(
			ctx, h.backend, project,
			types.Paging[types.ID]{PageSize: input.MaxSamples, IsForward: true},
			true,
		)
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			docKeys = append(docKeys, d.Key)
		}
	} else {
		for _, k := range input.DocumentKeys {
			docKeys = append(docKeys, key.Key(k))
		}
	}

	if len(docKeys) == 0 {
		return nil, fmt.Errorf("no documents to analyze")
	}

	// Analyze document structures
	fieldStats := make(map[string]*FieldStat)
	analyzedDocs := 0

	for i, docKey := range docKeys {
		if i >= input.MaxSamples {
			break
		}

		doc, err := documents.GetDocumentSummary(ctx, h.backend, project, docKey)
		if err != nil {
			continue
		}

		var root map[string]any
		if err := json.Unmarshal([]byte(doc.Root), &root); err != nil {
			continue
		}

		analyzeStructure("$", root, fieldStats)
		analyzedDocs++
	}

	// Generate suggested rules
	rules := generateRulesFromStats(fieldStats, analyzedDocs)

	return map[string]any{
		"analyzed_documents": analyzedDocs,
		"field_statistics":   fieldStats,
		"suggested_rules":    rules,
		"suggested_schema": map[string]any{
			"name":    "inferred-schema",
			"version": 1,
			"rules":   rules,
		},
	}, nil
}

func (h *Handler) generateMigrationCode(
	ctx context.Context,
	project *types.Project,
	params json.RawMessage,
) (any, error) {
	var input struct {
		DocumentKey string               `json:"document_key"`
		Operations  []TransformOperation `json:"operations"`
		Language    string               `json:"language"`
	}
	input.Language = "typescript"

	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}

	// Generate code based on language
	var code string
	switch input.Language {
	case "typescript":
		code = generateTypeScriptMigration(project, input.Operations)
	case "go":
		code = generateGoMigration(project, input.Operations)
	case "curl":
		code = generateCurlMigration(project, input.Operations)
	default:
		code = generateTypeScriptMigration(project, input.Operations)
	}

	return map[string]any{
		"language":   input.Language,
		"code":       code,
		"operations": input.Operations,
		"usage": fmt.Sprintf(
			"Save this code and run with your YORKIE_SECRET_KEY environment variable set.",
		),
	}, nil
}

// Helper functions

func applyOperation(data map[string]any, op TransformOperation) error {
	// Simplified path parsing - handles basic paths like "$.field" or "$.nested.field"
	path := op.Path
	if len(path) > 2 && path[:2] == "$." {
		path = path[2:]
	}

	parts := splitPath(path)
	if len(parts) == 0 {
		return fmt.Errorf("invalid path")
	}

	// Navigate to parent
	current := data
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		if next, ok := current[part].(map[string]any); ok {
			current = next
		} else {
			return fmt.Errorf("path not found: %s", parts[i])
		}
	}

	lastPart := parts[len(parts)-1]

	switch op.Op {
	case "set":
		current[lastPart] = op.Value
	case "delete":
		delete(current, lastPart)
	case "rename":
		if op.To == "" {
			return fmt.Errorf("rename requires 'to' field")
		}
		if val, ok := current[lastPart]; ok {
			current[op.To] = val
			delete(current, lastPart)
		}
	default:
		return fmt.Errorf("unknown operation: %s", op.Op)
	}

	return nil
}

func splitPath(path string) []string {
	// Simple split by "." - doesn't handle array indices yet
	var parts []string
	for _, p := range strings.Split(path, ".") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// FieldStat tracks statistics about a field across documents.
type FieldStat struct {
	Path        string         `json:"path"`
	Types       map[string]int `json:"types"`
	Occurrences int            `json:"occurrences"`
	Required    bool           `json:"required"`
}

func analyzeStructure(path string, data any, stats map[string]*FieldStat) {
	switch v := data.(type) {
	case map[string]any:
		for k, val := range v {
			fieldPath := path + "." + k
			if stats[fieldPath] == nil {
				stats[fieldPath] = &FieldStat{
					Path:  fieldPath,
					Types: make(map[string]int),
				}
			}
			stats[fieldPath].Occurrences++
			stats[fieldPath].Types[inferType(val)]++
			analyzeStructure(fieldPath, val, stats)
		}
	case []any:
		if len(v) > 0 {
			// Analyze first element as representative
			arrayPath := path + "[*]"
			if stats[arrayPath] == nil {
				stats[arrayPath] = &FieldStat{
					Path:  arrayPath,
					Types: make(map[string]int),
				}
			}
			stats[arrayPath].Occurrences++
			stats[arrayPath].Types[inferType(v[0])]++
			analyzeStructure(arrayPath, v[0], stats)
		}
	}
}

func inferType(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case float64, int, int64:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case nil:
		return "null"
	default:
		return "unknown"
	}
}

func generateRulesFromStats(stats map[string]*FieldStat, totalDocs int) []map[string]any {
	rules := make([]map[string]any, 0)
	for path, stat := range stats {
		// Find most common type
		var maxType string
		var maxCount int
		for t, c := range stat.Types {
			if c > maxCount {
				maxType = t
				maxCount = c
			}
		}

		// Only include fields that appear in most documents
		if stat.Occurrences >= totalDocs/2 {
			rules = append(rules, map[string]any{
				"path": path,
				"type": maxType,
			})
		}
	}
	return rules
}

func generateTypeScriptMigration(project *types.Project, ops []TransformOperation) string {
	opsJSON, _ := json.MarshalIndent(ops, "  ", "  ")

	return fmt.Sprintf(`// Yorkie Migration Script (TypeScript)
// Generated for project: %s
//
// Usage:
//   YORKIE_API_URL=https://api.yorkie.dev \\
//   YORKIE_SECRET_KEY=<your-secret-key> \\
//   npx ts-node migration.ts

const API_URL = process.env.YORKIE_API_URL || 'https://api.yorkie.dev';
const SECRET_KEY = process.env.YORKIE_SECRET_KEY!;

interface TransformOp {
  op: 'set' | 'delete' | 'rename';
  path: string;
  value?: any;
  to?: string;
}

const operations: TransformOp[] = %s;

async function adminRequest(method: string, body: any = {}) {
  const response = await fetch(API_URL + '/yorkie.v1.AdminService/' + method, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'authorization': 'API-Key ' + SECRET_KEY,
    },
    body: JSON.stringify(body),
  });
  if (!response.ok) throw new Error(await response.text());
  return response.json();
}

function applyOperations(root: any, ops: TransformOp[]): any {
  const result = JSON.parse(JSON.stringify(root));
  for (const op of ops) {
    // Navigate to path and apply operation
    const parts = op.path.replace(/^\$\./, '').split('.');
    let current = result;
    for (let i = 0; i < parts.length - 1; i++) {
      current = current[parts[i]];
    }
    const lastPart = parts[parts.length - 1];

    switch (op.op) {
      case 'set': current[lastPart] = op.value; break;
      case 'delete': delete current[lastPart]; break;
      case 'rename':
        current[op.to!] = current[lastPart];
        delete current[lastPart];
        break;
    }
  }
  return result;
}

async function migrate(documentKeys: string[]) {
  for (const docKey of documentKeys) {
    console.log('Migrating: ' + docKey);

    const { document } = await adminRequest('GetDocument', { document_key: docKey });
    if (document.attached_clients > 0) {
      console.log('  SKIP: has active editors');
      continue;
    }

    const original = JSON.parse(document.root);
    const transformed = applyOperations(original, operations);

    await adminRequest('UpdateDocument', {
      document_key: docKey,
      root: JSON.stringify(transformed),
    });
    console.log('  OK');
  }
}

// Run migration
migrate(['your-doc-key-here']).catch(console.error);
`, project.Name, string(opsJSON))
}

func generateGoMigration(project *types.Project, ops []TransformOperation) string {
	return fmt.Sprintf(`// Yorkie Migration Script (Go)
// Generated for project: %s
//
// Usage:
//   YORKIE_API_URL=https://api.yorkie.dev \
//   YORKIE_SECRET_KEY=<your-secret-key> \
//   go run migration.go

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

var (
	apiURL    = getEnv("YORKIE_API_URL", "https://api.yorkie.dev")
	secretKey = os.Getenv("YORKIE_SECRET_KEY")
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func adminRequest(method string, body any) (map[string]any, error) {
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", apiURL+"/yorkie.v1.AdminService/"+method, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("authorization", "API-Key "+secretKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

func main() {
	if secretKey == "" {
		fmt.Println("YORKIE_SECRET_KEY is required")
		os.Exit(1)
	}

	// Add your document keys and transformation logic here
	documentKeys := []string{"your-doc-key-here"}

	for _, docKey := range documentKeys {
		fmt.Printf("Migrating: %%s\n", docKey)

		result, err := adminRequest("GetDocument", map[string]string{"document_key": docKey})
		if err != nil {
			fmt.Printf("  ERROR: %%v\n", err)
			continue
		}

		// Apply your transformations here
		doc := result["document"].(map[string]any)
		root := doc["root"].(string)

		// Update document
		_, err = adminRequest("UpdateDocument", map[string]string{
			"document_key": docKey,
			"root":         root, // modified root
		})
		if err != nil {
			fmt.Printf("  ERROR: %%v\n", err)
			continue
		}
		fmt.Println("  OK")
	}
}
`, project.Name)
}

func generateCurlMigration(project *types.Project, ops []TransformOperation) string {
	return fmt.Sprintf(`#!/bin/bash
# Yorkie Migration Script (curl)
# Generated for project: %s
#
# Usage:
#   YORKIE_API_URL=https://api.yorkie.dev \
#   YORKIE_SECRET_KEY=<your-secret-key> \
#   bash migration.sh

API_URL="${YORKIE_API_URL:-https://api.yorkie.dev}"
SECRET_KEY="${YORKIE_SECRET_KEY}"

if [ -z "$SECRET_KEY" ]; then
  echo "YORKIE_SECRET_KEY is required"
  exit 1
fi

# Get document
get_document() {
  curl -s -X POST "$API_URL/yorkie.v1.AdminService/GetDocument" \
    -H "Content-Type: application/json" \
    -H "authorization: API-Key $SECRET_KEY" \
    -d "{\"document_key\": \"$1\"}"
}

# Update document
update_document() {
  curl -s -X POST "$API_URL/yorkie.v1.AdminService/UpdateDocument" \
    -H "Content-Type: application/json" \
    -H "authorization: API-Key $SECRET_KEY" \
    -d "{\"document_key\": \"$1\", \"root\": $2}"
}

# List documents
list_documents() {
  curl -s -X POST "$API_URL/yorkie.v1.AdminService/ListDocuments" \
    -H "Content-Type: application/json" \
    -H "authorization: API-Key $SECRET_KEY" \
    -d '{"page_size": 100, "include_root": true}'
}

# Example: Get a document
# get_document "your-doc-key"

# Example: Update a document
# update_document "your-doc-key" '"{\"field\": \"value\"}"'

echo "Migration script ready. Edit this script to add your transformations."
`, project.Name)
}
