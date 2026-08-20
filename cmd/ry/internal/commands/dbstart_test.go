package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Dshonored/ryla/cmd/ry/internal/project"
	"github.com/Dshonored/ryla/cmd/ry/internal/scaffold"
)

func mustDB(t *testing.T, name string) scaffold.Database {
	t.Helper()

	db, err := scaffold.LookupDatabase(name)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// TestDSNHostReadsEveryDSNForm covers the three shapes a connection string
// arrives in. Getting one wrong is not a visible bug: it silently decides a
// local database is remote, and the container is never started.
func TestDSNHostReadsEveryDSNForm(t *testing.T) {
	cases := []struct {
		name string
		db   string
		dsn  string
		want string
	}{
		{"postgres url", "postgres", "postgres://postgres:postgres@localhost:5432/app?sslmode=disable", "localhost"},
		{"postgres remote", "postgres", "postgres://u:p@db.example.com:5432/app", "db.example.com"},
		{"postgres keyword form", "postgres", "host=localhost port=5432 dbname=app user=postgres", "localhost"},
		{"postgres keyword remote", "postgres", "host=db.internal port=5432 dbname=app", "db.internal"},
		{"mysql tcp", "mysql", "root:@tcp(localhost:3306)/app?parseTime=True", "localhost"},
		{"mysql tcp remote", "mysql", "root:pw@tcp(mysql.example.com:3306)/app", "mysql.example.com"},
		{"mysql unix socket", "mysql", "root:@unix(/tmp/mysql.sock)/app", ""},
		{"mysql bare", "mysql", "/app", "localhost"},
		{"mongo uri", "mongo", "mongodb://localhost:27017/app", "localhost"},
		{"mongo atlas", "mongo", "mongodb+srv://u:p@cluster0.abcd.mongodb.net/app", "cluster0.abcd.mongodb.net"},
		{"ipv6 loopback", "postgres", "postgres://u@[::1]:5432/app", "::1"},
		{"empty", "postgres", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dsnHost(mustDB(t, tc.db), tc.dsn); got != tc.want {
				t.Errorf("dsnHost(%q) = %q, want %q", tc.dsn, got, tc.want)
			}
		})
	}
}

// TestOnlyALocalDSNWantsAContainer is the decision that keeps this feature out
// of the way: a compose file left on disk must not start a container for
// someone who has repointed .env at a server somewhere else.
func TestOnlyALocalDSNWantsAContainer(t *testing.T) {
	pg := mustDB(t, "postgres")

	local := []string{
		"postgres://postgres:postgres@localhost:5432/app?sslmode=disable",
		"postgres://postgres@127.0.0.1:5432/app",
		"postgres://postgres@[::1]:5432/app",
		"host=localhost port=5432 dbname=app",
	}
	for _, dsn := range local {
		if !isLocalDSN(pg, dsn) {
			t.Errorf("%q should be treated as local", dsn)
		}
	}

	remote := []string{
		"postgres://u:p@db.example.com:5432/app",
		"postgres://u:p@10.0.0.7:5432/app",
		"host=db.internal port=5432 dbname=app",
	}
	for _, dsn := range remote {
		if isLocalDSN(pg, dsn) {
			t.Errorf("%q should not be treated as local", dsn)
		}
	}
}

// writeProject lays down the smallest project ensureDatabase needs to decide.
func writeProject(t *testing.T, database string, files map[string]string) *project.Project {
	t.Helper()

	root := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p := &project.Project{Root: root}
	p.Name = "app"
	p.Database = database
	return p
}

