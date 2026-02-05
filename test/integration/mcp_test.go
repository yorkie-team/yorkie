//go:build integration

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

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/test/helper"
)

// mcpRequest makes a JSON-RPC request to the MCP endpoint.
func mcpRequest(t *testing.T, serverAddr, secretKey, method string, params any) map[string]any {
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
	}
	if params != nil {
		reqBody["params"] = params
	}

	body, err := json.Marshal(reqBody)
	assert.NoError(t, err)

	url := fmt.Sprintf("http://%s/mcp/", serverAddr)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	assert.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "API-Key "+secretKey)

	resp, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	defer func() {
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)

	var result map[string]any
	err = json.Unmarshal(respBody, &result)
	assert.NoError(t, err)

	return result
}

func TestMCP(t *testing.T) {
	ctx := context.Background()

	// Create admin client and login
	adminCli, err := admin.Dial(defaultServer.RPCAddr(), admin.WithInsecure(true))
	assert.NoError(t, err)
	_, err = adminCli.LogIn(ctx, "admin", "admin")
	assert.NoError(t, err)
	defer func() {
		adminCli.Close()
	}()

	// Get project to use secret key
	project, err := adminCli.GetProject(ctx, "default")
	assert.NoError(t, err)
	secretKey := project.SecretKey

	t.Run("initialize test", func(t *testing.T) {
		resp := mcpRequest(t, defaultServer.RPCAddr(), secretKey, "initialize", nil)

		assert.Nil(t, resp["error"])
		result := resp["result"].(map[string]any)
		assert.NotEmpty(t, result["protocolVersion"])
		assert.NotEmpty(t, result["serverInfo"])
		assert.NotEmpty(t, result["capabilities"])
	})

	t.Run("tools/list test", func(t *testing.T) {
		resp := mcpRequest(t, defaultServer.RPCAddr(), secretKey, "tools/list", nil)

		assert.Nil(t, resp["error"])
		result := resp["result"].(map[string]any)
		tools := result["tools"].([]any)
		assert.NotEmpty(t, tools)

		// Check that expected tools are present
		toolNames := make([]string, 0)
		for _, tool := range tools {
			toolMap := tool.(map[string]any)
			toolNames = append(toolNames, toolMap["name"].(string))
		}
		assert.Contains(t, toolNames, "list_documents")
		assert.Contains(t, toolNames, "get_document")
		assert.Contains(t, toolNames, "check_migration_safety")
		assert.Contains(t, toolNames, "preview_transform")
		assert.Contains(t, toolNames, "infer_schema")
	})

	t.Run("list_documents tool test", func(t *testing.T) {
		resp := mcpRequest(t, defaultServer.RPCAddr(), secretKey, "tools/call", map[string]any{
			"name":      "list_documents",
			"arguments": map[string]any{"page_size": 10},
		})

		assert.Nil(t, resp["error"])
		result := resp["result"].(map[string]any)
		content := result["content"].([]any)
		assert.NotEmpty(t, content)
	})

	t.Run("get_document tool test", func(t *testing.T) {
		// First create a document via admin API
		docKey := helper.TestKey(t).String()
		initialRoot := yson.Object{
			"title":   "Test Document",
			"content": "Hello World",
		}
		_, err := adminCli.CreateDocument(ctx, "default", docKey, initialRoot)
		if err != nil {
			// Document might already exist
			t.Logf("Document creation: %v", err)
		}

		// Now get it via MCP
		resp := mcpRequest(t, defaultServer.RPCAddr(), secretKey, "tools/call", map[string]any{
			"name":      "get_document",
			"arguments": map[string]any{"document_key": docKey},
		})

		assert.Nil(t, resp["error"])
		result := resp["result"].(map[string]any)
		content := result["content"].([]any)
		assert.NotEmpty(t, content)

		// Parse the content text
		contentText := content[0].(map[string]any)["text"].(string)
		var docResult map[string]any
		err = json.Unmarshal([]byte(contentText), &docResult)
		assert.NoError(t, err)
		assert.Equal(t, docKey, docResult["key"])
	})

	t.Run("check_migration_safety tool test", func(t *testing.T) {
		// Create a document
		docKey := helper.TestKey(t).String()
		initialRoot := yson.Object{"field": "value"}
		_, err := adminCli.CreateDocument(ctx, "default", docKey, initialRoot)
		if err != nil {
			t.Logf("Document creation: %v", err)
		}

		// Check migration safety
		resp := mcpRequest(t, defaultServer.RPCAddr(), secretKey, "tools/call", map[string]any{
			"name": "check_migration_safety",
			"arguments": map[string]any{
				"document_keys": []string{docKey, "nonexistent-doc"},
			},
		})

		assert.Nil(t, resp["error"])
		result := resp["result"].(map[string]any)
		content := result["content"].([]any)
		assert.NotEmpty(t, content)

		// Parse the result
		contentText := content[0].(map[string]any)["text"].(string)
		var safetyResult map[string]any
		err = json.Unmarshal([]byte(contentText), &safetyResult)
		assert.NoError(t, err)
		assert.NotNil(t, safetyResult["safe"])
		assert.NotNil(t, safetyResult["unsafe"])
	})

	t.Run("preview_transform tool test", func(t *testing.T) {
		// Create a document
		docKey := helper.TestKey(t).String()
		initialRoot := yson.Object{
			"title":   "Original Title",
			"content": "Original Content",
		}
		_, err := adminCli.CreateDocument(ctx, "default", docKey, initialRoot)
		if err != nil {
			t.Logf("Document creation: %v", err)
		}

		// Preview a transformation
		resp := mcpRequest(t, defaultServer.RPCAddr(), secretKey, "tools/call", map[string]any{
			"name": "preview_transform",
			"arguments": map[string]any{
				"document_key": docKey,
				"operations": []map[string]any{
					{"op": "set", "path": "$.title", "value": "New Title"},
				},
			},
		})

		assert.Nil(t, resp["error"])
		result := resp["result"].(map[string]any)
		content := result["content"].([]any)
		assert.NotEmpty(t, content)

		// Parse the result
		contentText := content[0].(map[string]any)["text"].(string)
		var transformResult map[string]any
		err = json.Unmarshal([]byte(contentText), &transformResult)
		assert.NoError(t, err)
		assert.NotEmpty(t, transformResult["original"])
		assert.NotEmpty(t, transformResult["transformed"])
	})

	t.Run("generate_migration_code tool test", func(t *testing.T) {
		resp := mcpRequest(t, defaultServer.RPCAddr(), secretKey, "tools/call", map[string]any{
			"name": "generate_migration_code",
			"arguments": map[string]any{
				"operations": []map[string]any{
					{"op": "set", "path": "$.newField", "value": "newValue"},
				},
				"language": "typescript",
			},
		})

		assert.Nil(t, resp["error"])
		result := resp["result"].(map[string]any)
		content := result["content"].([]any)
		assert.NotEmpty(t, content)

		// Parse the result
		contentText := content[0].(map[string]any)["text"].(string)
		var codeResult map[string]any
		err = json.Unmarshal([]byte(contentText), &codeResult)
		assert.NoError(t, err)
		assert.Equal(t, "typescript", codeResult["language"])
		assert.Contains(t, codeResult["code"].(string), "Yorkie Migration Script")
	})

	t.Run("method not found test", func(t *testing.T) {
		resp := mcpRequest(t, defaultServer.RPCAddr(), secretKey, "unknown/method", nil)

		assert.NotNil(t, resp["error"])
		errMap := resp["error"].(map[string]any)
		assert.Equal(t, float64(-32601), errMap["code"])
	})

	t.Run("unauthorized test", func(t *testing.T) {
		resp := mcpRequest(t, defaultServer.RPCAddr(), "invalid-key", "tools/list", nil)

		assert.NotNil(t, resp["error"])
		errMap := resp["error"].(map[string]any)
		assert.Equal(t, float64(-32001), errMap["code"])
	})
}
