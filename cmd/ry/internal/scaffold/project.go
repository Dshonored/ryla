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

	// Server marks a database that runs as a separate process. SQLite is a
	// file and needs nothing; the rest need something listening before the
	// first migration, which is worth settling at `ry new` rather than
	// discovering as a connection refused.
	Server bool
	// ComposeImage and ComposePort describe the container `ry new` can write a
	// compose file for. The credentials in that file are chosen to match
	// DefaultDSN, so the generated .env works against it untouched.
	ComposeImage string
	ComposePort  int
}

// WebMode describes one supported frontend style.
type WebMode struct {
	Name    string
	Overlay string
	// Frontend names the family of frontend overlays this mode picks from,
	// which the chosen language completes: "react" plus "ts" is the overlay
	// frontend/react-ts. It is empty for modes with no Vite build.
	//
	// react and svelte differ only in that directory — the JSON server they sit
	// in front of is one implementation, so they share one Overlay rather than
	// keeping two copies that drift apart.
	Frontend  string
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
		Server:       true,
		ComposeImage: "mongo:7",
		ComposePort:  27017,
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
		Server:       true,
		ComposeImage: "postgres:17",
		ComposePort:  5432,
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
		Server:       true,
		ComposeImage: "mysql:8",
		ComposePort:  3306,
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
		Name: "react",
		// The Go half of both single-page modes: a JSON API, and one handler
		// that proxies to Vite while developing and serves the embedded build
		// afterwards.
		Overlay:   "web/spa",
		Frontend:  "react",
		UsesVite:  true,
		Available: true,
		Summary:   "A React frontend built by Vite, embedded into the binary. JSON endpoints behind it.",
	},
	{
		Name:      "svelte",
		Overlay:   "web/spa",
		Frontend:  "svelte",
		UsesVite:  true,
		Available: true,
		Summary:   "A Svelte frontend built by Vite, embedded into the binary. JSON endpoints behind it.",
	},
}

// Language is one of the languages a Vite frontend can be written in. It is a
// separate choice from the framework because the two are independent: every
// frontend overlay exists in both.
type Language struct {
	Name    string
	Label   string
	Summary string
}

// TypeScript is first because it is the recommendation: the API client is
// generic over the shape each endpoint returns, and a Go server is already
// describing those shapes in a struct, so the types are being written either
// way — the only question is whether a compiler reads them.
var languages = []Language{
	{
		Name:    "ts",
		Label:   "TypeScript",
		Summary: "Checked at build time. `ry build` refuses to ship a type error.",
	},
	{
		Name:    "js",
		Label:   "JavaScript",
		Summary: "No compiler, no tsconfig, nothing to configure. Plain modules.",
	},
}

// DefaultLanguage is what `ry new` selects when nothing says otherwise.
const DefaultLanguage = "ts"

// Languages lists every frontend language option.
func Languages() []Language { return languages }

// LookupLanguage finds a frontend language by name.
func LookupLanguage(name string) (Language, error) {
	for _, l := range languages {
		if l.Name == name {
			return l, nil
		}
	}
	return Language{}, fmt.Errorf("unknown frontend language %q (available: %s)", name, strings.Join(languageNames(), ", "))
}

func languageNames() []string {
	out := make([]string, 0, len(languages))
	for _, l := range languages {
		out = append(out, l.Name)
	}
	sort.Strings(out)
	return out
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
	// Lang is the frontend language, "ts" or "js". It is only meaningful for a
	// mode with a Vite frontend, and empty for the rest rather than carrying a
	// value no template can use.
	Lang string

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
func NewProject(name, module, dbName, webName, langName, rylaVersion, goVersion string) (*Project, error) {
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

	// A language is only a question for a frontend that has one. Answering it
	// for an mvc project would put a field in the manifest that nothing reads
	// and that would be wrong the moment the mode changed.
	lang := ""
	if web.Frontend != "" {
		if langName == "" {
			langName = DefaultLanguage
		}
		l, err := LookupLanguage(langName)
		if err != nil {
			return nil, err
		}
		lang = l.Name
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
		Lang:           lang,
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
// order: base first, then the database, then the web mode, and last the
// frontend for the chosen language — so the most specific choice wins any file
// two of them both name.
func (p *Project) Overlays() []string {
	db, _ := LookupDatabase(p.DB)
	web, _ := LookupWebMode(p.Web)

	overlays := []string{"base", db.Overlay, web.Overlay}
	if web.Frontend != "" {
		overlays = append(overlays, "frontend/"+web.Frontend+"-"+p.Lang)
	}
	return overlays
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
