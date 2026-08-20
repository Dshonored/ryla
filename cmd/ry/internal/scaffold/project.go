package scaffold

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Dshonored/ryla/cmd/ry/internal/naming"
)

// RylaModule is the framework's module path. Generated projects import it.
const RylaModule = "github.com/Dshonored/ryla"

// Database describes one supported database and the defaults that go with it.
type Database struct {
	Name string
	// Overlay is the template directory applied for this database.
	Overlay string
	// DefaultDSN seeds both .env and the driver package's constant.
	DefaultDSN string
	// MaxOpenConns is the pool default for this engine.
	MaxOpenConns int
	// Available marks a database as implemented. Unavailable ones are still
	// listed by `ry new`, so the intended shape of the menu stays visible
	// rather than pretending the option does not exist.
	Available bool
	Summary   string
}

// WebMode describes one supported frontend style.
type WebMode struct {
	Name    string
	Overlay string
	// Shared is an overlay applied just before Overlay, holding what this mode
	// has in common with another. react and svelte differ only in the frontend
	// directory: the JSON server they sit in front of is one implementation, so
	// it is one set of templates rather than two that drift apart.
	Shared    string
	UsesTempl bool
	// UsesVite marks a mode whose frontend is compiled by Vite, which is what
	// puts the vite flag in the generated ryla.yaml.
	UsesVite  bool
	Available bool
	Summary   string
}

var databases = []Database{
	{
		Name:         "sqlite",
		Overlay:      "db/sqlite",
		DefaultDSN:   "storage/%s.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)",
		MaxOpenConns: 4,
		Available:    true,
		Summary:      "A single file, no server. Pure Go, so the binary still cross-compiles.",
	},
	{
		Name:    "mongo",
		Overlay: "db/mongo",
		// The database is named in the URI's path, which is the form hosting
		// providers hand out, so one pasted string is enough to connect.
		DefaultDSN: "mongodb://localhost:27017/%s",
		// The driver's own default. Named MaxOpenConns like the SQL engines
		// because it is the same knob; MongoDB calls it maxPoolSize.
		MaxOpenConns: 100,
		Available:    true,
		Summary:      "Documents rather than rows. No GORM and no migrations: indexes are declared in code.",
	},
	{
		Name:    "postgres",
		Overlay: "db/postgres",
		// The URL form rather than the space-separated keyword one: it is what
		// every hosting provider hands out, so replacing this default means
		// pasting one string. sslmode is stated outright because the driver's
		// own default negotiates and falls back, which makes whether a local
		// connection is encrypted depend on how the server was built.
		DefaultDSN: "postgres://postgres:postgres@localhost:5432/%s?sslmode=disable",
		// Postgres backs every connection with a process, so the pool is a real
		// cost on the server rather than a client-side counter. 25 sits under
		// the stock max_connections of 100 with room for a worker, a migration
		// and a psql session alongside the web process.
		MaxOpenConns: 25,
		Available:    true,
		Summary:      "A real server, and transactional DDL: a failed migration leaves nothing half-built.",
	},
	{
		Name:    "mysql",
		Overlay: "db/mysql",
		// parseTime and charset are not optional in practice: without the first
		// a DATETIME will not scan into a time.Time, and without the second the
		// connection defaults to an encoding that cannot hold every rune.
		DefaultDSN: "root:@tcp(localhost:3306)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		// Matched to Postgres for want of a reason to differ. MySQL threads are
		// cheaper than Postgres processes and max_connections defaults to 151,
		// so this is, if anything, conservative.
		MaxOpenConns: 25,
		Available:    true,
		Summary:      "MariaDB too. DDL commits implicitly, so a failed migration is not rolled back.",
	},
}

var webModes = []WebMode{
	{
		Name:      "mvc",
		Overlay:   "web/mvc",
		UsesTempl: true,
		Available: true,
		Summary:   "Server-rendered pages with templ. Compile-checked views, no JS build step.",
	},
	{
		Name:    "api",
		Overlay: "web/api",
		// No views at all, so there is nothing to generate: `ry new`, `ry dev`
		// and `ry build` skip the templ step, and the project never takes on
		// the dependency.
		UsesTempl: false,
		Available: true,
		Summary:   "JSON endpoints and no views. The errors are JSON too, 404 and 500 included.",
	},
	{
		Name:    "react",
		Overlay: "web/react",
		// The Go half of both single-page modes: a JSON API, and one handler
		// that proxies to Vite while developing and serves the embedded build
		// afterwards.
		Shared:    "web/spa",
		UsesVite:  true,
		Available: true,
		Summary:   "A React frontend built by Vite, embedded into the binary. JSON endpoints behind it.",
	},
	{
		Name:      "svelte",
		Overlay:   "web/svelte",
		Shared:    "web/spa",
		UsesVite:  true,
		Available: true,
		Summary:   "A Svelte frontend built by Vite, embedded into the binary. JSON endpoints behind it.",
	},
}

