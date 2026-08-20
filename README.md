# Ryla

A batteries-included web framework for Go, with a CLI that writes the
boilerplate for you.

Laravel's productivity comes from two things: everything is already wired
together, and `artisan` generates the parts you would otherwise type by hand.
Ryla takes both, and drops the parts that only make sense in PHP — there are no
facades, no service container and no runtime resolution. `ry` generates real Go
files you can read, step through and delete.

```bash
curl -fsSL https://raw.githubusercontent.com/Dshonored/ryla/main/install.sh | sh

ry new myapp
cd myapp
ry migrate
ry dev
```

The script installs a Go toolchain if the machine has none, installs `ry`, and
adds both to your shell's PATH, on macOS and Linux. Go is not optional for a Go
framework — `ry dev` shells out to it on every save — so installing it is part
of installing Ryla rather than a prerequisite to read about first. Re-running
the script updates `ry` in place.

Already have Go, or on Windows? `go install github.com/Dshonored/ryla/cmd/ry@latest`
does the same job, and `ry update --self` keeps it current afterwards.

## The CLI

| Command | What it does |
| --- | --- |
| `ry new <name>` | Scaffold a project, interactively or from flags |
| `ry dev` | Watch, rebuild and restart on every save |
| `ry dev full` | The same, plus every development service in `compose.yaml` |
| `ry build` | Compile one static binary with views and assets embedded |
| `ry start` | Run the compiled binary |
| `ry migrate` | Apply pending migrations |
| `ry migrate:rollback` | Undo the last batch |
| `ry migrate:status` | Show what has run |
| `ry migrate:refresh` | Roll everything back, then migrate again |
| `ry make:model <Name>` | Model plus its create-table migration |
| `ry make:controller <Name>` | Controller, `--resource` for the seven CRUD handlers |
| `ry make:migration <desc>` | Empty migration |
| `ry make:middleware <Name>` | HTTP middleware |
| `ry make:request <Name>` | Validated request type |
| `ry make:seeder <Name>` | Database seeder |
| `ry make:job <Name>` | Background job |
| `ry make:test <Name>` | Feature test through the real router |
| `ry make:auth` | Registration, sign-in, sign-out, email verification, password reset |
| `ry make:2fa` | TOTP two-factor: enrolment, challenge and recovery codes |
| `ry queue:work` | Drain background jobs until interrupted |
| `ry queue:failed` / `ry queue:retry` | Inspect and requeue exhausted jobs |
| `ry schedule:run` / `ry schedule:list` | Run or list recurring tasks |
| `ry db:seed` | Run the database seeders |
| `ry db:compose` | Write a compose file with a development database |
| `ry key:generate` | Generate the key that signs cookies |
| `ry routes` | List named routes |
| `ry update` | Update the framework version, the CLI, or both |

## Databases

Pick one with `ry new --db`. The first three are GORM and share the migration
system; MongoDB is a document store and has no migrations at all.

| `--db` | Notes |
| --- | --- |
| `sqlite` | A single file, no server. Pure Go, so the binary still cross-compiles. |
| `postgres` | Transactional DDL: a failed migration leaves nothing half-built. |
| `mysql` | MariaDB too. DDL commits implicitly, so a failed migration is not rolled back. |
| `mongo` | Documents rather than rows. Indexes are declared in code; `ry migrate` applies them. |

The three that run as servers ask where that server should be, and the usual
answer writes a `compose.yaml` whose credentials already match your `.env`.
From then on `ry dev`, `ry start` and `ry migrate` start that container and wait
for its health check before connecting, so there is no first command to
remember and no connection refused to decode.

It stays out of the way when it should: no compose file, or a `DB_DSN` naming a
host that is not this machine, and nothing happens. `RYLA_NO_DOCKER=1` switches
it off entirely. `ry db:compose` adds the file later, for a project that chose
to decide later.

