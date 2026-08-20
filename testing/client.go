package testing

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/Dshonored/ryla/auth"
	"github.com/Dshonored/ryla/cookie"
	"github.com/Dshonored/ryla/csrf"
	"github.com/Dshonored/ryla/session"
)

// Config describes the application under test.
//
// Every field is taken from the application's own App struct rather than
// rebuilt here, so a test exercises the same router, the same signing key and
// the same session store the server runs with. Wiring one up by hand would test
// the wiring in the test.
type Config struct {
	// Handler is the application's router, complete with its middleware stack.
	// Required.
	Handler http.Handler

	// Jar is the application's cookie jar. Given one, the client reads the CSRF
	// token out of its signed cookie and echoes it back on every unsafe
	// request, so a test can POST to a real form route without knowing the
	// token exists. Without one, an unsafe request carries no token and the
	// middleware answers 403, exactly as it would for a form that forgot to
	// embed one.
	Jar *cookie.Jar

	// Session is the application's session store. Login writes a signed-in
	// session through it, which is what makes the resulting cookie valid for
	// the store the application will read it back from.
	Session session.Store
}

// Client drives an application's handler the way a browser would.
//
// It keeps cookies between requests, so a session survives from the request
// that created it to the ones that follow. Redirects are not followed: in a
// test the redirect itself is usually the thing worth asserting on, and
// following it silently would hide a login that bounced somewhere unexpected.
type Client struct {
	t   TB
	cfg Config

	// cookies is the browser's cookie store, keyed by name.
	cookies map[string]*http.Cookie

	// Header is sent with every request. Set entries here for a bearer token
	// or an Accept header that every request in a test needs.
	Header http.Header

	// primed records that a request has already been made for the sole purpose
	// of collecting a CSRF cookie, so a missing token cannot loop.
	primed bool
}

// New builds a client for the application described by cfg.
func New(t TB, cfg Config) *Client {
	t.Helper()

	if cfg.Handler == nil {
		t.Fatalf("testing: Config.Handler is required")
	}

	return &Client{
		t:       t,
		cfg:     cfg,
		cookies: map[string]*http.Cookie{},
		Header:  http.Header{},
	}
}

// Get issues a GET request.
func (c *Client) Get(path string) *Response {
	c.t.Helper()
	return c.Do(c.Request(http.MethodGet, path, nil))
}

