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
	"github.com/go-playground/validator/v10"
)

// CreateProjectFields is a set of fields that use to create a project.
type CreateProjectFields struct {
	// Name is the name of this project.
	Name *string `bson:"name,omitempty" validate:"required,min=2,max=30,slug,reservedname"`
}

// Validate validates the CreateProjectFields.
func (i *CreateProjectFields) Validate() error {
	if err := defaultValidator.Struct(i); err != nil {
		invalidFieldsError := &InvalidFieldsError{}
		for _, err := range err.(validator.ValidationErrors) {
			v := &FieldViolation{
				Field:       err.StructField(),
				Description: err.Translate(trans),
			}
			invalidFieldsError.Violations = append(invalidFieldsError.Violations, v)
		}
		return invalidFieldsError
	}

	return nil
}
