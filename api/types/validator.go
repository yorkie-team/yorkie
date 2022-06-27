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

var (
	defaultValidator = initValidator()
)

// initValidator creaete new instance of 'validate'
func initValidator() *validator.Validate {
	return validator.New()
}

// registerValidation is shortcut of defaultValidator.RegisterValidation
// that register custom validaiton with given tag and it can be used in init fuc
func registerValidation(tag string, fn validator.Func) {
	if err := defaultValidator.RegisterValidation(tag, fn); err != nil {
		panic(err)
	}
}

// validateStruct is shortcut of defaultValidator.Struct
func validateStruct(s interface{}) error {
	return defaultValidator.Struct(s)
}
