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
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/go-playground/locales/en"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	entranslations "github.com/go-playground/validator/v10/translations/en"
)

const (
	// NOTE(DongjinS): regular expression is referenced unreserved characters
	// (https://datatracker.ietf.org/doc/html/rfc3986#section-2.3)
	// and copied from https://gist.github.com/dpk/4757681
	slugRegexString                   = `^[a-z0-9\-._~]+$`
	caseSensitiveSlugRegexString      = `^[a-zA-Z0-9\-._~]+$`
	containAlphaRegexString           = `[a-zA-Z]`
	containNumberRegexString          = `[0-9]`
	containSpecialCharRegexString     = `[\{\}\[\]\/?.,;:|\)*~!^\-_+<>@\#$%&\\\=\(\'\"\x60]`
	alphaNumberSpecialCharRegexString = `^[a-zA-Z0-9\{\}\[\]\/?.,;:|\)*~!^\-_+<>@\#$%&\\\=\(\'\"\x60]+$`
	timeDurationFormatRegexString     = `^(\d{1,2}h\s?)?(\d{1,2}m\s?)?(\d{1,2}s)?$`
)

var (
	slugRegex                   = regexp.MustCompile(slugRegexString)
	caseSensitiveSlugRegex      = regexp.MustCompile(caseSensitiveSlugRegexString)
	containAlphaRegex           = regexp.MustCompile(containAlphaRegexString)
	containNumberRegex          = regexp.MustCompile(containNumberRegexString)
	containSpecialCharRegex     = regexp.MustCompile(containSpecialCharRegexString)
	alphaNumberSpecialCharRegex = regexp.MustCompile(alphaNumberSpecialCharRegexString)
	timeDurationFormatRegex     = regexp.MustCompile(timeDurationFormatRegexString)
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

func hasAlphaNumSpecial(str string) bool {
	isValid := true
	// NOTE(chacha912): Re2 in Go doesn't support lookahead assertion(?!re)
	// so iterate over the string to check if the regular expression is matching.
	// https://github.com/golang/go/issues/18868
	testRegexes := []*regexp.Regexp{
		alphaNumberSpecialCharRegex,
		containAlphaRegex,
		containNumberRegex,
		containSpecialCharRegex}

	for _, regex := range testRegexes {
		t := regex.MatchString(str)
		if !t {
			isValid = false
			break
		}
	}
	return isValid
}

func isValidTimeDurationStringFormat(str string) bool {
	return timeDurationFormatRegex.MatchString(str)
}

// CustomRuleFunc custom rule check function.
type CustomRuleFunc = validator.Func

// FieldLevel is the field level interface.
type FieldLevel = validator.FieldLevel

// CustomRule is the custom rule struct.
type CustomRule struct {
	Tag  string
	Func CustomRuleFunc
	Err  error
}

// Violation is the error returned by the validation.
type Violation struct {
	Tag         string
	Field       string
	Err         error
	Description string
}

// Error returns the error message.
func (e Violation) Error() string {
	return e.Err.Error()
}

// StructError is the error returned by the validation of struct.
type StructError struct {
	Violations []Violation
}

// Error returns the error message.
func (s StructError) Error() string {
	sb := strings.Builder{}

	for _, v := range s.Violations {
		sb.WriteString(v.Error())
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String())
}

// RegisterValidation is shortcut of defaultValidator.RegisterValidation
// that register custom validation with given tag, and it can be used in init.
func RegisterValidation(tag string, fn validator.Func) error {
	if err := defaultValidator.RegisterValidation(tag, fn); err != nil {
		return fmt.Errorf("register validation: %w", err)
	}
	return nil
}

// RegisterTranslation is shortcut of defaultValidator.RegisterTranslation
// that registers translations against the provided tag with given msg.
func RegisterTranslation(tag, msg string) error {
	if err := defaultValidator.RegisterTranslation(
		tag,
		trans,
		func(ut ut.Translator) error {
			if err := ut.Add(tag, msg, true); err != nil {
				return fmt.Errorf("register translation: %w", err)
			}
			return nil
		},
		func(ut ut.Translator, fe validator.FieldError) string {
			t, _ := ut.T(tag, fe.Field())
			return t
		},
	); err != nil {
		return fmt.Errorf("register translation: %w", err)
	}
	return nil
}

// ValidateValue validates the value with the tag
func ValidateValue(v interface{}, tag string) error {
	if err := defaultValidator.Var(v, tag); err != nil {
		for _, e := range err.(validator.ValidationErrors) {
			return Violation{
				Tag:         e.Tag(),
				Err:         e,
				Description: e.Translate(trans),
			}
		}
	}
	return nil
}

// Validate validates the given string with tag.
func Validate(v string, tagOrRules []interface{}) error {
	sb := strings.Builder{}

	for i, tagOrRule := range tagOrRules {
		if i != 0 {
			sb.WriteString(",")
		}

		switch value := tagOrRule.(type) {
		case string:
			sb.WriteString(value)
		case CustomRule:
			var tag = fmt.Sprintf("custom_key_%d", i)
			if value.Tag != "" {
				tag = value.Tag
			}

			if value.Func != nil {
				if err := RegisterValidation(tag, value.Func); err != nil {
					fmt.Fprintln(os.Stderr, "validation custom rule: %w", err)
					os.Exit(1)
				}
			}

			if value.Err != nil {
				if err := RegisterTranslation(tag, value.Err.Error()); err != nil {
					fmt.Fprintln(os.Stderr, "validation custom rule: %w", err)
					os.Exit(1)
				}
			}

			sb.WriteString(tag)
		}
	}

	return ValidateValue(v, sb.String())
}

// ValidateStruct validates the struct
func ValidateStruct(s interface{}) error {
	if err := defaultValidator.Struct(s); err != nil {
		structError := &StructError{}
		for _, e := range err.(validator.ValidationErrors) {
			structError.Violations = append(structError.Violations, Violation{
				Tag:         e.Tag(),
				Field:       e.StructField(),
				Err:         e,
				Description: e.Translate(trans),
			})
		}
		return structError
	}

	return nil
}

func init() {
	if err := entranslations.RegisterDefaultTranslations(defaultValidator, trans); err != nil {
		fmt.Fprintln(os.Stderr, "validation register default translations: %w", err)
		os.Exit(1)
	}

	if err := RegisterValidation("slug", func(level validator.FieldLevel) bool {
		val := level.Field().String()
		return slugRegex.MatchString(val)
	}); err != nil {
		fmt.Fprintln(os.Stderr, "validation slug: %w", err)
		os.Exit(1)
	}
	if err := RegisterTranslation(
		"slug",
		"{0} must only contain letters, numbers, hyphen, period, underscore, and tilde",
	); err != nil {
		fmt.Fprintln(os.Stderr, "validation slug: %w", err)
		os.Exit(1)
	}

	if err := RegisterValidation("case_sensitive_slug", func(level validator.FieldLevel) bool {
		val := level.Field().String()
		return caseSensitiveSlugRegex.MatchString(val)
	}); err != nil {
		fmt.Fprintln(os.Stderr, "validation case_sensitive_slug: %w", err)
		os.Exit(1)
	}
	if err := RegisterTranslation(
		"case_sensitive_slug",
		"{0} must only contain case-sensitive letters, numbers, hyphen, period, underscore, and tilde",
	); err != nil {
		fmt.Fprintln(os.Stderr, "validation case_sensitive_slug: %w", err)
		os.Exit(1)
	}

	if err := RegisterValidation("alpha_num_special", func(level validator.FieldLevel) bool {
		val := level.Field().String()
		return hasAlphaNumSpecial(val)
	}); err != nil {
		fmt.Fprintln(os.Stderr, "validation alpha_num_special: %w", err)
		os.Exit(1)
	}
	if err := RegisterTranslation(
		"alpha_num_special",
		"{0} must include letters, numbers, and special characters",
	); err != nil {
		fmt.Fprintln(os.Stderr, "validation alpha_num_special: %w", err)
		os.Exit(1)
	}

	if err := RegisterValidation("emptystring", func(level validator.FieldLevel) bool {
		val := level.Field().String()
		return val == ""
	}); err != nil {
		fmt.Fprintln(os.Stderr, "validation emptystring: %w", err)
		os.Exit(1)
	}
	if err := RegisterTranslation("url|emptystring", "{0} must be a valid URL"); err != nil {
		fmt.Fprintln(os.Stderr, "validation emptystring: %w", err)
		os.Exit(1)
	}

	if err := RegisterValidation("duration", func(level validator.FieldLevel) bool {
		val := level.Field().String()
		return isValidTimeDurationStringFormat(val)
	}); err != nil {
		fmt.Fprintln(os.Stderr, "validation duration: %w", err)
		os.Exit(1)
	}
	if err := RegisterTranslation("duration", "{0} must be a valid time duration string format"); err != nil {
		fmt.Fprintln(os.Stderr, "validation duration: %w", err)
		os.Exit(1)
	}
}
