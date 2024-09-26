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
	"github.com/yorkie-team/yorkie/internal/validation"
)

// UserFields is a set of fields that use to sign up or change password to yorkie server.
type UserFields struct {
	// Username is the name of user.
	Username *string `bson:"username" validate:"required,min=2,max=30,slug"`

	// Password is the password of user.
	Password *string `bson:"password" validate:"required,min=8,max=30,alpha_num_special"`
}

// Validate validates the UserFields.
func (i *UserFields) Validate() error {
	return validation.ValidateStruct(i)
}
