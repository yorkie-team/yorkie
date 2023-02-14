package validation

import (
	"errors"
	"fmt"
	"github.com/go-playground/locales/en"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	"regexp"
)

const (
	// slugRegexString regular expression for key validation
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

func registerValidation(tag string, fn func(fl validator.FieldLevel) bool) {
	if err := defaultValidator.RegisterValidation(
		tag,
		fn,
	); err != nil {
		panic(err)
	}
}

func registerTranslation(tag, msg string) {
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

func ValidateValue(v interface{}, tag string) error {
	return defaultValidator.Var(v, tag)
}

func init() {
	// register validation for key
	registerValidation("slug", func(v validator.FieldLevel) bool {
		if slugRegex.MatchString(v.Field().String()) == false {
			return false
		}

		return true
	})

	registerTranslation("slug", ErrKeyInvalid.Error())
}