// Databases lists every known database option.
func Databases() []Database { return databases }

// WebModes lists every known web mode option.
func WebModes() []WebMode { return webModes }

// LookupDatabase finds a database by name.
func LookupDatabase(name string) (Database, error) {
	for _, d := range databases {
		if d.Name == name {
			if !d.Available {
				return Database{}, fmt.Errorf("database %q is not implemented yet (available: %s)", name, strings.Join(availableDatabases(), ", "))
			}
			return d, nil
		}
	}
	return Database{}, fmt.Errorf("unknown database %q (available: %s)", name, strings.Join(availableDatabases(), ", "))
}

// LookupWebMode finds a web mode by name.
func LookupWebMode(name string) (WebMode, error) {
	for _, w := range webModes {
		if w.Name == name {
			if !w.Available {
				return WebMode{}, fmt.Errorf("web mode %q is not implemented yet (available: %s)", name, strings.Join(availableWebModes(), ", "))
			}
			return w, nil
		}
	}
	return WebMode{}, fmt.Errorf("unknown web mode %q (available: %s)", name, strings.Join(availableWebModes(), ", "))
}

func availableDatabases() []string {
	var out []string
	for _, d := range databases {
		if d.Available {
			out = append(out, d.Name)
		}
	}
	sort.Strings(out)
	return out
}

func availableWebModes() []string {
	var out []string
	for _, w := range webModes {
		if w.Available {
			out = append(out, w.Name)
		}
	}
	sort.Strings(out)
	return out
}

// Project is the data every project template is rendered against.
type Project struct {
	Name    string
	Module  string
	Package string

	DB  string
	Web string

	RylaModule  string
	RylaVersion string
	GoVersion   string

	DefaultDSN     string
	DBMaxOpenConns int
	UsesTempl      bool
	UsesVite       bool

	// AppKey signs cookies. It is generated per project so a new application is
	// never born with a shared or empty signing key.
	AppKey string
}

// NewAppKey returns a fresh random application key.
func NewAppKey() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("scaffold: generate application key: %w", err)
	}
	return "base64:" + base64.StdEncoding.EncodeToString(b[:]), nil
}

// NewProject validates the inputs and fills in everything derived from them.
func NewProject(name, module, dbName, webName, rylaVersion, goVersion string) (*Project, error) {
	if name == "" {
		return nil, fmt.Errorf("project name is required")
	}
	if module == "" {
		// A bare name is a valid module path and keeps `ry new` usable before
		// you have decided where the code will live.
		module = name
	}
	if err := validModule(module); err != nil {
		return nil, err
	}

	db, err := LookupDatabase(dbName)
	if err != nil {
		return nil, err
	}
	web, err := LookupWebMode(webName)
	if err != nil {
		return nil, err
	}

	key, err := NewAppKey()
	if err != nil {
		return nil, err
	}

	return &Project{
		Name:           name,
		Module:         module,
		Package:        naming.Package(name),
		DB:             db.Name,
		Web:            web.Name,
		RylaModule:     RylaModule,
		RylaVersion:    rylaVersion,
		GoVersion:      goVersion,
		DefaultDSN:     fmt.Sprintf(db.DefaultDSN, naming.Kebab(name)),
		DBMaxOpenConns: db.MaxOpenConns,
		UsesTempl:      web.UsesTempl,
		UsesVite:       web.UsesVite,
		AppKey:         key,
	}, nil
}

// Overlays returns the template overlays for this project, in application
// order: base first, then database, then the web mode — preceded by whatever
// that mode shares with another, so the mode's own files still win.
func (p *Project) Overlays() []string {
	db, _ := LookupDatabase(p.DB)
	web, _ := LookupWebMode(p.Web)

	overlays := []string{"base", db.Overlay}
	if web.Shared != "" {
		overlays = append(overlays, web.Shared)
	}
	return append(overlays, web.Overlay)
}

func validModule(module string) error {
	if strings.ContainsAny(module, " \t\\") {
		return fmt.Errorf("invalid module path %q: it must not contain spaces or backslashes", module)
	}
	if path.Clean(module) != module {
		return fmt.Errorf("invalid module path %q", module)
	}
	return nil
}