`ry dev full` starts the rest of the development stack with it. That file also
carries Redis and Mailpit, commented out and tagged with a `full` profile:
uncomment one and `ry dev full` runs it, while a plain `ry dev` still starts
only the database, so enabling a mail catcher never slows the rebuild loop. The
credentials already match `.env` — Redis on 6379, Mailpit's SMTP on 1025 with
its inbox on 8025 — so `CACHE_DRIVER=redis` or `MAIL_DRIVER=smtp` is the whole
of the change.

## Web modes

Pick one with `ry new --web`.

| `--web` | Notes |
| --- | --- |
| `mvc` | Server-rendered pages with templ. Compile-checked views, no JS build step. |
| `api` | JSON endpoints and no views. The errors are JSON too, 404 and 500 included. |
| `react` | A React frontend built by Vite and embedded in the binary, JSON behind it. |
| `svelte` | The same, with Svelte. |

`react` and `svelte` share one Go half: a JSON API plus a handler that proxies
to Vite while developing and serves the embedded build afterwards. `ry dev`
runs both on one address, so `fetch("/api/...")` needs no base URL and there is
no CORS to configure. Node is needed to build the frontend, never to run it.

### Language and styling

Both Vite modes ask two more questions, or take them as flags:

| Flag | Options | Default |
| --- | --- | --- |
| `--lang` | `ts`, `js` | `ts` |
| `--css` | `tailwind`, `plain` | `tailwind` |

TypeScript is the default because the API client is generic over the shape each
endpoint returns, so a field renamed on the Go side fails the build instead of
rendering `undefined`. `ry build` runs the type checker — `svelte-check` for
Svelte, `tsc` otherwise — before Vite compiles, since Vite strips types without
looking at them.

Either styling ships the same welcome page and the same design tokens.
`tailwind` adds Tailwind v4 and hands it the neutral ramp, so `bg-gray-3` and
`var(--surface-card)` mean the same colour and the light theme needs no Tailwind
configuration at all. `plain` installs nothing extra.

## The design system

Generated pages ship the Ryla design system: dark-first, with a twelve-step
monochrome ramp carrying most of the interface, one blue accent (`#0072f5`)
reserved for interactive emphasis, sharp 2px corners, and Geist Mono on every
label, metric and identifier. Depth reads through 1px borders stepping up the
ramp rather than through shadow.

`:root` **is** the dark theme. Put `data-theme="light"` on `<html>` for light —
there is no `prefers-color-scheme` branch, because dark is the brand rather
than a preference.

It lives in one file — `resources/frontend/src/app.css` for the Vite modes,
`resources/static/app.css` for `mvc` — with the tokens at the top and every
rule below written against them, so restyling an application means editing that
block and nothing else. Delete the file and nothing in the framework notices.

Geist and Geist Mono load from Google Fonts. Remove the `@import` at the top of
the stylesheet and the stacks fall back to the platform UI and monospace fonts,
which is what their fallbacks are for.

## What a project looks like

```
myapp/
├── app/
│   ├── controllers/     handlers, embedding a Base with render helpers
│   ├── models/          GORM models, or document types on MongoDB
│   ├── middleware/      app-specific middleware
│   ├── requests/        what a form may contain, and what counts as valid
│   ├── jobs/            background work, registered from init
│   ├── schedule/        recurring tasks, declared in Go rather than a crontab
│   ├── app.go           the App struct — config, log, db, router
│   └── commands.go      the binary's own serve/migrate/seed commands
├── config/config.go     typed settings, read explicitly from the environment
├── database/
│   ├── driver.go        selects the SQL dialect
│   ├── migrations/      timestamped, self-registering Go migrations
│   └── seeders/
├── resources/
│   ├── views/           templ components, compiled and type-checked
│   └── static/          embedded into the binary
├── routes/web.go        the route table, written by hand
├── tests/               feature tests through the real router
├── cmd/app/main.go      wiring: build the app, register routes, run
└── ryla.yaml            manifest ry reads to build and run the project
```

