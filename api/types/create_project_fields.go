/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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
	// reservedProjectNames is a map of reserved project names.
	reservedProjectNames = map[string]bool{"new": true, "default": true}
)

// CreateProjectFields is a set of fields that use to create a project.
type CreateProjectFields struct {
	// Name is the name of this project.
	Name *string `bson:"name,omitempty" validate:"required,min=2,max=30,slug,reserved_project_name"`
}

// Validate validates the CreateProjectFields.
func (i *CreateProjectFields) Validate() error {
	return validation.ValidateStruct(i)
}

func init() {
	if err := validation.RegisterValidation(
		"reserved_project_name",
		func(level validation.FieldLevel) bool {
			_, ok := reservedProjectNames[level.Field().String()]
			return !ok
		},
	); err != nil {
		fmt.Fprintln(os.Stderr, "create project fields: ", err)
		os.Exit(1)
	}

	if err := validation.RegisterTranslation("reserved_project_name", "given {0} is reserved name"); err != nil {
		fmt.Fprintln(os.Stderr, "create project fields: ", err)
		os.Exit(1)
	}
}
