// Package release looks up the newest published version of a module.
//
// It asks the Go module proxy rather than the GitHub API: the proxy needs no
// authentication, imposes no rate limit, is already the source of truth for
// what `go install @latest` would fetch, and works for private modules through
// whatever GOPROXY the user has configured.
package release

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// CheckInterval is how long a lookup is cached. A day is frequent enough to
// notice a release and rare enough that nobody's dev loop is doing network I/O.
const CheckInterval = 24 * time.Hour

// Info describes a published version.
type Info struct {
	Version string    `json:"Version"`
	Time    time.Time `json:"Time"`
}

// Latest asks the proxy for the newest version of module.
func Latest(ctx context.Context, module string) (Info, error) {
	base := proxyBase()
	url := fmt.Sprintf("%s/%s/@latest", strings.TrimRight(base, "/"), escapeModule(module))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Info{}, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("module proxy returned %s for %s", resp.Status, module)
	}

	var info Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return Info{}, fmt.Errorf("decode proxy response: %w", err)
	}
	return info, nil
}

// Cached returns the newest version, reusing a recent lookup when there is one.
//
// Every failure is silent by design: an update notice is a convenience, and a
// flaky network or an air-gapped machine must never make `ry dev` slower or
// noisier than it would otherwise be.
func Cached(ctx context.Context, module string) (Info, bool) {
	if disabled() {
		return Info{}, false
	}

	path := cachePath(module)
	if info, ok := readCache(path); ok {
		return info, true
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	info, err := Latest(ctx, module)
	if err != nil {
		// Remember the failure briefly too, so an offline machine does not
		// retry on every single command.
		writeCache(path, Info{}, time.Now())
		return Info{}, false
	}

	writeCache(path, info, time.Now())
	return info, true
}

// IsNewer reports whether latest is a later release than current.
//
// A development build — anything untagged, dirty, or carrying a pseudo-version
// — is never "out of date": its code is not on the proxy at all, so comparing
// it to a published tag would be meaningless.
func IsNewer(current, latest string) bool {
	if latest == "" || current == "" || current == "dev" {
		return false
	}
	if strings.Contains(current, "+dirty") {
		return false
	}
	if !semver.IsValid(current) || !semver.IsValid(latest) {
		return false
	}
	return semver.Compare(latest, current) > 0
}

func disabled() bool {
	for _, key := range []string{"RYLA_NO_UPDATE_CHECK", "CI"} {
		// An empty value counts as unset. Shells and test harnesses set
		// variables to "" routinely, and treating that as "yes, I am CI" would
		// silently switch the check off.
		switch v := os.Getenv(key); v {
		case "", "0", "false":
		default:
			return true
		}
	}
	return false
}

func proxyBase() string {
	proxy := os.Getenv("GOPROXY")
	if proxy == "" || proxy == "off" || proxy == "direct" {
		return "https://proxy.golang.org"
	}
	// GOPROXY is a comma or pipe separated fallback list; the first usable
	// entry is the one the go command would try first.
	for _, part := range strings.FieldsFunc(proxy, func(r rune) bool { return r == ',' || r == '|' }) {
		part = strings.TrimSpace(part)
		if part != "" && part != "direct" && part != "off" {
			return part
		}
	}
	return "https://proxy.golang.org"
}

// escapeModule applies the module proxy's case encoding, where an uppercase
// letter becomes "!" followed by its lowercase form. Without it,
// github.com/Dshonored/ryla would 404.
func escapeModule(module string) string {
	var b strings.Builder
	for _, r := range module {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

type cacheEntry struct {
	Info      Info      `json:"info"`
	CheckedAt time.Time `json:"checked_at"`
}

func cachePath(module string) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	name := strings.NewReplacer("/", "_", ":", "_").Replace(module) + ".json"
	return filepath.Join(dir, "ryla", name)
}

func readCache(path string) (Info, bool) {
	if path == "" {
		return Info{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Info{}, false
	}

	var entry cacheEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return Info{}, false
	}
	if time.Since(entry.CheckedAt) > CheckInterval {
		return Info{}, false
	}
	if entry.Info.Version == "" {
		// A remembered failure: still fresh, still nothing to report.
		return Info{}, true
	}
	return entry.Info, true
}

func writeCache(path string, info Info, at time.Time) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	raw, err := json.Marshal(cacheEntry{Info: info, CheckedAt: at})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0o644)
}
