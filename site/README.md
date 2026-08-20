# ryla-site

A [Ryla](https://github.com/Dshonored/ryla) application.

## Getting started

```bash
ry migrate   # create the database schema
ry dev       # start the dev server with live reload
```

The app listens on http://localhost:8080.

## Commands

| Command | What it does |
| --- | --- |
| `ry dev` | Watch, rebuild, restart, and reload the browser |
| `ry build` | Compile a single static binary |
| `ry start` | Run the compiled binary |
| `ry migrate` | Apply pending migrations |
| `ry migrate:rollback` | Undo the last batch of migrations |
| `ry migrate:status` | Show which migrations have run |
| `ry migrate:refresh` | Roll everything back, then migrate again |
| `ry make:controller Posts` | Generate a controller |
| `ry make:auth` | Registration, sign-in, sign-out, email verification, password reset |
| `ry make:2fa` | TOTP two-factor: enrolment, challenge and recovery codes |
| `ry make:job SendEmail` | Generate a background job |
| `ry make:middleware EnsureAdmin` | Generate an HTTP middleware |
| `ry make:seeder Users` | Generate a database seeder |
| `ry db:seed` | Run the database seeders |
| `ry queue:failed` | List jobs that ran out of attempts |
| `ry queue:work` | Run background jobs |
| `ry schedule:run` | Run recurring tasks |
| `ry make:request CreatePost` | Generate a validated request type |
| `ry key:generate` | Generate the key that signs cookies |
| `ry make:model Post` | Generate a model and its migration |
| `ry routes` | List named routes |
| `ry make:test Posts` | Generate a feature test |

## The database

This project runs on **sqlite**. `DB_DSN` in `.env` points at it, and
`database/driver.go` selects the dialect. Schema changes are migrations:

```bash
ry make:migration add_slug_to_posts
ry migrate
```

A migration carries its own snapshot of the tables it touches rather than
referencing a model, so it keeps doing what it did the day it was written. Never
edit one that has already run anywhere — add another.

## Authentication

```bash
ry make:auth   # then add RegisterAuth(a) to routes/web.go, and ry migrate
ry make:2fa    # TOTP on top, once make:auth has been run
```

`ry make:auth` writes a User model, registration, email verification, sign-in,
sign-out and password reset. `ry make:2fa` adds an enrolment page with a QR
code, a challenge that holds a signed-in session until it is answered, and
single-use recovery codes shown once at enrolment.

Everything they write is ordinary code you own and can edit. Neither touches
your existing files: the lines to add to `routes/web.go` and
`app/models/user.go` are printed for you to paste in.

## Tests

```bash
go test ./...
```

`tests/example_test.go` builds the whole application against a throwaway
in-memory database with the migrations applied, then makes requests through the
real router and middleware stack:

```go
client(t).Get("/").AssertOK().AssertContains("Welcome")
```

The client keeps cookies between requests, carries the CSRF token on every form
submission, and `LoginID(user.ID)` signs it in without going through the login
form. Copy the file for your own tests, or generate one with `ry make:test Posts`.

## The welcome page

The page at `/` is a demo: it exercises a route, a controller, a JSON endpoint
and a compiled view, and tells you which files to edit. Delete it when you have
your own — `app/controllers/demo.go`, the `/demo/counter` routes, and
`resources/static/demo.js`.

`resources/static/app.css` is a design system rather than demo styling: tokens
at the top, then base elements, then components. Change the tokens and the whole
application restyles.

## Layout

```
app/            controllers, models, middleware — your code
app/requests/   what a form may contain, and what counts as valid
config/         typed configuration, read from the environment
database/       migrations and seeders
resources/      templ views and static assets
routes/         the route table
tests/          feature tests through the real router
storage/        database file, logs, build scratch
cmd/app/        the binary's entrypoint
```

## Working with coding agents

[AGENTS.md](AGENTS.md) describes the layout, the conventions and which files are
generated, so Claude Code, Cursor and similar tools do not hand-edit `*_templ.go`
or rewrite `routes/web.go`. `CLAUDE.md` points at the same file.

## Deploying

`ry build` produces one static binary with the views and static assets compiled
in. Copy it and the `.env` file to the server; nothing else is required — no Go
toolchain, no `ry`, and with SQLite no database server either.

```bash
./ryla-site migrate
./ryla-site serve
```
