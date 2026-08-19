// Package validate binds HTTP requests into structs and validates them.
//
// A request type declares what a form or JSON body is allowed to contain, and
// the compiler checks every use of it. What comes back on failure is an
// ErrorBag keyed by field name, which is what a form template needs to show the
// message next to the input that caused it.
package validate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-playground/validator/v10"
)

// MaxBodySize caps how much of a request body is read. Without a limit, a
// single request can exhaust the server's memory.
const MaxBodySize = 10 << 20 // 10 MB

// ErrBodyTooLarge is returned when a request body exceeds MaxBodySize.
var ErrBodyTooLarge = errors.New("validate: request body too large")

var (
	once     sync.Once
	instance *validator.Validate
)

// validate returns the shared validator. It is built once because registering
// tag name resolution is not cheap and the instance is safe for concurrent use.
func shared() *validator.Validate {
	once.Do(func() {
		instance = validator.New(validator.WithRequiredStructEnabled())

		// Report errors under the field's form name rather than its Go name, so
		// ErrorBag keys match the name attributes in the template.
		instance.RegisterTagNameFunc(func(f reflect.StructField) string {
			for _, tag := range []string{"form", "json"} {
				name := strings.Split(f.Tag.Get(tag), ",")[0]
				if name != "" && name != "-" {
					return name
				}
			}
			return f.Name
		})
	})
	return instance
}

// Bind fills dst from the request, choosing the decoder from the content type:
// JSON bodies are decoded as JSON, everything else is read as form values.
//
// dst must be a non-nil pointer to a struct. Fields are matched by their `form`
// tag, falling back to `json`, then to the field name.
func Bind(r *http.Request, dst any) error {
	if err := checkTarget(dst); err != nil {
		return err
	}

	contentType := r.Header.Get("Content-Type")
	if media, _, _ := strings.Cut(contentType, ";"); strings.TrimSpace(media) == "application/json" {
		return bindJSON(r, dst)
	}
	return bindForm(r, dst)
}

// Check validates an already-populated struct, returning an ErrorBag that is
// empty when everything passed.
func Check(v any) ErrorBag {
	err := shared().Struct(v)
	if err == nil {
		return ErrorBag{}
	}

	var invalid *validator.InvalidValidationError
	if errors.As(err, &invalid) {
		// Passing something that cannot be validated is a programming mistake,
		// not user input, and silently returning "valid" would hide it.
		panic(fmt.Sprintf("validate: %v", invalid))
	}

	bag := ErrorBag{}
	var fieldErrors validator.ValidationErrors
	if errors.As(err, &fieldErrors) {
		for _, fe := range fieldErrors {
			bag.Add(fe.Field(), message(fe))
		}
	}
	return bag
}

// BindAndCheck binds the request into dst and validates it in one step.
//
// The returned error covers malformed input — a body that is not valid JSON, a
// number where a number cannot be parsed — which is a different failure from a
// value the user simply needs to correct. Those come back in the ErrorBag.
func BindAndCheck(r *http.Request, dst any) (ErrorBag, error) {
	if err := Bind(r, dst); err != nil {
		return ErrorBag{}, err
	}
	return Check(dst), nil
}

func checkTarget(dst any) error {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("validate: destination must be a non-nil pointer, got %T", dst)
	}
	if rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("validate: destination must point to a struct, got %T", dst)
	}
	return nil
}

func bindJSON(r *http.Request, dst any) error {
	body := http.MaxBytesReader(nil, r.Body, MaxBodySize)

	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return ErrBodyTooLarge
		}
		return fmt.Errorf("validate: decode JSON: %w", err)
	}

	// A second value in the stream means the client sent something other than
	// the single object the handler expects.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("validate: body must contain a single JSON object")
	}
	return nil
}

func bindForm(r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, MaxBodySize)

	if err := r.ParseForm(); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return ErrBodyTooLarge
		}
		return fmt.Errorf("validate: parse form: %w", err)
	}

	rv := reflect.ValueOf(dst).Elem()
	rt := rv.Type()

	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if !field.IsExported() {
			continue
		}

		name := fieldName(field)
		if name == "-" {
			continue
		}
		if _, present := r.Form[name]; !present {
			continue
		}

		if err := setField(rv.Field(i), r.Form[name]); err != nil {
			return fmt.Errorf("validate: field %q: %w", name, err)
		}
	}
	return nil
}

func fieldName(f reflect.StructField) string {
	for _, tag := range []string{"form", "json"} {
		name := strings.Split(f.Tag.Get(tag), ",")[0]
		if name != "" {
			return name
		}
	}
	return f.Name
}

// setField converts submitted strings into the field's type. Everything from a
// form arrives as text, so this is where "42" becomes an int.
func setField(field reflect.Value, values []string) error {
	if len(values) == 0 {
		return nil
	}

	if field.Kind() == reflect.Slice && field.Type().Elem().Kind() != reflect.Uint8 {
		slice := reflect.MakeSlice(field.Type(), len(values), len(values))
		for i, v := range values {
			if err := setScalar(slice.Index(i), v); err != nil {
				return err
			}
		}
		field.Set(slice)
		return nil
	}
	return setScalar(field, values[0])
}

func setScalar(field reflect.Value, value string) error {
	if field.Kind() == reflect.Pointer {
		if value == "" {
			return nil
		}
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		field = field.Elem()
	}

	// time.Duration is an int64 underneath, but "30s" is what a form carries.
	if field.Type() == reflect.TypeOf(time.Duration(0)) {
		d, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("%q is not a duration", value)
		}
		field.SetInt(int64(d))
		return nil
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(value)
	case reflect.Bool:
		// An HTML checkbox submits "on"; nothing at all when unchecked.
		switch strings.ToLower(value) {
		case "on", "yes":
			field.SetBool(true)
		case "off", "no", "":
			field.SetBool(false)
		default:
			b, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("%q is not a boolean", value)
			}
			field.SetBool(b)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(value, 10, field.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a whole number", value)
		}
		field.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(value, 10, field.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a positive whole number", value)
		}
		field.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(value, field.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a number", value)
		}
		field.SetFloat(f)
	default:
		return fmt.Errorf("unsupported field type %s", field.Type())
	}
	return nil
}
