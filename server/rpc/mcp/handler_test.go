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
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMCPTypes(t *testing.T) {
	t.Run("Request marshaling", func(t *testing.T) {
		req := Request{
			JSONRPC: "2.0",
			ID:      1,
			Method:  "tools/list",
		}
		data, err := json.Marshal(req)
		assert.NoError(t, err)
		assert.Contains(t, string(data), `"jsonrpc":"2.0"`)
		assert.Contains(t, string(data), `"method":"tools/list"`)
	})

	t.Run("Response with result", func(t *testing.T) {
		resp := Response{
			JSONRPC: "2.0",
			ID:      1,
			Result:  map[string]string{"foo": "bar"},
		}
		data, err := json.Marshal(resp)
		assert.NoError(t, err)
		assert.Contains(t, string(data), `"result"`)
		assert.NotContains(t, string(data), `"error"`)
	})

	t.Run("Response with error", func(t *testing.T) {
		resp := Response{
			JSONRPC: "2.0",
			ID:      1,
			Error: &Error{
				Code:    ErrCodeMethodNotFound,
				Message: "Method not found",
			},
		}
		data, err := json.Marshal(resp)
		assert.NoError(t, err)
		assert.Contains(t, string(data), `"error"`)
		assert.Contains(t, string(data), `"code":-32601`)
	})
}

func TestMCPHandler_MethodNotAllowed(t *testing.T) {
	h := &Handler{
		tools: make(map[string]*ToolDefinition),
	}

	// Test GET request (should fail)
	req := httptest.NewRequest(http.MethodGet, "/mcp/", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	var resp Response
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, ErrCodeInvalidRequest, resp.Error.Code)
}

func TestMCPHandler_MissingAuth(t *testing.T) {
	h := &Handler{
		tools: make(map[string]*ToolDefinition),
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	var resp Response
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, ErrCodeUnauthorized, resp.Error.Code)
}

func TestMCPHandler_InvalidAuthFormat(t *testing.T) {
	h := &Handler{
		tools: make(map[string]*ToolDefinition),
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer invalid-format") // Wrong scheme
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	var resp Response
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, ErrCodeUnauthorized, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "invalid authorization format")
}

func TestToolDefinition(t *testing.T) {
	tool := Tool{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"param1": {
					Type:        "string",
					Description: "A parameter",
				},
			},
			Required: []string{"param1"},
		},
	}

	data, err := json.Marshal(tool)
	assert.NoError(t, err)
	assert.Contains(t, string(data), `"name":"test_tool"`)
	assert.Contains(t, string(data), `"inputSchema"`)
}

func TestInitializeResult(t *testing.T) {
	result := InitializeResult{
		ProtocolVersion: MCPProtocolVersion,
		ServerInfo: ServerInfo{
			Name:    "yorkie-mcp",
			Version: "1.0.0",
		},
		Capabilities: Capabilities{
			Tools: &ToolsCapability{
				ListChanged: false,
			},
		},
	}

	data, err := json.Marshal(result)
	assert.NoError(t, err)
	assert.Contains(t, string(data), `"protocolVersion"`)
	assert.Contains(t, string(data), `"serverInfo"`)
	assert.Contains(t, string(data), `"capabilities"`)
}
