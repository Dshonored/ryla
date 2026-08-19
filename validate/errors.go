package validate

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/go-playground/validator/v10"
)

// ErrorBag holds validation messages keyed by field name.
//
// It is deliberately a plain map: templates read it directly, and a handler
// that wants to add its own error — "that email is already taken", which no tag
// can know — just calls Add.
type ErrorBag map[string][]string

// Add appends a message for a field.
func (b ErrorBag) Add(field, message string) {
	b[field] = append(b[field], message)
}

// Any reports whether the bag holds any message at all. Templates use it to
// decide whether to render a summary.
func (b ErrorBag) Any() bool { return len(b) > 0 }

// Has reports whether a specific field failed.
func (b ErrorBag) Has(field string) bool { return len(b[field]) > 0 }

// First returns the first message for a field, or "". A form shows one message
// per input; the rest are noise.
func (b ErrorBag) First(field string) string {
	if len(b[field]) == 0 {
		return ""
	}
	return b[field][0]
}

// All returns every message, sorted by field so the order is stable between
// renders rather than following Go's random map iteration.
func (b ErrorBag) All() []string {
	fields := make([]string, 0, len(b))
	for field := range b {
		fields = append(fields, field)
	}
	sort.Strings(fields)

	var out []string
	for _, field := range fields {
		out = append(out, b[field]...)
	}
	return out
}

// Fields returns the failing field names, sorted.
func (b ErrorBag) Fields() []string {
	fields := make([]string, 0, len(b))
	for field := range b {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

// Error makes ErrorBag usable as an error, for handlers that would rather
// return it than branch on Any.
func (b ErrorBag) Error() string {
	return strings.Join(b.All(), "; ")
}

// message turns a failed tag into something worth showing a person.
//
// validator's own output ("Key: 'Form.email' Error:Field validation for 'email'
// failed on the 'email' tag") is written for a developer reading a log, not for
// someone who mistyped their address.
func message(fe validator.FieldError) string {
	field := label(fe.Field())
	param := fe.Param()

	switch fe.Tag() {
	case "required", "required_with", "required_without":
		return field + " is required."
	case "email":
		return "Enter a valid email address."
	case "url":
		return "Enter a valid URL."
	case "min":
		if fe.Kind() == reflect.String {
			return fmt.Sprintf("%s must be at least %s characters.", field, param)
		}
		return fmt.Sprintf("%s must be at least %s.", field, param)
	case "max":
		if fe.Kind() == reflect.String {
			return fmt.Sprintf("%s must be %s characters or fewer.", field, param)
		}
		return fmt.Sprintf("%s must be %s or less.", field, param)
	case "len":
		return fmt.Sprintf("%s must be exactly %s characters.", field, param)
	case "eqfield":
		return fmt.Sprintf("%s must match %s.", field, label(param))
	case "nefield":
		return fmt.Sprintf("%s must be different from %s.", field, label(param))
	case "gt", "gte":
		return fmt.Sprintf("%s must be greater than %s.", field, param)
	case "lt", "lte":
		return fmt.Sprintf("%s must be less than %s.", field, param)
	case "oneof":
		return fmt.Sprintf("%s must be one of: %s.", field, strings.ReplaceAll(param, " ", ", "))
	case "alpha":
		return field + " may contain letters only."
	case "alphanum":
		return field + " may contain letters and numbers only."
	case "numeric":
		return field + " must be a number."
	case "boolean":
		return field + " must be true or false."
	case "uuid", "uuid4":
		return field + " must be a valid UUID."
	default:
		// An unmapped tag still has to say something useful rather than leaking
		// validator's internal phrasing.
		return fmt.Sprintf("%s is not valid.", field)
	}
}

// label turns a field name into something readable: "first_name" and
// "firstName" both become "First name".
func label(field string) string {
	if field == "" {
		return "This field"
	}

	spaced := strings.NewReplacer("_", " ", "-", " ").Replace(field)

	var b strings.Builder
	for i, r := range spaced {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte(' ')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}

	out := strings.TrimSpace(strings.ToLower(b.String()))
	if out == "" {
		return "This field"
	}
	return strings.ToUpper(out[:1]) + out[1:]
}
