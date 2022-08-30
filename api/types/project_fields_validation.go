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

var (
	// reservedNames is a map of reserved names. It is used to check if the
	// given project name is reserved or not.
	reservedNames = map[string]bool{"new": true, "default": true}

	// NOTE(DongjinS): regular expression is referenced unreserved characters
	// (https://datatracker.ietf.org/doc/html/rfc3986#section-2.3)
	// and copied from https://gist.github.com/dpk/4757681
	nameRegex = regexp.MustCompile(`^[a-z0-9\-._~]+$`)
)

func isReservedName(name string) bool {
	if _, ok := reservedNames[name]; ok {
		return true
	}
	return false
}

func init() {
	registerValidation("slug", func(level validator.FieldLevel) bool {
		name := level.Field().String()
		return nameRegex.MatchString(name)
	})
	registerTranslation("slug", "{0} must only contain slug available characters")

	registerValidation("reservedname", func(level validator.FieldLevel) bool {
		name := level.Field().String()
		return !isReservedName(name)
	})
	registerTranslation("reservedname", "given {0} is reserved name")

	registerValidation("invalidmethod", func(level validator.FieldLevel) bool {
		methods := level.Field().Interface().([]string)
		for _, method := range methods {
			if !IsAuthMethod(method) {
				return false
			}
		}
		return true
	})
	registerTranslation("invalidmethod", "given {0} is invalid method")

	registerValidation("emptystring", func(level validator.FieldLevel) bool {
		url := level.Field().String()
		return url == ""
	})
	registerTranslation("url|emptystring", "{0} must be a valid URL")
}
