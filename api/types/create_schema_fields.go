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
 *
 */

package types

import (
	"fmt"
	"os"

	"github.com/yorkie-team/yorkie/internal/validation"
)

var (
	// reservedSchemaNames is a map of reserved schema names.
	reservedSchemaNames = map[string]bool{"new": true}
)

// CreateSchemaFields is a set of fields that use to create a schema.
type CreateSchemaFields struct {
	// Name is the name of this schema.
	Name *string `bson:"name,omitempty" validate:"required,min=2,max=30,slug,reserved_schema_name"`
}

// Validate validates the CreateSchemaFields.
func (i *CreateSchemaFields) Validate() error {
	return validation.ValidateStruct(i)
}

func init() {
	if err := validation.RegisterValidation(
		"reserved_schema_name",
		func(level validation.FieldLevel) bool {
			_, ok := reservedSchemaNames[level.Field().String()]
			return !ok
		},
	); err != nil {
		fmt.Fprintln(os.Stderr, "create schema fields: ", err)
		os.Exit(1)
	}

	if err := validation.RegisterTranslation("reserved_schema_name", "given {0} is reserved name"); err != nil {
		fmt.Fprintln(os.Stderr, "create schema fields: ", err)
		os.Exit(1)
	}
}
