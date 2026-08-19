# Ryla

A batteries-included web framework for Go, with a CLI that writes the
boilerplate for you.

Laravel's productivity comes from two things: everything is already wired
together, and `artisan` generates the parts you would otherwise type by hand.
Ryla takes both, and drops the parts that only make sense in PHP — there are no
facades, no service container and no runtime resolution. `ry` generates real Go
files you can read, step through and delete.

```bash
go install github.com/Dshonored/ryla/cmd/ry@latest

ry new myapp
cd myapp
ry migrate
ry dev
```

## The CLI

| Command | What it does |
| --- | --- |
| `ry new <name>` | Scaffold a project, interactively or from flags |
| `ry dev` | Watch, rebuild and restart on every save |
| `ry build` | Compile one static binary with views and assets embedded |
| `ry start` | Run the compiled binary |
| `ry migrate` | Apply pending migrations |
| `ry migrate:rollback` | Undo the last batch |
| `ry migrate:status` | Show what has run |
| `ry make:model <Name>` | Model plus its create-table migration |
| `ry make:controller <Name>` | Controller, `--resource` for the seven CRUD handlers |
| `ry make:migration <desc>` | Empty migration |
| `ry make:middleware <Name>` | HTTP middleware |
| `ry make:seeder <Name>` | Database seeder |
| `ry routes` | List named routes |

## What a project looks like

```
myapp/
├── app/
│   ├── controllers/     handlers, embedding a Base with render helpers
│   ├── models/          GORM models
│   ├── middleware/      app-specific middleware
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
├── cmd/app/main.go      wiring: build the app, register routes, run
└── ryla.yaml            manifest ry reads to build and run the project
```

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

Working today: project scaffolding, routing, middleware, config, logging, GORM
with SQLite, versioned migrations, templ views, embedded assets, the code
generators, and the `dev`/`build`/`start` loop.

Planned, roughly in this order: request validation and CSRF, authentication
(sessions, then API tokens, OAuth and 2FA), queues, mail, cache, scheduler, then
the remaining database and web-mode overlays — Postgres and MySQL, and API,
React and Svelte front ends. `ry new` already lists those options and tells you
plainly which are not built yet.

## Development

```bash
go test ./...
```

The test that matters most scaffolds every database × web-mode combination into
a temporary directory and checks the result builds, vets and is gofmt-clean. A
scaffolder that emits code which does not compile is worse than no scaffolder,
and because templates are text, nothing else in the build would catch it.

To work on the framework and an app at the same time, put a `go.work` in a
parent directory joining both, or point `RYLA_PATH` at this checkout — `ry new`
then wires the generated project to your local tree.
