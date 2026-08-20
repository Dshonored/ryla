package views

// The terminal and code panels on the landing page.
//
// These live here as HTML rather than in the .templ file because templ reads
// `{` as the start of a Go expression, and the whole point of a Go code sample
// is that it is full of braces. They are rendered with templ.Raw, which is safe
// precisely because this is authored markup with nothing interpolated into it —
// no request data reaches these strings.
const (
	// TermNew is the `ry new` session in the "one command" section.
	TermNew = `<span class="c-dim">$</span> <span class="c-fg">ry new myapp</span>

<span class="c-mut">Database</span>
<span class="c-acc">&rsaquo;</span> <span class="c-fg">sqlite</span> <span class="c-dim">— one file, no server</span>
  <span class="c-dim">postgres — a real server, transactional DDL</span>

<span class="c-mut">Web mode</span>
<span class="c-acc">&rsaquo;</span> <span class="c-fg">react</span> <span class="c-dim">— Vite, embedded in the binary</span>

<span class="c-ok">Created myapp (45 files)</span>
<span class="c-mut">Resolving dependencies…</span>

<span class="c-dim">$</span> <span class="c-fg">ry dev</span>
<span class="c-mut">watching</span> <span class="c-dim">~/myapp</span> <span class="c-mut">· vite on :5173</span>
<span class="c-fg">→ http://localhost:8080</span><span class="caret"></span>`

	// CodePosts is the controller shown under "no magic". It is the shape
	// `ry make:controller` actually emits.
	CodePosts = `<span class="c-dim">// Posts handles the blog.</span>
<span class="c-key">type</span> Posts <span class="c-key">struct</span> {
	app <span class="c-typ">*app.App</span>
}

<span class="c-key">func</span> <span class="c-fn">NewPosts</span>(a <span class="c-typ">*app.App</span>) <span class="c-typ">*Posts</span> {
	<span class="c-key">return</span> &amp;Posts{app: a}
}

<span class="c-key">func</span> (c <span class="c-typ">*Posts</span>) <span class="c-fn">Index</span>(w http.ResponseWriter, r <span class="c-typ">*http.Request</span>) {
	<span class="c-key">var</span> posts []models.Post
	c.app.DB.<span class="c-fn">Find</span>(&amp;posts)
	<span class="c-dim">// …</span>
}`

	// TermBuild is the deploy section's build and ship.
	TermBuild = `<span class="c-dim">$</span> <span class="c-fg">ry build</span>
<span class="c-mut">Checking types… </span><span class="c-ok">0 errors</span>
<span class="c-mut">vite build → dist/ </span><span class="c-dim">198 kB</span>
<span class="c-fg">Built myapp </span><span class="c-dim">(21.8 MB)</span>

<span class="c-dim">$</span> <span class="c-fg">scp myapp server:/srv/ &amp;&amp; ssh server /srv/myapp serve</span>`
)