That is the `mvc` shape. `react` and `svelte` replace `resources/` with a Vite
application in `resources/frontend`; `api` has no `resources/` at all. MongoDB
projects have no `database/migrations`, and declare indexes on the models
instead.

## Authentication

`ry make:auth` writes the whole flow as ordinary code you own: a User model and
its migration, registration, sign-in, sign-out, email verification and password
reset. `ry make:2fa` adds TOTP on top — enrolment with a QR code, a challenge
that holds a signed-in session until it is answered, and single-use recovery
codes.

`make:auth` works in every web mode and against every database. On `mvc` it
writes templ pages; on `api`, `react` and `svelte` it writes JSON endpoints
under `/api/auth` and prints their contract. The storage half is chosen the same
way: a GORM model and a migration, or a document with its own unique index.

What it does not vary is the flow. Both presentations call one `authCore`, so
the decisions that have to be right — what a failed sign-in reveals, when a
password hash is upgraded, how long an emailed link lives — exist once and
cannot drift apart. It does not generate frontend screens yet; the printed
contract is what you build them against.

The session is a cookie for JSON clients too. For a browser that is the safer
choice: the cookie is `HttpOnly`, so a script that manages to run on your page
cannot read the credential, which is not true of a token in local storage. The
CSRF check therefore looks at the credential rather than the path, and the
generated API client sends the token.

`make:2fa` follows the same shape: templ pages on `mvc`, JSON endpoints under
`/api/two-factor` everywhere else, and the same `twoFactorCore` behind both. On
a document store the three fields simply appear on the User document, so there
is no migration to run.

Three decisions are worth knowing, because they are the parts that are easy to
get subtly wrong:

- A wrong password and an unknown email produce the same message, and the
  unknown-email path still runs a hash, so neither the wording nor the timing
  says whether an address is registered.
- Registration answers "check your inbox" either way and puts the difference in
  the email. That is also why it does not sign you in: doing so would make a new
  address observably different from an existing one.
- `auth.Login` regenerates the session id, which is what closes session
  fixation. Sessions live in the store rather than the cookie, so they can be
  revoked.

For API and mobile clients, the `bearer` package authenticates from an
`Authorization: Bearer` header against personal access tokens stored as SHA-256
digests, with scopes, expiry and immediate revocation. The plaintext exists once,
when the token is created; nothing stores it, so a settings page can list a
user's tokens but never show them again. It is built on GORM, and there is no
generator for it yet — see Status below.

## Testing

The `testing` package (imported as `rytest`) drives the real application rather
than calling handler functions:

```go
client(t).LoginID(user.ID).Get("/posts").AssertOK().AssertContains("First post")
```

`rytest.Env` points the process at a throwaway in-memory SQLite database and a
configuration safe to test against; `rytest.Migrate` applies the migrations;
`rytest.New` returns a client with its own cookie jar that attaches the CSRF
token to every unsafe request. Assertions print the response body when they
fail, which is usually the whole diagnosis.

Generated projects ship a `tests/example_test.go` wired up this way, and
`ry make:test Posts` adds more.

## Design decisions

**No container, no facades.** `App` is a plain struct. Controllers hold a
`*app.App` and reach for what they need. Every dependency is visible in
`app.go`, and the compiler checks all of it.

**Generators never edit your files.** `ry make:controller` writes a new file and
*prints* the route lines to add. `routes/web.go` stays a reliable table of
contents, because nothing rewrites it behind your back.

**Migrations carry their own schema.** A generated create-table migration
defines a local snapshot struct rather than pointing at your model. A migration
has to keep doing what it did the day it was written; if it referenced the
model, every later field change would silently rewrite history.

**Views are compiled.** templ turns components into Go, so a typo or a wrong
prop is a build error. `ry` runs `go tool templ generate` from the project's own
`go.mod`, so there is no global binary to install or keep in sync.

