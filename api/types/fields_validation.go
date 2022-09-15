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
	"regexp"

	"github.com/go-playground/validator/v10"
)

const (
	// NOTE(DongjinS): regular expression is referenced unreserved characters
	// (https://datatracker.ietf.org/doc/html/rfc3986#section-2.3)
	// and copied from https://gist.github.com/dpk/4757681
	slugRegexString                   = `^[a-z0-9\-._~]+$`
	containAlphaRegexString           = `[a-zA-Z]`
	containNumberRegexString          = `[0-9]`
	containSpecialCharRegexString     = `[\{\}\[\]\/?.,;:|\)*~!^\-_+<>@\#$%&\\\=\(\'\"\x60]`
	alphaNumberSpecialCharRegexString = `^[a-zA-Z0-9\{\}\[\]\/?.,;:|\)*~!^\-_+<>@\#$%&\\\=\(\'\"\x60]+$`
)

var (
	// reservedProjectNames is a map of reserved names. It is used to check if the
	// given project name is reserved or not.
	reservedProjectNames = map[string]bool{"new": true, "default": true}

	slugRegex                   = regexp.MustCompile(slugRegexString)
	containAlphaRegex           = regexp.MustCompile(containAlphaRegexString)
	containNumberRegex          = regexp.MustCompile(containNumberRegexString)
	containSpecialCharRegex     = regexp.MustCompile(containSpecialCharRegexString)
	alphaNumberSpecialCharRegex = regexp.MustCompile(alphaNumberSpecialCharRegexString)
)

func isReservedProjectName(name string) bool {
	if _, ok := reservedProjectNames[name]; ok {
		return true
	}
	return false
}

func hasAlphaNumSpecial(str string) bool {
	isValid := true
	// NOTE(chacha912): Re2 in Go doesn't support lookahead assertion(?!re)
	// so iterate over the string to check if the regular expression is matching.
	// https://github.com/golang/go/issues/18868
	testRegexs := []*regexp.Regexp{
		alphaNumberSpecialCharRegex,
		containAlphaRegex,
		containNumberRegex,
		containSpecialCharRegex}

	for _, regex := range testRegexs {
		t := regex.MatchString(str)
		if !t {
			isValid = false
			break
		}
	}
	return isValid
}

func init() {
	registerValidation("slug", func(level validator.FieldLevel) bool {
		val := level.Field().String()
		return slugRegex.MatchString(val)
	})
	registerTranslation("slug", "{0} must only contain letters, numbers, hyphen, period, underscore, and tilde")

	registerValidation("alpha_num_special", func(level validator.FieldLevel) bool {
		val := level.Field().String()
		return hasAlphaNumSpecial(val)
	})
	registerTranslation("alpha_num_special", "{0} must include letters, numbers, and special characters")

	registerValidation("reserved_project_name", func(level validator.FieldLevel) bool {
		name := level.Field().String()
		return !isReservedProjectName(name)
	})
	registerTranslation("reserved_project_name", "given {0} is reserved name")

	registerValidation("invalid_webhook_method", func(level validator.FieldLevel) bool {
		methods := level.Field().Interface().([]string)
		for _, method := range methods {
			if !IsAuthMethod(method) {
				return false
			}
		}
		return true
	})
	registerTranslation("invalid_webhook_method", "given {0} is invalid method")

	registerValidation("emptystring", func(level validator.FieldLevel) bool {
		val := level.Field().String()
		return val == ""
	})
	registerTranslation("url|emptystring", "{0} must be a valid URL")
}
