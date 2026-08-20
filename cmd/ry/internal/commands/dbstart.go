package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/joho/godotenv"

	"github.com/Dshonored/ryla/cmd/ry/internal/naming"
	"github.com/Dshonored/ryla/cmd/ry/internal/project"
	"github.com/Dshonored/ryla/cmd/ry/internal/scaffold"
)

// ComposeFile is the development database container `ry new` writes.
const ComposeFile = "compose.yaml"

// FullProfile is the compose profile holding the development services that are
// not the database. They ship commented out, so it is empty until someone wants
// one of them.
const FullProfile = "full"

// ensureServices starts the project's development containers before a command
// that is about to connect to them.
//
// A server database nobody started is the most common way a new project fails,
// and the failure is the worst kind: a connection refused raised from inside
// GORM, which reads as a broken framework rather than as a container that was
// never brought up. `ry new` already writes a compose file whose credentials
// match .env; starting it is the other half of that, and there is no version of
// "run this one command first, every time" that is better than doing it.
//
// It stays out of the way whenever the project is pointed at something else. No
// compose.yaml, a DSN naming a host that is not this machine, or RYLA_NO_DOCKER
// in the environment, and this does nothing at all.
// full additionally starts everything in the "full" profile, which is what
// `ry dev full` asks for.
func ensureServices(ctx context.Context, p *project.Project, out io.Writer, full bool) error {
	db, ok := composeDatabase(p)
	if !ok {
		return nil
	}

	// Docker missing is not a failure here. The application may still connect —
	// the DSN can name a server installed some other way — so this says what it
	// noticed and lets the app be the one to decide.
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Fprintf(out, "%s needs a server and %s is here, but Docker is not installed.\n"+
			"Install it from https://docs.docker.com/get-docker/, or point %s in .env at a server you have.\n\n",
			db.Name, ComposeFile, db.DSNEnv)
		return nil
	}

	if full && !hasFullProfile(p.Path(ComposeFile)) {
		fmt.Fprintf(out, "Nothing is in the %q profile yet — redis and mailpit are waiting,\n"+
			"commented out, at the bottom of %s.\n", FullProfile, ComposeFile)
	}

	// Already running is the common case for the database, and it has to stay
	// silent: this runs before every `ry migrate`, and a line of output each
	// time would be noise. The wait still happens, because a container that is
	// running is not necessarily one that is accepting connections yet.
	//
	// `full` never takes that path. Some of its services being up says nothing
	// about the rest, and the one that is missing may need an image pulled —
	// which is not something to do behind a silent prompt.
	running := !full && composeRunning(ctx, p)
	if !running {
		if full {
			fmt.Fprintln(out, "Starting the development services...")
		} else {
			fmt.Fprintf(out, "Starting %s...\n", db.Name)
		}
	}

	output, err := composeUp(ctx, p, full, !running)
	if err != nil {
		return composeFailure(err, output, db)
	}
	return nil
}

// hasFullProfile reports whether any service is actually in the full profile.
//
// The generated ones are commented out, so `ry dev full` on a fresh project
// would otherwise start exactly what `ry dev` does and look like it had been
// ignored. Comment lines are skipped, which is the whole point.
func hasFullProfile(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "profiles:") && strings.Contains(line, FullProfile) {
			return true
		}
	}
	return false
}

// composeDatabase reports the database whose container this project expects,
// and whether starting it is the right thing to do at all.
func composeDatabase(p *project.Project) (scaffold.Database, bool) {
	if disabledByEnv("RYLA_NO_DOCKER") {
		return scaffold.Database{}, false
	}

	db, err := scaffold.LookupDatabase(p.Database)
	if err != nil || !db.Server {
		return scaffold.Database{}, false
	}
	if _, err := os.Stat(p.Path(ComposeFile)); err != nil {
		return scaffold.Database{}, false
	}

	// The compose file publishes a port on this machine, so it can only be what
	// a local DSN is talking about. Someone who repointed .env at a staging
	// server still has the file on disk, and starting a container they are not
	// using would be a surprise rather than a convenience.
	if !isLocalDSN(db, ProjectDSN(p, db)) {
		return scaffold.Database{}, false
	}
	return db, true
}

// disabledByEnv reports whether an opt-out variable is set to something meaning
// yes. An empty value counts as unset: shells and test harnesses set variables
// to "" routinely, and reading that as "yes, disable it" would switch a feature
// off for people who never asked.
func disabledByEnv(key string) bool {
	switch os.Getenv(key) {
	case "", "0", "false":
		return false
	}
	return true
}