**Pure Go SQLite.** The SQLite driver is `glebarez/sqlite`, wrapping
`modernc.org/sqlite`. The CGO driver would break cross-compilation and the
single static binary, which is most of the reason to build this on Go at all.

**One binary in production.** The built artifact carries its own commands, so a
server needs no Go toolchain and no `ry`:

```bash
./myapp migrate
./myapp serve
```

## Status

Ryla is pre-1.0. Everything below exists and builds, so the headings sort by how
well it is actually tested rather than by whether it is finished — "it compiles"
and "it works" are not the same claim, and this section is only useful if it
keeps them apart.

**Built, and unit-tested in process.** Routing, middleware, typed config,
logging, request validation, CSRF, flash messages, signed cookies, sessions,
password authentication, rate limiting, mail, signed links, background jobs,
cache, the scheduler, migrations, personal access tokens, TOTP two-factor, the
testing helpers, the generators, and the `dev`/`build`/`start` loop. The Redis
drivers for cache, sessions and the queue are tested against an in-process
server speaking the real protocol, so the actual client and Lua scripts run.

**Built, and covered end to end.** Every one of the sixteen database × web-mode
combinations is scaffolded into a temporary directory on each CI run and checked
to build, vet, test and be gofmt-clean, on Linux, macOS and Windows. The
`make:auth` scaffold gets the same treatment across every web mode on both
SQLite and MongoDB, and the feature tests it ships run as part of it — so the
sign-in flow is exercised, not merely compiled. `make:2fa` gets the same
treatment, and its feature tests enrol, answer a challenge, spend a recovery
code and turn the whole thing off again. This is the test that matters most:
templates are text, so nothing else in the build would catch a broken one.

**Built, and integration-tested against a real server.** MongoDB — the driver,
and the cache, session and queue stores built on it — runs against a `mongo:7`
service container in CI.

**Built, but not integration-tested in CI.** Postgres and MySQL. Both are
exercised by the scaffold tests, so the generated project and its shipped
migrations compile and vet, and `db/schema_live_test.go` will run the migrations
against a real server when `POSTGRES_TEST_DSN` or `MYSQL_TEST_DSN` is set — but
CI does not set them, so on CI nothing runs those migrations. Treat the two
engines as working but less proven than SQLite and MongoDB.

**Built, but not scaffolded.** The `bearer` package (personal access tokens) is
implemented and unit-tested, and there is a migration template for the table,
but no generator wires it into a project yet. Using it today means writing the
migration and the routes by hand. It is GORM-only, so MongoDB projects need
their own token storage.

**Not built.** OAuth and social sign-in. Frontend screens for `make:auth` and
`make:2fa` on `react` and `svelte`: the endpoints and their contract are
generated, the components are not, so the sign-in page and the enrolment screen
are still yours to write.

## Development

```bash
go test ./...
```

The test that matters most scaffolds every database × web-mode combination into
a temporary directory and checks the result builds, vets and is gofmt-clean. A
scaffolder that emits code which does not compile is worse than no scaffolder,
and because templates are text, nothing else in the build would catch it.

Integration tests skip unless they are pointed at a server, and are named
`TestLive...` so they can be selected on their own:

```bash
MONGO_TEST_URI=mongodb://localhost:27017 \
  go test ./mongo/ ./cache/mongo/ ./session/mongo/ ./queue/mongo/ -run Live -v
POSTGRES_TEST_DSN='postgres://...' go test ./db/ -run Live -v
```

Use `-run Live -v` when you want proof they ran: a missing variable is a skip,
and a skip is indistinguishable from a pass in the summary line.

To work on the framework and an app at the same time, put a `go.work` in a
parent directory joining both, or point `RYLA_PATH` at this checkout — `ry new`
then wires the generated project to your local tree.

## License

MIT — see [LICENSE](LICENSE).

The license covers the framework and the `ry` command. Code that `ry new`
writes into your project is yours: the templates are scaffolding, not a
dependency you inherit terms from, and nothing generated carries a notice back
to this repository.