// TestProjectDSNFollowsTheApplicationsPrecedence matters because the decision
// to start a container is made from this string. Reading .env when a real
// environment variable is set would start a container for a DSN the
// application is not going to use.
func TestProjectDSNFollowsTheApplicationsPrecedence(t *testing.T) {
	pg := mustDB(t, "postgres")

	t.Run("environment wins", func(t *testing.T) {
		p := writeProject(t, "postgres", map[string]string{
			".env": "DB_DSN=postgres://postgres@localhost:5432/from_file\n",
		})
		t.Setenv("DB_DSN", "postgres://postgres@db.example.com:5432/from_env")

		if got := ProjectDSN(p, pg); got != "postgres://postgres@db.example.com:5432/from_env" {
			t.Errorf("got %q, want the environment's value", got)
		}
	})

	t.Run("then .env", func(t *testing.T) {
		p := writeProject(t, "postgres", map[string]string{
			".env": "DB_DSN=postgres://postgres@localhost:5432/from_file\n",
		})
		t.Setenv("DB_DSN", "")

		if got := ProjectDSN(p, pg); got != "postgres://postgres@localhost:5432/from_file" {
			t.Errorf("got %q, want the .env value", got)
		}
	})

	t.Run("then the built-in default", func(t *testing.T) {
		p := writeProject(t, "postgres", nil)
		t.Setenv("DB_DSN", "")

		want := "postgres://postgres:postgres@localhost:5432/app?sslmode=disable"
		if got := ProjectDSN(p, pg); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("mongo reads its own variable", func(t *testing.T) {
		p := writeProject(t, "mongo", map[string]string{
			".env": "DB_DSN=postgres://ignored\nMONGO_URI=mongodb://localhost:27017/app\n",
		})
		t.Setenv("MONGO_URI", "")

		if got := ProjectDSN(p, mustDB(t, "mongo")); got != "mongodb://localhost:27017/app" {
			t.Errorf("got %q, want the MONGO_URI value", got)
		}
	})
}

// TestComposeDatabaseGate covers every reason to do nothing. Each one is a way
// this feature could otherwise start a container nobody asked for.
func TestComposeDatabaseGate(t *testing.T) {
	const compose = "services:\n  postgres:\n    image: postgres:17\n"

	t.Run("starts for a local server database with a compose file", func(t *testing.T) {
		t.Setenv("RYLA_NO_DOCKER", "")
		t.Setenv("DB_DSN", "")
		p := writeProject(t, "postgres", map[string]string{ComposeFile: compose})

		if _, ok := composeDatabase(p); !ok {
			t.Error("a postgres project with a compose file should start it")
		}
	})

	t.Run("does nothing without a compose file", func(t *testing.T) {
		t.Setenv("RYLA_NO_DOCKER", "")
		t.Setenv("DB_DSN", "")
		p := writeProject(t, "postgres", nil)

		if _, ok := composeDatabase(p); ok {
			t.Error("no compose file means no container to start")
		}
	})

	t.Run("does nothing for sqlite", func(t *testing.T) {
		t.Setenv("RYLA_NO_DOCKER", "")
		p := writeProject(t, "sqlite", map[string]string{ComposeFile: compose})

		if _, ok := composeDatabase(p); ok {
			t.Error("sqlite is a file and needs no server")
		}
	})

	t.Run("does nothing when the DSN points elsewhere", func(t *testing.T) {
		t.Setenv("RYLA_NO_DOCKER", "")
		t.Setenv("DB_DSN", "postgres://u:p@db.example.com:5432/app")
		p := writeProject(t, "postgres", map[string]string{ComposeFile: compose})

		if _, ok := composeDatabase(p); ok {
			t.Error("a DSN naming another host must not start a local container")
		}
	})

	t.Run("RYLA_NO_DOCKER switches it off", func(t *testing.T) {
		t.Setenv("DB_DSN", "")
		t.Setenv("RYLA_NO_DOCKER", "1")
		p := writeProject(t, "postgres", map[string]string{ComposeFile: compose})

		if _, ok := composeDatabase(p); ok {
			t.Error("RYLA_NO_DOCKER should switch this off entirely")
		}
	})

	// An empty value is what a shell or a test harness leaves behind, and
	// reading it as "yes" would disable the feature for people who never asked.
	t.Run("an empty RYLA_NO_DOCKER is not an opt-out", func(t *testing.T) {
		t.Setenv("DB_DSN", "")
		t.Setenv("RYLA_NO_DOCKER", "")
		p := writeProject(t, "postgres", map[string]string{ComposeFile: compose})

		if _, ok := composeDatabase(p); !ok {
			t.Error("an empty value should count as unset")
		}
	})
}

// TestComposeFailureExplainsItself checks the diagnoses, because the whole
// point of this feature is replacing an unreadable error with a readable one.
func TestComposeFailureExplainsItself(t *testing.T) {
	pg := mustDB(t, "postgres")
	boom := os.ErrClosed

	cases := []struct {
		name   string
		output string
		want   string
	}{
		{
			"daemon down",
			"Cannot connect to the Docker daemon at unix:///var/run/docker.sock.",
			"Docker is installed but not running",
		},
		{
			"no compose plugin",
			"docker: 'compose' is not a docker command.",
			"no `compose` plugin",
		},
		{
			"port taken",
			"Error response from daemon: driver failed programming external connectivity: port is already allocated",
			"already in use",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := composeFailure(boom, tc.output, pg)
			if err == nil {
				t.Fatal("want an error")
			}
			if !contains(err.Error(), tc.want) {
				t.Errorf("error %q does not explain %q", err, tc.want)
			}
		})
	}

	t.Run("anything else keeps the output", func(t *testing.T) {
		err := composeFailure(boom, "some unrecognised compose failure", pg)
		if !contains(err.Error(), "some unrecognised compose failure") {
			t.Errorf("an unrecognised failure must not be swallowed: %v", err)
		}
	})
}

func contains(haystack, needle string) bool {
	return containsAny(haystack, needle)
}

// TestHasFullProfileIgnoresCommentedServices is the check behind `ry dev full`
// telling you nothing is enabled. The generated services are commented out, and
// a reader that counted them would report a stack it is not starting.
func TestHasFullProfileIgnoresCommentedServices(t *testing.T) {
	dir := t.TempDir()

	// Named explicitly rather than from t.Name(), which carries the subtest's
	// slash and would ask for a directory that does not exist.
	write := func(t *testing.T, name, body string) string {
		t.Helper()

		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("commented out", func(t *testing.T) {
		path := write(t, "commented", "services:\n  postgres:\n    image: postgres:17\n"+
			"#  redis:\n#    image: redis:7-alpine\n#    profiles: [\"full\"]\n")

		if hasFullProfile(path) {
			t.Error("a commented-out service is not in the profile")
		}
	})

	t.Run("uncommented", func(t *testing.T) {
		path := write(t, "enabled", "services:\n  postgres:\n    image: postgres:17\n"+
			"  redis:\n    image: redis:7-alpine\n    profiles: [\"full\"]\n")

		if !hasFullProfile(path) {
			t.Error("an enabled service should be found")
		}
	})

	t.Run("no such file", func(t *testing.T) {
		if hasFullProfile(filepath.Join(dir, "missing.yaml")) {
			t.Error("a missing file has no profile")
		}
	})
}

// TestDevAcceptsOnlyFull pins the argument the command takes. Anything else has
// to be refused rather than silently ignored, since `ry dev fulll` would
// otherwise start the database alone and look like it worked.
func TestDevAcceptsOnlyFull(t *testing.T) {
	if err := onlyFull(nil, nil); err != nil {
		t.Errorf("no arguments should be fine: %v", err)
	}
	if err := onlyFull(nil, []string{"full"}); err != nil {
		t.Errorf(`"full" should be accepted: %v`, err)
	}
	if err := onlyFull(nil, []string{"fulll"}); err == nil {
		t.Error("a misspelling has to be an error, not a silent plain dev")
	}
	if err := onlyFull(nil, []string{"full", "extra"}); err == nil {
		t.Error("two arguments should be refused")
	}
}
