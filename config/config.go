// Package config loads application configuration from the environment.
//
// Ryla deliberately does not reflect over struct tags to populate config.
// Instead an application declares its own Config struct and fills it with
// explicit calls to the typed readers below, so every setting is visible,
// greppable and checked by the compiler.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Environment names understood by the framework.
const (
	EnvLocal      = "local"
	EnvTest       = "test"
	EnvProduction = "production"
)

// Core is the configuration every Ryla application needs. Applications embed
// it in their own Config struct and add whatever else they require.
type Core struct {
	Name  string
	Env   string
	Debug bool
	URL   string
	Addr  string
	Key   string
}

// IsProduction reports whether the app is running in the production environment.
func (c Core) IsProduction() bool { return c.Env == EnvProduction }

// IsLocal reports whether the app is running in the local environment.
func (c Core) IsLocal() bool { return c.Env == EnvLocal }

// Load reads the given dotenv files into the process environment. Values
// already present in the environment always win, so real environment variables
// override the contents of a .env file. Missing files are not an error, which
// is what makes the same call work in development and in production.
func Load(paths ...string) error {
	if len(paths) == 0 {
		paths = []string{".env"}
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := godotenv.Overload(p); err != nil {
			return err
		}
	}
	return nil
}

// String returns the value of key, or def when unset or empty.
func String(key, def string) string {
	if v, ok := lookup(key); ok {
		return v
	}
	return def
}

// Int returns the value of key parsed as an int, or def when unset or invalid.
func Int(key string, def int) int {
	v, ok := lookup(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// Bool returns the value of key parsed as a bool, or def when unset or invalid.
// It accepts the usual 1/t/T/true/TRUE spellings plus "yes" and "on".
func Bool(key string, def bool) bool {
	v, ok := lookup(key)
	if !ok {
		return def
	}
	switch strings.ToLower(v) {
	case "yes", "on":
		return true
	case "no", "off":
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// Duration returns the value of key parsed as a time.Duration, or def when
// unset or invalid.
func Duration(key string, def time.Duration) time.Duration {
	v, ok := lookup(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func lookup(key string) (string, bool) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}
