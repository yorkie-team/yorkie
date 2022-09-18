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

	"github.com/go-playground/locales/en"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	en_translations "github.com/go-playground/validator/v10/translations/en"
)

var (
	// defaultValidator is the default validation instance that is used in this
	// api package. In this package, some fields are provided by the user, and
	// we need to validate them.
	defaultValidator = validator.New()
	// defaultEn is the default translator instance for the 'en' locale.
	defaultEn = en.New()
	// uni is the UniversalTranslator instance set with
	// the fallback locale and locales it should support.
	uni = ut.New(defaultEn, defaultEn)

	// trans is the specified translator for the given locale,
	// or fallback if not found.
	trans, _ = uni.GetTranslator(defaultEn.Locale())
)

// FieldViolation is used to describe a single bad request field
type FieldViolation struct {
	// A Field of which field of the reques is bad.
	Field string
	// A description of why the request element is bad.
	Description string
}

// InvalidFieldsError is used to describe invalid fields.
type InvalidFieldsError struct {
	Violations []*FieldViolation
}

// Error returns the error message.
func (e *InvalidFieldsError) Error() string { return "invalid fields" }

// registerValidation is shortcut of defaultValidator.RegisterValidation
// that register custom validation with given tag, and it can be used in init.
func registerValidation(tag string, fn validator.Func) {
	if err := defaultValidator.RegisterValidation(tag, fn); err != nil {
		panic(err)
	}
}

// registerTranslation is shortcut of defaultValidator.RegisterTranslation
// that registers translations against the provided tag with given msg.
func registerTranslation(tag, msg string) {
	if err := defaultValidator.RegisterTranslation(tag, trans, func(ut ut.Translator) error {
		if err := ut.Add(tag, msg, true); err != nil {
			return fmt.Errorf("add normal translation: %w", err)
		}

		return nil
	}, func(ut ut.Translator, fe validator.FieldError) string {
		t, _ := ut.T(tag, fe.Field())
		return t
	}); err != nil {
		panic(err)
	}
}

func init() {
	if err := en_translations.RegisterDefaultTranslations(defaultValidator, trans); err != nil {
		panic(err)
	}
}
