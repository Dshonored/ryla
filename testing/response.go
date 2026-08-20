package testing

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
)

// bodyExcerpt bounds how much of a response a failure message prints.
//
// Enough to see the error page's message and the start of the markup, short of
// pasting a whole rendered page into the test output — at which point nobody
// reads any of it.
const bodyExcerpt = 1500

// Response is one recorded response.
//
// The assertions all report through the test rather than returning an error,
// and every one of them prints the request and the response body when it fails.
// A bare "status = 500, want 200" sends you to the logs; the body usually says
// what happened on the line that failed.
type Response struct {
	t   TB
	rec *httptest.ResponseRecorder

	method string
	path   string
}

// Status is the response status code.
func (r *Response) Status() int { return r.rec.Code }

// Body is the response body as a string.
func (r *Response) Body() string { return r.rec.Body.String() }

// Header returns the response headers.
func (r *Response) Header() http.Header { return r.rec.Header() }

// Result returns the underlying *http.Response, for anything the assertions
// below do not cover.
func (r *Response) Result() *http.Response { return r.rec.Result() }

// AssertStatus fails the test unless the status matches.
func (r *Response) AssertStatus(want int) *Response {
	r.t.Helper()

	if r.rec.Code != want {
		r.fail("status = %d, want %d", r.rec.Code, want)
	}
	return r
}

// AssertOK fails the test unless the response is 200.
func (r *Response) AssertOK() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusOK)
}

// AssertRedirect fails the test unless the response redirects to location.
//
// It accepts any 3xx rather than one specific code: whether a handler answers
// 302 or 303 is rarely what a test means to pin, and where it sends the visitor
// always is.
func (r *Response) AssertRedirect(location string) *Response {
	r.t.Helper()

	if r.rec.Code < 300 || r.rec.Code >= 400 {
		r.fail("status = %d, want a redirect to %s", r.rec.Code, location)
		return r
	}
	if got := r.rec.Header().Get("Location"); got != location {
		r.fail("Location = %q, want %q", got, location)
	}
	return r
}

// AssertContains fails the test unless every one of wants appears in the body.
func (r *Response) AssertContains(wants ...string) *Response {
	r.t.Helper()

	body := r.Body()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			r.fail("the body does not contain %q", want)
		}
	}
	return r
}

// AssertNotContains fails the test if any of wants appears in the body. This is
// how a test proves something was hidden rather than merely that the page
// rendered.
func (r *Response) AssertNotContains(wants ...string) *Response {
	r.t.Helper()

	body := r.Body()
	for _, want := range wants {
		if strings.Contains(body, want) {
			r.fail("the body contains %q, which it should not", want)
		}
	}
	return r
}

// AssertHeader fails the test unless a response header has the expected value.
func (r *Response) AssertHeader(name, want string) *Response {
	r.t.Helper()

	if got := r.rec.Header().Get(name); got != want {
		r.fail("header %s = %q, want %q", name, got, want)
	}
	return r
}

// AssertJSON decodes the body into target, failing the test if it is not JSON.
//
//	var body struct{ Status string }
//	client.Get("/up").AssertOK().AssertJSON(&body)
func (r *Response) AssertJSON(target any) *Response {
	r.t.Helper()

	if err := json.Unmarshal(r.rec.Body.Bytes(), target); err != nil {
		r.fail("the body is not the JSON expected: %v", err)
	}
	return r
}

// fail reports a failed assertion along with what was asked for and what came
// back.
func (r *Response) fail(format string, args ...any) {
	r.t.Helper()

	r.t.Errorf("%s %s: %s\n\nbody:\n%s",
		r.method, r.path, fmt.Sprintf(format, args...), excerpt(r.Body()))
}

func excerpt(body string) string {
	if body == "" {
		return "(empty)"
	}
	if len(body) <= bodyExcerpt {
		return body
	}
	return body[:bodyExcerpt] + "\n... (truncated)"
}
