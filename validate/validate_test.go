package validate

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type signup struct {
	Email    string `form:"email" validate:"required,email"`
	Name     string `form:"name" validate:"required,min=2,max=40"`
	Age      int    `form:"age" validate:"gte=18"`
	Newsflag bool   `form:"newsletter"`
	Tags     []string
	Website  string `form:"website" validate:"omitempty,url"`
}

func formRequest(values string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(values))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func jsonRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestBindForm(t *testing.T) {
	var form signup
	r := formRequest("email=a@b.com&name=Alice&age=30&newsletter=on&Tags=go&Tags=web")

	if err := Bind(r, &form); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	if form.Email != "a@b.com" || form.Name != "Alice" || form.Age != 30 {
		t.Errorf("scalars bound wrong: %+v", form)
	}
	// An HTML checkbox submits "on", which is not something ParseBool accepts.
	if !form.Newsflag {
		t.Error("checkbox value \"on\" did not bind to true")
	}
	if len(form.Tags) != 2 || form.Tags[0] != "go" {
		t.Errorf("repeated values did not bind to a slice: %v", form.Tags)
	}
}

func TestBindJSON(t *testing.T) {
	type payload struct {
		Email string `json:"email"`
		Age   int    `json:"age"`
	}

	var p payload
	if err := Bind(jsonRequest(`{"email":"a@b.com","age":30}`), &p); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if p.Email != "a@b.com" || p.Age != 30 {
		t.Errorf("bound wrong: %+v", p)
	}
}

func TestBindJSONRejectsGarbage(t *testing.T) {
	type payload struct {
		Email string `json:"email"`
	}

	cases := map[string]string{
		"malformed":     `{"email":`,
		"unknown field": `{"email":"a@b.com","admin":true}`,
		"trailing junk": `{"email":"a@b.com"} {"email":"c@d.com"}`,
		"wrong type":    `{"email":123}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			var p payload
			if err := Bind(jsonRequest(body), &p); err == nil {
				t.Errorf("accepted %s", body)
			}
		})
	}
}

func TestBindLeavesAbsentFieldsAlone(t *testing.T) {
	// A field the form did not submit must keep whatever the handler already
	// put there, so a partial update does not silently blank things out.
	form := signup{Name: "existing"}
	if err := Bind(formRequest("email=a@b.com"), &form); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if form.Name != "existing" {
		t.Errorf("Name = %q, want it untouched", form.Name)
	}
}

func TestBindRejectsBadTargets(t *testing.T) {
	var notPointer signup
	if err := Bind(formRequest(""), notPointer); err == nil {
		t.Error("a non-pointer target was accepted")
	}
	if err := Bind(formRequest(""), (*signup)(nil)); err == nil {
		t.Error("a nil pointer was accepted")
	}
	s := "string"
	if err := Bind(formRequest(""), &s); err == nil {
		t.Error("a pointer to a non-struct was accepted")
	}
}

func TestBindReportsUnparsableNumbers(t *testing.T) {
	var form signup
	err := Bind(formRequest("age=not-a-number"), &form)
	if err == nil {
		t.Fatal("a non-numeric age was accepted")
	}
	if !strings.Contains(err.Error(), "age") {
		t.Errorf("error should name the field, got: %v", err)
	}
}

func TestCheckProducesReadableMessages(t *testing.T) {
	bag := Check(&signup{Email: "nope", Name: "A", Age: 12})

	if !bag.Any() {
		t.Fatal("invalid struct produced no errors")
	}

	tests := map[string]string{
		"email": "Enter a valid email address.",
		"name":  "Name must be at least 2 characters.",
		"age":   "Age must be greater than 18.",
	}
	for field, want := range tests {
		if got := bag.First(field); got != want {
			t.Errorf("bag.First(%q) = %q, want %q", field, got, want)
		}
	}
}

func TestCheckKeysByFormName(t *testing.T) {
	// The bag is keyed by the form field name, not the Go field name, so a
	// template can look up errors with the same string it uses for the input's
	// name attribute.
	bag := Check(&signup{})
	if !bag.Has("email") {
		t.Errorf("expected an \"email\" key, got %v", bag.Fields())
	}
	if bag.Has("Email") {
		t.Error("bag should not be keyed by the Go field name")
	}
}

func TestValidStructProducesEmptyBag(t *testing.T) {
	bag := Check(&signup{Email: "a@b.com", Name: "Alice", Age: 30})
	if bag.Any() {
		t.Errorf("valid struct produced errors: %v", bag.All())
	}
}

func TestBindAndCheck(t *testing.T) {
	var form signup
	bag, err := BindAndCheck(formRequest("email=bad&name=Alice&age=30"), &form)
	if err != nil {
		t.Fatalf("BindAndCheck: %v", err)
	}
	if !bag.Has("email") {
		t.Errorf("expected an email error, got %v", bag.All())
	}
	// Binding still happened, so the template can re-render what was typed.
	if form.Name != "Alice" {
		t.Errorf("Name = %q, want the submitted value preserved", form.Name)
	}
}

func TestErrorBagHelpers(t *testing.T) {
	bag := ErrorBag{}
	if bag.Any() {
		t.Error("empty bag reported errors")
	}

	bag.Add("email", "That email is already taken.")
	bag.Add("email", "second")
	bag.Add("name", "Name is required.")

	if !bag.Any() || !bag.Has("email") {
		t.Error("bag did not record the errors")
	}
	if got := bag.First("email"); got != "That email is already taken." {
		t.Errorf("First = %q", got)
	}
	if got := bag.First("absent"); got != "" {
		t.Errorf("First on an absent field = %q, want empty", got)
	}
	// Sorted by field, so a form renders identically on every request rather
	// than following Go's random map order.
	if got := strings.Join(bag.All(), "|"); got != "That email is already taken.|second|Name is required." {
		t.Errorf("All() = %q", got)
	}
}

func TestLabel(t *testing.T) {
	tests := map[string]string{
		"email":      "Email",
		"first_name": "First name",
		"firstName":  "First name",
		"Age":        "Age",
		"":           "This field",
	}
	for in, want := range tests {
		if got := label(in); got != want {
			t.Errorf("label(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBodySizeIsCapped(t *testing.T) {
	var form signup
	huge := strings.Repeat("x", MaxBodySize+1)

	err := Bind(formRequest("name="+huge), &form)
	if err == nil {
		t.Fatal("an oversized body was accepted")
	}
}
