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
	"net/http"
	"strings"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/internal/version"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/projects"
)

const (
	// MCPProtocolVersion is the version of the MCP protocol supported.
	MCPProtocolVersion = "2024-11-05"

	// AuthorizationHeader is the header key for authorization.
	AuthorizationHeader = "authorization"

	// APIKeyScheme is the scheme for API key authentication.
	APIKeyScheme = "API-Key"
)

// Handler handles MCP protocol requests.
type Handler struct {
	backend *backend.Backend
	tools   map[string]*ToolDefinition
}

// ToolDefinition defines a tool with its handler function.
type ToolDefinition struct {
	Tool    Tool
	Handler func(ctx context.Context, project *types.Project, params json.RawMessage) (any, error)
}

// NewHandler creates a new MCP handler.
func NewHandler(be *backend.Backend) (string, http.Handler) {
	h := &Handler{
		backend: be,
		tools:   make(map[string]*ToolDefinition),
	}

	// Register tools
	h.registerTools()

	return "/mcp/", h
}

// ServeHTTP handles HTTP requests for the MCP endpoint.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Set CORS and content type headers
	w.Header().Set("Content-Type", "application/json")

	// Only accept POST requests
	if r.Method != http.MethodPost {
		h.writeError(w, nil, ErrCodeInvalidRequest, "Method not allowed")
		return
	}

	// Authenticate the request
	project, err := h.authenticate(r)
	if err != nil {
		h.writeError(w, nil, ErrCodeUnauthorized, "Unauthorized: "+err.Error())
		return
	}

	// Parse the JSON-RPC request
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, nil, ErrCodeParseError, "Parse error: "+err.Error())
		return
	}

	// Validate JSON-RPC version
	if req.JSONRPC != "2.0" {
		h.writeError(w, req.ID, ErrCodeInvalidRequest, "Invalid JSON-RPC version")
		return
	}

	// Create context with project
	ctx := projects.With(r.Context(), project)

	// Route to the appropriate handler
	switch req.Method {
	case "initialize":
		h.handleInitialize(w, req)
	case "initialized":
		h.handleInitialized(w, req)
	case "tools/list":
		h.handleToolsList(w, req)
	case "tools/call":
		h.handleToolsCall(ctx, w, req, project)
	case "resources/list":
		h.handleResourcesList(w, req)
	case "resources/read":
		h.handleResourcesRead(ctx, w, req, project)
	default:
		h.writeError(w, req.ID, ErrCodeMethodNotFound, "Method not found: "+req.Method)
	}
}

// authenticate extracts and validates the API key from the request.
func (h *Handler) authenticate(r *http.Request) (*types.Project, error) {
	authHeader := r.Header.Get(AuthorizationHeader)
	if authHeader == "" {
		// Try lowercase
		authHeader = r.Header.Get(strings.ToLower(AuthorizationHeader))
	}

	if authHeader == "" {
		return nil, ErrMissingAPIKey
	}

	// Parse "API-Key <key>" format
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != APIKeyScheme {
		return nil, ErrInvalidAuthFormat
	}

	secretKey := parts[1]
	return projects.ProjectFromSecretKey(r.Context(), h.backend, secretKey)
}

// handleInitialize handles the initialize method.
func (h *Handler) handleInitialize(w http.ResponseWriter, req Request) {
	result := InitializeResult{
		ProtocolVersion: MCPProtocolVersion,
		ServerInfo: ServerInfo{
			Name:    "yorkie-mcp",
			Version: version.Version,
		},
		Capabilities: Capabilities{
			Tools: &ToolsCapability{
				ListChanged: false,
			},
			Resources: &ResourcesCapability{
				Subscribe:   false,
				ListChanged: false,
			},
		},
	}

	h.writeResult(w, req.ID, result)
}

// handleInitialized handles the initialized notification.
func (h *Handler) handleInitialized(w http.ResponseWriter, req Request) {
	// This is a notification, no response needed
	h.writeResult(w, req.ID, nil)
}

// handleToolsList handles the tools/list method.
func (h *Handler) handleToolsList(w http.ResponseWriter, req Request) {
	tools := make([]Tool, 0, len(h.tools))
	for _, def := range h.tools {
		tools = append(tools, def.Tool)
	}

	h.writeResult(w, req.ID, ToolsListResult{Tools: tools})
}

// handleToolsCall handles the tools/call method.
func (h *Handler) handleToolsCall(ctx context.Context, w http.ResponseWriter, req Request, project *types.Project) {
	var params ToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		h.writeError(w, req.ID, ErrCodeInvalidParams, "Invalid params: "+err.Error())
		return
	}

	toolDef, ok := h.tools[params.Name]
	if !ok {
		h.writeError(w, req.ID, ErrCodeMethodNotFound, "Tool not found: "+params.Name)
		return
	}

	// Call the tool handler
	result, err := toolDef.Handler(ctx, project, params.Arguments)
	if err != nil {
		logging.DefaultLogger().Warnf("MCP tool %s error: %v", params.Name, err)
		h.writeResult(w, req.ID, ToolCallResult{
			Content: []ContentBlock{{Type: "text", Text: "Error: " + err.Error()}},
			IsError: true,
		})
		return
	}

	// Convert result to JSON string
	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		h.writeError(w, req.ID, ErrCodeInternalError, "Failed to marshal result")
		return
	}

	h.writeResult(w, req.ID, ToolCallResult{
		Content: []ContentBlock{{Type: "text", Text: string(resultJSON)}},
	})
}

// handleResourcesList handles the resources/list method.
func (h *Handler) handleResourcesList(w http.ResponseWriter, req Request) {
	// For now, we don't expose resources directly
	// Documents are accessed via tools
	h.writeResult(w, req.ID, ResourcesListResult{Resources: []Resource{}})
}

// handleResourcesRead handles the resources/read method.
func (h *Handler) handleResourcesRead(ctx context.Context, w http.ResponseWriter, req Request, project *types.Project) {
	var params ResourceReadParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		h.writeError(w, req.ID, ErrCodeInvalidParams, "Invalid params: "+err.Error())
		return
	}

	// For now, resources are not implemented
	h.writeError(w, req.ID, ErrCodeNotFound, "Resource not found: "+params.URI)
}

// writeResult writes a successful JSON-RPC response.
func (h *Handler) writeResult(w http.ResponseWriter, id any, result any) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logging.DefaultLogger().Errorf("MCP: failed to write response: %v", err)
	}
}

// writeError writes an error JSON-RPC response.
func (h *Handler) writeError(w http.ResponseWriter, id any, code int, message string) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &Error{
			Code:    code,
			Message: message,
		},
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logging.DefaultLogger().Errorf("MCP: failed to write error response: %v", err)
	}
}
