/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

// Package schema provides a validator for the yorkie ruleset.
package schema

import (
	"fmt"
	"strings"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
)

// ValidationResult represents the result of validation
type ValidationResult struct {
	Valid  bool
	Errors []ValidationError
}

// ValidationError represents a validation error
type ValidationError struct {
	Path    string
	Message string
}

// ValidateYorkieRuleset validates the given data against the ruleset.
func ValidateYorkieRuleset(data any, rules []types.Rule) ValidationResult {
	var errors []ValidationError
	for _, rule := range rules {
		value := getValueByPath(data, rule.Path)
		result := validateValue(value, rule)
		if !result.Valid {
			errors = append(errors, result.Errors...)
		}
	}

	return ValidationResult{
		Valid:  len(errors) == 0,
		Errors: errors,
	}
}

// getValueByPath gets a value from the given object using the given path.
func getValueByPath(obj any, path string) any {
	if !strings.HasPrefix(path, "$") {
		panic(fmt.Sprintf("Path must start with $, got %s", path))
	}

	keys := strings.Split(path, ".")
	current := obj

	for i := 1; i < len(keys); i++ {
		key := keys[i]
		obj, ok := current.(*crdt.Object)
		if !ok {
			return nil
		}
		current = obj.Get(key)
	}

	return current
}

// validateValue validates a value against a rule.
func validateValue(value interface{}, rule types.Rule) ValidationResult {
	switch rule.Type {
	case "string", "boolean", "integer", "double", "long", "date", "bytes", "null":
		return validatePrimitiveValue(value, rule)
	case "object":
		if _, ok := value.(*crdt.Object); !ok {
			return ValidationResult{
				Valid: false,
				Errors: []ValidationError{{
					Path:    rule.Path,
					Message: fmt.Sprintf("expected object at path %s", rule.Path),
				}},
			}
		}
	case "array":
		if _, ok := value.(*crdt.Array); !ok {
			return ValidationResult{
				Valid: false,
				Errors: []ValidationError{{
					Path:    rule.Path,
					Message: fmt.Sprintf("expected array at path %s", rule.Path),
				}},
			}
		}
	case "yorkie.Text":
		if _, ok := value.(*crdt.Text); !ok {
			return ValidationResult{
				Valid: false,
				Errors: []ValidationError{{
					Path:    rule.Path,
					Message: fmt.Sprintf("expected yorkie.Text at path %s", rule.Path),
				}},
			}
		}
	case "yorkie.Tree":
		if _, ok := value.(*crdt.Tree); !ok {
			return ValidationResult{
				Valid: false,
				Errors: []ValidationError{{
					Path:    rule.Path,
					Message: fmt.Sprintf("expected yorkie.Tree at path %s", rule.Path),
				}},
			}
		}
	case "yorkie.Counter":
		if _, ok := value.(*crdt.Counter); !ok {
			return ValidationResult{
				Valid: false,
				Errors: []ValidationError{{
					Path:    rule.Path,
					Message: fmt.Sprintf("expected yorkie.Counter at path %s", rule.Path),
				}},
			}
		}
	default:
		panic(fmt.Sprintf("Unknown rule type: %s", rule.Type))
	}

	return ValidationResult{Valid: true}
}

// validatePrimitiveValue validates a primitive value against a rule.
func validatePrimitiveValue(value interface{}, rule types.Rule) ValidationResult {
	if primitive, ok := value.(*crdt.Primitive); ok {
		switch rule.Type {
		case "string":
			if primitive.ValueType() == crdt.String {
				return ValidationResult{Valid: true}
			}
		case "boolean":
			if primitive.ValueType() == crdt.Boolean {
				return ValidationResult{Valid: true}
			}
		case "integer":
			if primitive.ValueType() == crdt.Integer {
				return ValidationResult{Valid: true}
			}
		case "double":
			if primitive.ValueType() == crdt.Double {
				return ValidationResult{Valid: true}
			}
		case "long":
			if primitive.ValueType() == crdt.Long {
				return ValidationResult{Valid: true}
			}
		case "date":
			if primitive.ValueType() == crdt.Date {
				return ValidationResult{Valid: true}
			}
		case "bytes":
			if primitive.ValueType() == crdt.Bytes {
				return ValidationResult{Valid: true}
			}
		case "null":
			if primitive.ValueType() == crdt.Null {
				return ValidationResult{Valid: true}
			}
		}
	}

	return ValidationResult{
		Valid: false,
		Errors: []ValidationError{{
			Path:    rule.Path,
			Message: fmt.Sprintf("expected %s at path %s", rule.Type, rule.Path),
		}},
	}
}