// ProjectDSN is the connection string the application is going to use.
//
// The precedence is the application's own: a real environment variable beats
// .env, which beats the built-in default. Guessing differently here would mean
// deciding whether to start a container by reading a DSN nobody uses.
func ProjectDSN(p *project.Project, db scaffold.Database) string {
	key := db.DSNEnv
	if key == "" {
		key = "DB_DSN"
	}

	if v := os.Getenv(key); v != "" {
		return v
	}
	if values, err := godotenv.Read(p.Path(".env")); err == nil {
		if v := values[key]; v != "" {
			return v
		}
	}
	return fmt.Sprintf(db.DefaultDSN, naming.Kebab(p.Name))
}

// mysqlAddr matches the address inside a Go MySQL DSN, which is not a URL:
// root:@tcp(localhost:3306)/app.
var mysqlAddr = regexp.MustCompile(`@(\w+)\(([^)]*)\)`)

// isLocalDSN reports whether dsn names a database on this machine, which is the
// only kind a published container port can be serving.
func isLocalDSN(db scaffold.Database, dsn string) bool {
	return isLoopback(dsnHost(db, dsn))
}

// dsnHost extracts the host from a connection string, or "" when it does not
// name one that a container could be behind.
func dsnHost(db scaffold.Database, dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return ""
	}

	if db.Name == "mysql" {
		m := mysqlAddr.FindStringSubmatch(dsn)
		if m == nil {
			// No address at all means the driver's own default, which is local.
			return "localhost"
		}
		if m[1] != "tcp" {
			// A unix socket is local, but it is not a published port, so no
			// container of ours is serving it.
			return ""
		}
		return hostOnly(m[2])
	}

	// Postgres also accepts the space-separated keyword form, which no URL
	// parser will read: host=localhost port=5432 dbname=app.
	if !strings.Contains(dsn, "://") {
		for _, field := range strings.Fields(dsn) {
			if host, ok := strings.CutPrefix(field, "host="); ok {
				return hostOnly(host)
			}
		}
		return ""
	}

	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// hostOnly drops a trailing :port, leaving an IPv6 literal intact.
func hostOnly(addr string) string {
	if strings.HasPrefix(addr, "[") {
		if end := strings.Index(addr, "]"); end >= 0 {
			return addr[1:end]
		}
		return addr
	}
	if host, _, found := strings.Cut(addr, ":"); found {
		return host
	}
	return addr
}

func isLoopback(host string) bool {
	switch strings.ToLower(host) {
	case "", "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	return false
}

// composeRunning reports whether the project's containers are already up.
//
// Every failure answers "no": the daemon being unreachable, or compose being
// absent, is diagnosed properly by the `up` that follows, and duplicating that
// judgement here would mean two places to keep saying the same thing.
func composeRunning(ctx context.Context, p *project.Project) bool {
	cmd := exec.CommandContext(ctx, "docker", "compose", "ps", "--quiet", "--status", "running")
	cmd.Dir = p.Root

	out, err := cmd.Output()
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

// composeUp starts the containers and waits for their health checks to pass.
//
// --wait is why the generated compose files carry health checks at all: without
// it `up -d` returns as soon as the container exists, and a migration run
// straight afterwards races a database still replaying its write-ahead log.
//
// Output is streamed on the first run because it may include an image pull,
// which takes long enough that silence looks like a hang. It is captured either
// way, since the text is the only thing that can tell a stopped daemon from an
// unhealthy container.
func composeUp(ctx context.Context, p *project.Project, full, stream bool) (string, error) {
	args := []string{"compose"}
	if full {
		args = append(args, "--profile", FullProfile)
	}
	args = append(args, "up", "-d", "--wait")

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = p.Root

	var buf bytes.Buffer
	if stream {
		cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
		cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
	} else {
		cmd.Stdout = &buf
		cmd.Stderr = &buf
	}

	err := cmd.Run()
	return buf.String(), err
}

// composeFailure turns a compose exit code into something worth reading.
func composeFailure(err error, output string, db scaffold.Database) error {
	switch {
	case containsAny(output,
		"Cannot connect to the Docker daemon",
		"docker daemon is not running",
		"The system cannot find the file specified",
		"open //./pipe/dockerDesktopLinuxEngine"):
		return errors.New("Docker is installed but not running.\n" +
			"Start Docker Desktop and try again, or set RYLA_NO_DOCKER=1 and point " +
			db.DSNEnv + " in .env at a server you already have")

	case containsAny(output, "is not a docker command", "unknown docker command"):
		return errors.New("this Docker has no `compose` plugin.\n" +
			"Install Docker Compose v2, or start the database yourself and set RYLA_NO_DOCKER=1")

	case containsAny(output, "port is already allocated", "address already in use"):
		return fmt.Errorf("port %d is already in use, so the %s container cannot start.\n"+
			"Stop whatever is on it, or point %s in .env at that server instead",
			db.ComposePort, db.Name, db.DSNEnv)
	}

	if strings.TrimSpace(output) == "" {
		return fmt.Errorf("start the %s container: %w", db.Name, err)
	}
	return fmt.Errorf("start the %s container: %w\n%s", db.Name, err, strings.TrimSpace(output))
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