// Post submits a form, urlencoded as a browser would.
func (c *Client) Post(path string, form url.Values) *Response {
	c.t.Helper()

	req := c.Request(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.Do(req)
}

// PostJSON sends a JSON body.
func (c *Client) PostJSON(path string, body any) *Response {
	c.t.Helper()
	return c.JSON(http.MethodPost, path, body)
}

// JSON sends a JSON body with an arbitrary method, for PUT, PATCH and DELETE.
func (c *Client) JSON(method, path string, body any) *Response {
	c.t.Helper()

	raw, err := json.Marshal(body)
	if err != nil {
		c.t.Fatalf("testing: encode %s %s body: %v", method, path, err)
	}

	req := c.Request(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	return c.Do(req)
}

// Delete issues a DELETE request.
func (c *Client) Delete(path string) *Response {
	c.t.Helper()
	return c.Do(c.Request(http.MethodDelete, path, nil))
}

// Request builds a request carrying the client's default headers, for a test
// that needs to adjust it before sending. Pass it to Do, which is what attaches
// the cookies.
func (c *Client) Request(method, path string, body io.Reader) *http.Request {
	c.t.Helper()

	req := httptest.NewRequest(method, path, body)
	for name, values := range c.Header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	return req
}

// Do sends a request through the application and records the response.
func (c *Client) Do(req *http.Request) *Response {
	c.t.Helper()

	// The token is resolved before the cookies are attached, because resolving
	// it may itself issue a request that sets the CSRF cookie. Attaching first
	// would send the header without the cookie it has to match, which the
	// middleware rejects — and the rejection would look like a framework bug.
	var token string
	if !safeMethod(req.Method) && req.Header.Get(csrf.HeaderName) == "" {
		token = c.csrfToken()
	}

	c.attachCookies(req)
	if token != "" {
		req.Header.Set(csrf.HeaderName, token)
	}

	rec := httptest.NewRecorder()
	c.cfg.Handler.ServeHTTP(rec, req)
	c.remember(rec.Result().Cookies())

	return &Response{t: c.t, rec: rec, method: req.Method, path: req.URL.RequestURI()}
}

// attachCookies adds the client's cookies, leaving alone any the caller set on
// the request itself so a test can override one deliberately.
func (c *Client) attachCookies(req *http.Request) {
	for name, ck := range c.cookies {
		if _, err := req.Cookie(name); err == nil {
			continue
		}
		req.AddCookie(ck)
	}
}

// Login signs the client in as the given user id, without going through a login
// form.
//
// Almost every interesting test starts from a signed-in user, and reaching that
// state through the real form would make each of those tests depend on the
// login page's markup, its field names and its rate limiter. The session is
// written through the application's own store, so what the client holds
// afterwards is a cookie the application will accept.
func (c *Client) Login(userID string) *Client {
	c.t.Helper()
	return c.loginWith(func(s *session.Session) { auth.Login(s, userID) })
}

// LoginID signs the client in as a numeric user id, which is what a GORM model
// with the default primary key has.
func (c *Client) LoginID(userID uint) *Client {
	c.t.Helper()
	return c.loginWith(func(s *session.Session) { auth.LoginID(s, userID) })
}

func (c *Client) loginWith(mark func(*session.Session)) *Client {
	c.t.Helper()

	if c.cfg.Session == nil {
		c.t.Fatalf("testing: Login needs Config.Session, the application's session store")
	}

	sess := session.New(c.cfg.Session.Lifetime())
	mark(sess)

	rec := httptest.NewRecorder()
	if err := c.cfg.Session.Save(rec, c.Request(http.MethodGet, "/", nil), sess); err != nil {
		c.t.Fatalf("testing: save the signed-in session: %v", err)
	}
	c.remember(rec.Result().Cookies())

	return c
}

// Logout drops every cookie, leaving the client anonymous.
//
// It clears the browser's side only. A test asserting that the server also
// destroyed the session should post to the application's own logout route
// instead.
func (c *Client) Logout() *Client {
	c.cookies = map[string]*http.Cookie{}
	c.primed = false
	return c
}

// Cookie returns a cookie the application has set, or nil.
func (c *Client) Cookie(name string) *http.Cookie { return c.cookies[name] }

// remember applies a response's Set-Cookie headers to the client's store.
func (c *Client) remember(cookies []*http.Cookie) {
	for _, ck := range cookies {
		// A negative MaxAge is a deletion. Keeping the cookie would make a
		// logout look like it had no effect on the next request.
		if ck.MaxAge < 0 {
			delete(c.cookies, ck.Name)
			continue
		}
		c.cookies[ck.Name] = ck
	}
}

// csrfToken returns the token to echo back on an unsafe request, priming the
// client with one request first if it has not seen the cookie yet.
//
// The value has to be unsigned before it is submitted: the cookie carries the
// token plus its signature, and the middleware compares the submitted value
// against the verified one.
func (c *Client) csrfToken() string {
	c.t.Helper()

	if c.cfg.Jar == nil {
		return ""
	}

	if _, ok := c.cookies[csrf.CookieName]; !ok {
		if c.primed {
			return ""
		}
		c.primed = true
		// Any GET will do and the response is discarded: the CSRF middleware
		// runs ahead of routing, so a token is issued even for a path with no
		// route behind it.
		c.Get("/")
	}

	ck, ok := c.cookies[csrf.CookieName]
	if !ok {
		return ""
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(ck)

	token, err := c.cfg.Jar.Get(req, csrf.CookieName)
	if err != nil {
		return ""
	}
	return token
}

func safeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}
