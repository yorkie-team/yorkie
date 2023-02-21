/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

// Package validation provides the validation functions.
package validation

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-playground/locales/en"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
)

const (
	// slugRegexString regular expression for slug validation.
	slugRegexString = `^[a-z0-9\-._~]+$`
)

var (
	slugRegex = regexp.MustCompile(slugRegexString)

	// ErrKeyInvalid is returned when the key is invalid
	ErrKeyInvalid = errors.New("key should be alphanumeric, _, ., ~, ")

	defaultValidator = validator.New()
	defaultEn        = en.New()
	uni              = ut.New(defaultEn, defaultEn)

	trans, _ = uni.GetTranslator(defaultEn.Locale()) //
)

// CustomRuleFunc custom rule check function
type CustomRuleFunc func(fl validator.FieldLevel) bool

// CustomRule is the custom rule struct
type CustomRule struct {
	Tag  string
	Func CustomRuleFunc
	Err  error
}

// ValidError is the error returned by the validation
type ValidError struct {
	Tag   string
	Field string
	Err   error
	Trans string
}

// StructError is the error list
type StructError struct {
	Violations []ValidError
}

func (s StructError) Error() string {
	return "invalid fields"
}

func (e ValidError) Error() string {
	return e.Err.Error()
}

// RegisterValidation register validation rule
func RegisterValidation(tag string, fn func(fl validator.FieldLevel) bool) {
	if err := defaultValidator.RegisterValidation(
		tag,
		fn,
	); err != nil {
		panic(err)
	}
}

// RegisterTranslation set to register validation rule translation
func RegisterTranslation(tag string, msg string) {
	if err := defaultValidator.RegisterTranslation(tag, trans, func(ut ut.Translator) error {
		if err := ut.Add(tag, msg, true); err != nil {
			return fmt.Errorf("add translation: %w", err)
		}
		return nil
	}, func(ut ut.Translator, fe validator.FieldError) string {
		t, _ := ut.T(tag, fe.Field())
		return t
	}); err != nil {
		panic(err)
	}
}

// ValidateValue validates the value with the tag
func ValidateValue(v interface{}, tag string) error {
	if err := defaultValidator.Var(v, tag); err != nil {
		for _, e := range err.(validator.ValidationErrors) {

			return ValidError{
				Tag:   e.Tag(),
				Err:   e,
				Trans: e.Translate(trans),
			}
		}
	}

	return nil
}

// ValidateDynamically validate value by dynamically rule
func ValidateDynamically(v interface{}, tag []interface{}) error {
	var tags []string
	var customTags []string

	for i, v := range tag {
		switch v.(type) {
		case string:
			tags = append(tags, fmt.Sprintf("%v", v))
		case CustomRule:

			var NewCustomRule = v.(CustomRule)
			var NewCustomRuleTag = fmt.Sprintf("custom_key_%v", i)

			if NewCustomRule.Tag != "" {
				NewCustomRuleTag = NewCustomRule.Tag
			} else {
				customTags = append(customTags, NewCustomRuleTag)
			}

			if NewCustomRule.Func != nil {
				RegisterValidation(NewCustomRuleTag, NewCustomRule.Func)
			}

			if NewCustomRule.Err != nil {
				RegisterTranslation(NewCustomRuleTag, NewCustomRule.Err.Error())
			}

			tags = append(tags, NewCustomRuleTag)
		}
	}

	err := ValidateValue(v, strings.Join(tags, ","))

	return err

}

// ValidateStruct validates the struct
func ValidateStruct(s interface{}) error {
	if err := defaultValidator.Struct(s); err != nil {
		for _, e := range err.(validator.ValidationErrors) {
			return ValidError{
				Tag:   e.Tag(),
				Field: e.StructField(),
				Err:   e,
				Trans: e.Translate(trans),
			}
		}
	}
	return nil
}

func init() {
	RegisterValidation("slug", func(v validator.FieldLevel) bool {
		if slugRegex.MatchString(v.Field().String()) == false {
			return false
		}

		return true
	})

	RegisterTranslation("slug", ErrKeyInvalid.Error())
}
