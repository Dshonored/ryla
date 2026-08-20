#!/bin/sh
#
# Install the Ryla CLI.
#
#   curl -fsSL https://raw.githubusercontent.com/Dshonored/ryla/main/install.sh | sh
#
# Ryla is a Go framework, so a Go toolchain is not an installer detail you can
# skip: `ry dev` and `ry build` shell out to it on every save. This script
# therefore does the whole job — find or install Go, install `ry`, and put both
# on PATH permanently — so that "install Ryla" is one command rather than a
# checklist ending in "now fix your PATH".
#
# It is also the update path: re-running it reinstalls the latest `ry` over the
# old one and leaves everything else alone.
#
# Options, as flags or environment variables:
#
#   --version <v>        RYLA_VERSION       version to install (default: latest)
#   --no-modify-path     RYLA_NO_MODIFY_PATH=1   do not touch any shell rc file
#                        RYLA_HOME          where a downloaded Go lives
#                                           (default: ~/.ryla)
set -eu

# Wrapping everything in main() and calling it on the last line is deliberate:
# piped into `sh`, the script is read from stdin, and a child process that also
# reads stdin would otherwise swallow the rest of it. Defining a function forces
# the shell to read the whole body before any of it runs.
main() {
	RYLA_MODULE="github.com/Dshonored/ryla"

	# The `go` directive in go.mod. install_test.go fails if the two drift.
	MIN_GO="1.25.2"

	RYLA_VERSION="${RYLA_VERSION:-latest}"
	RYLA_HOME="${RYLA_HOME:-$HOME/.ryla}"
	RYLA_NO_MODIFY_PATH="${RYLA_NO_MODIFY_PATH:-}"

	# Set when this script downloads a toolchain itself, in which case its bin
	# directory has to go on PATH too — nothing else on the machine knows it is
	# there.
	MANAGED_GO_BIN=""

	# The PATH as the user's shell had it. Everything below prepends to this
	# script's own PATH so it can use what it installs; the closing advice has to
	# be judged against the shell they will go back to, not against ours.
	ORIG_PATH="$PATH"

	parse_args "$@"
	detect_platform

	ensure_go
	install_ry
	if [ -z "$RYLA_NO_MODIFY_PATH" ]; then
		update_shell_path
	fi
	report
}

# ---------------------------------------------------------------- diagnostics

# Escapes only when something is there to render them; piped into a log or a
# CI job, this prints plain text.
if [ -t 1 ] && [ -t 2 ]; then
	BOLD=$(printf '\033[1m'); YELLOW=$(printf '\033[33m')
	RED=$(printf '\033[31m'); RESET=$(printf '\033[0m')
else
	BOLD=''; YELLOW=''; RED=''; RESET=''
fi

say() { printf '%s\n' "$*"; }
step() { printf '%s==>%s %s\n' "$BOLD" "$RESET" "$*"; }
warn() { printf '%swarning:%s %s\n' "$YELLOW" "$RESET" "$*" >&2; }
die() { printf '%serror:%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

usage() {
	cat <<'USAGE'
Install the Ryla CLI, and a Go toolchain if this machine has none.

  install.sh [--version <v>] [--no-modify-path]

  --version <v>     version of ry to install (default: latest)
  --no-modify-path  do not touch any shell startup file

Environment: RYLA_VERSION, RYLA_NO_MODIFY_PATH, RYLA_HOME.
USAGE
	exit 0
}

parse_args() {
	while [ $# -gt 0 ]; do
		case "$1" in
			--version)
				[ $# -ge 2 ] || die "--version needs a value"
				RYLA_VERSION="$2"
				shift 2
				;;
			--version=*) RYLA_VERSION="${1#--version=}"; shift ;;
			--no-modify-path) RYLA_NO_MODIFY_PATH=1; shift ;;
			-h|--help) usage ;;
			*) die "unknown option: $1" ;;
		esac
	done
}

detect_platform() {
	case "$(uname -s)" in
		Darwin) GO_OS=darwin ;;
		Linux) GO_OS=linux ;;
		*)
			die "$(uname -s) is not supported by this script.
On Windows, install Go from https://go.dev/dl/ and run:
  go install $RYLA_MODULE/cmd/ry@latest"
			;;
	esac

	case "$(uname -m)" in
		arm64|aarch64) GO_ARCH=arm64 ;;
		x86_64|amd64) GO_ARCH=amd64 ;;
		*) die "unsupported architecture: $(uname -m)" ;;
	esac
}

# ------------------------------------------------------------------------ Go

# ensure_go leaves a Go new enough for Ryla on PATH, installing one if needed.
ensure_go() {
	ensure_go_toolchain

	# A downloaded toolchain belongs in the shell's PATH permanently only for as
	# long as it is the one in use. Deciding that from which `go` actually won,
	# rather than from what this run happened to do, keeps the rc block stable
	# across re-runs — and lets the line get out of the way by itself if a newer
	# Go is installed some other way later.
	case "$(command -v go)" in
		"$RYLA_HOME"/go/bin/go) MANAGED_GO_BIN="$RYLA_HOME/go/bin" ;;
		*) MANAGED_GO_BIN="" ;;
	esac
}

ensure_go_toolchain() {
	current=$(installed_go_version)
	if [ -n "$current" ] && version_ge "$current" "$MIN_GO"; then
		step "Go $current is already installed."
		return
	fi

	# A toolchain an earlier run downloaded is only on PATH once the shell has
	# been reloaded. Without this, running the script twice in one terminal
	# downloads Go twice.
	if [ -x "$RYLA_HOME/go/bin/go" ]; then
		PATH="$RYLA_HOME/go/bin:$PATH"
		export PATH
		current=$(installed_go_version)
		if [ -n "$current" ] && version_ge "$current" "$MIN_GO"; then
			step "Go $current is already installed in $(tilde "$RYLA_HOME/go")."
			return
		fi
	fi

	if [ -n "$current" ]; then
		step "Go $current is older than the $MIN_GO Ryla needs. Updating."
	else
		step "No Go toolchain found. Installing one."
	fi

	# Homebrew first, because a Go it manages is a Go the user can upgrade the
	# way they upgrade everything else. Its formula can lag, though, and on an
	# older macOS it can be pinned well behind — so the result is checked, and a
	# Go that is still too old falls through to the download rather than failing.
	if have brew; then
		if install_go_with_brew && go_is_adequate; then
			return
		fi
		warn "Homebrew did not produce a Go $MIN_GO or newer; downloading one instead."
	fi

	install_go_from_tarball

	go_is_adequate ||
		die "installed Go $(installed_go_version), but Ryla needs $MIN_GO or newer."
}

# go_is_adequate reports whether the `go` on PATH is new enough to build Ryla.
go_is_adequate() {
	found=$(installed_go_version)
	[ -n "$found" ] && version_ge "$found" "$MIN_GO"
}

# installed_go_version prints the version of the `go` on PATH, or nothing.
installed_go_version() {
	have go || return 0
	# `go version` prints "go version go1.25.2 darwin/arm64". The split into
	# fields is the point, so the expansion is deliberately unquoted.
	# shellcheck disable=SC2046
	set -- $(go version 2>/dev/null)
	[ $# -ge 3 ] || return 0
	printf '%s\n' "${3#go}"
}

# version_ge reports whether $1 >= $2, comparing dotted numbers.
#
# Not `sort -V`: the BSD sort on macOS and GNU sort disagree about it often
# enough that a stock machine cannot rely on the answer.
version_ge() {
	# A toolchain built from source reports something like "devel go1.26-abc123".
	# Whoever runs one of those knows more about their Go than this script does,
	# so take it.
	case "$1" in
		[0-9]*) ;;
		*) return 0 ;;
	esac

	awk -v a="$1" -v b="$2" 'BEGIN {
		na = split(a, x, ".")
		nb = split(b, y, ".")
		n = (na > nb ? na : nb)
		for (i = 1; i <= n; i++) {
			ai = (i <= na ? x[i] + 0 : 0)
			bi = (i <= nb ? y[i] + 0 : 0)
			if (ai > bi) exit 0
			if (ai < bi) exit 1
		}
		exit 0
	}'
}

install_go_with_brew() {
	step "Installing Go with Homebrew."

	# `brew upgrade go` fails when Go came from somewhere else, and
	# `brew install go` fails when it is already a formula. Ask first.
	if brew list --formula go >/dev/null 2>&1; then
		brew upgrade go </dev/null || return 1
	else
		brew install go </dev/null || return 1
	fi

	# A brew installed moments ago may not be on this script's PATH yet, even
	# though it will be in the user's next shell.
	prefix=$(brew --prefix 2>/dev/null || true)
	if [ -n "$prefix" ] && [ -x "$prefix/bin/go" ]; then
		PATH="$prefix/bin:$PATH"
		export PATH
	fi
	have go
}

install_go_from_tarball() {
	version=$(latest_go_version)
	tarball="${version}.${GO_OS}-${GO_ARCH}.tar.gz"
	url="https://go.dev/dl/${tarball}"
	dest="$RYLA_HOME/go"

	step "Downloading $version from go.dev."

	tmp=$(mktemp -d "${TMPDIR:-/tmp}/ryla-go.XXXXXX")
	trap 'rm -rf "$tmp"' EXIT INT TERM

	fetch "$url" > "$tmp/$tarball" || die "could not download $url"
	verify_checksum "$tmp/$tarball" "$tarball"

	# Only ever replace a directory this script created. Anything else under
	# that path was put there by someone else and is not ours to delete.
	if [ -e "$dest" ] && [ ! -x "$dest/bin/go" ]; then
		die "$dest exists but does not look like a Go installation. Remove it, or set RYLA_HOME."
	fi
	rm -rf "$dest"
	mkdir -p "$RYLA_HOME"

	# The archive already contains a top-level go/ directory.
	tar -C "$RYLA_HOME" -xzf "$tmp/$tarball" </dev/null ||
		die "could not unpack $tarball"

	rm -rf "$tmp"
	trap - EXIT INT TERM

	PATH="$dest/bin:$PATH"
	export PATH
	say "Installed Go to $(tilde "$dest")"
}

# latest_go_version asks go.dev what the current release is, falling back to the
# minimum Ryla needs if the network says nothing useful.
latest_go_version() {
	version=$(fetch "https://go.dev/VERSION?m=text" 2>/dev/null | head -n 1 | tr -d '\r' || true)
	case "$version" in
		go[0-9]*) printf '%s\n' "$version" ;;
		*) printf 'go%s\n' "$MIN_GO" ;;
	esac
}

verify_checksum() {
	file="$1"
	name="$2"

	actual=$(sha256_of "$file") || {
		warn "no sha256 tool available, so the download was not verified."
		return 0
	}

	# go.dev's JSON is pretty-printed, one object per downloadable file. Deleting
	# every space first and then splitting on "{" collapses each of those objects
	# onto a single line carrying both the filename and its checksum, which is as
	# much JSON parsing as this needs. Go filenames never contain spaces, so
	# nothing is lost.
	expected=$(fetch "https://go.dev/dl/?mode=json&include=all" 2>/dev/null |
		tr -d '[:space:]' |
		tr '{' '\n' |
		grep "\"filename\":\"$name\"" |
		sed -n 's/.*"sha256":"\([a-f0-9]*\)".*/\1/p' |
		head -n 1 || true)

	if [ -z "$expected" ]; then
		warn "go.dev did not publish a checksum for $name; skipping verification."
		return 0
	fi
	[ "$actual" = "$expected" ] ||
		die "checksum mismatch for $name.
  expected $expected
  got      $actual"
}

sha256_of() {
	if have shasum; then
		shasum -a 256 "$1" | awk '{print $1}'
	elif have sha256sum; then
		sha256sum "$1" | awk '{print $1}'
	else
		return 1
	fi
}

fetch() {
	if have curl; then
		curl -fsSL "$1"
	elif have wget; then
		wget -qO- "$1"
	else
		die "neither curl nor wget is available."
	fi
}

# ------------------------------------------------------------------------ ry

install_ry() {
	step "Installing ry@$RYLA_VERSION."

	GOBIN_DIR=$(go env GOBIN)
	if [ -z "$GOBIN_DIR" ]; then
		GOBIN_DIR="$(go env GOPATH)/bin"
	fi

	# Emptied, not inherited: GOOS and GOARCH exported for cross-compiling would
	# send `ry` to $GOPATH/bin/<os>_<arch>/ instead. Empty means "this machine".
	GOOS='' GOARCH='' go install "$RYLA_MODULE/cmd/ry@$RYLA_VERSION" </dev/null ||
		die "go install $RYLA_MODULE/cmd/ry@$RYLA_VERSION failed."

	RY_BIN="$GOBIN_DIR/ry"
	[ -x "$RY_BIN" ] || die "ry was not written to $GOBIN_DIR."
}

# ---------------------------------------------------------------------- PATH

update_shell_path() {
	TOUCHED=""
	# read rather than word splitting, so a home directory with a space in it
	# does not turn one rc file into two.
	while IFS= read -r rc; do
		[ -n "$rc" ] || continue
		body=$(path_block "$rc")
		if write_block "$rc" "$body"; then
			TOUCHED="${TOUCHED:+$TOUCHED
}$rc"
		fi
	done <<EOF
$(rc_files)
EOF

	# So the checks below, and anything else this script runs, see the new tools.
	PATH="$GOBIN_DIR:$PATH"
	export PATH
}

# path_entries lists the directories that have to be on PATH: where `ry` was
# written, and a downloaded Go toolchain when there is one.
path_entries() {
	if [ -n "$MANAGED_GO_BIN" ]; then
		printf '%s\n' "$MANAGED_GO_BIN"
	fi
	printf '%s\n' "$GOBIN_DIR"
}

# path_block renders the PATH lines in the syntax of the given startup file.
path_block() {
	case "$1" in
		*/config.fish) path_block_fish "$(path_entries)" ;;
		*) path_block_posix "$(path_entries)" ;;
	esac
}

# rc_candidates names the startup files to edit: the one belonging to the login
# shell, plus any other well-known rc that already exists. Someone whose
# terminal is zsh but whose scripts run under bash should find `ry` in both.
rc_candidates() {
	case "$(basename "${SHELL:-sh}")" in
		zsh) printf '%s\n' "$HOME/.zshrc" ;;
		bash) printf '%s\n' "$HOME/.bashrc" ;;
		fish) printf '%s\n' "$HOME/.config/fish/config.fish" ;;
		*) printf '%s\n' "$HOME/.profile" ;;
	esac

	for f in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.bash_profile" \
		"$HOME/.profile" "$HOME/.config/fish/config.fish"; do
		if [ -f "$f" ]; then
			printf '%s\n' "$f"
		fi
	done
}

# rc_files is rc_candidates with the duplicates removed: the login shell's rc is
# named once as the shell's own and again as one that exists.
rc_files() {
	rc_candidates | awk '!seen[$0]++'
}

# tilde rewrites a path under $HOME back to a literal $HOME, so the line written
# into an rc file stays readable and survives a moved home directory.
tilde() {
	case "$1" in
		"$HOME"/*) printf '%s\n' "\$HOME/${1#"$HOME"/}" ;;
		*) printf '%s\n' "$1" ;;
	esac
}

path_block_posix() {
	printf '%s\n' "$1" | while IFS= read -r dir; do
		[ -n "$dir" ] || continue
		# $PATH has to reach the file unexpanded.
		# shellcheck disable=SC2016
		printf 'export PATH="%s:$PATH"\n' "$(tilde "$dir")"
	done
}

path_block_fish() {
	printf '%s\n' "$1" | while IFS= read -r dir; do
		[ -n "$dir" ] || continue
		# shellcheck disable=SC2016
		printf 'set -gx PATH %s $PATH\n' "$(tilde "$dir")"
	done
}

# write_block puts body between Ryla's markers in rc, replacing any block left
# by an earlier run. It returns non-zero when the file already said exactly
# that, so the summary can list only what actually changed.
write_block() {
	rc="$1"
	body="$2"
	block="# >>> ryla >>>
$body
# <<< ryla <<<"

	if [ -f "$rc" ]; then
		current=$(awk '
			/^# <<< ryla <<<$/ { inside = 0 }
			inside { print }
			/^# >>> ryla >>>$/ { inside = 1 }
		' "$rc")
		if [ "$current" = "$body" ]; then
			return 1
		fi
	fi

	mkdir -p "$(dirname "$rc")"
	tmpfile="$rc.ryla.$$"

	if [ -f "$rc" ]; then
		# Drop any block an earlier run left, and the blank lines that used to
		# separate it, so re-running does not grow a drift of empty lines.
		awk '
			/^# >>> ryla >>>$/ { skip = 1 }
			skip == 0 { lines[++n] = $0 }
			/^# <<< ryla <<<$/ { skip = 0 }
			END {
				while (n > 0 && lines[n] ~ /^[[:space:]]*$/) n--
				for (i = 1; i <= n; i++) print lines[i]
			}
		' "$rc" > "$tmpfile"
	else
		: > "$tmpfile"
	fi

	if [ -s "$tmpfile" ]; then
		printf '\n' >> "$tmpfile"
	fi
	printf '%s\n' "$block" >> "$tmpfile"

	# Written through the existing file rather than moved over it, so the rc
	# keeps its inode and its permissions.
	cat "$tmpfile" > "$rc"
	rm -f "$tmpfile"
	return 0
}

# -------------------------------------------------------------------- report

# usable_from_users_shell reports whether both `ry` and `go` are already
# reachable from the shell that ran this script. `go` counts: `ry dev` shells
# out to it on every save, so an installation the user cannot build with is not
# finished.
usable_from_users_shell() {
	(
		PATH="$ORIG_PATH"
		export PATH
		[ "$(command -v ry 2>/dev/null || true)" = "$RY_BIN" ] &&
			command -v go >/dev/null 2>&1
	)
}

report() {
	version=$("$RY_BIN" version 2>/dev/null | head -n 1 || true)
	[ -n "$version" ] || version="ry installed"

	say ""
	step "$version"
	say ""
	say "  ry   $(tilde "$RY_BIN")"
	say "  go   $(tilde "$(command -v go)")"

	if [ -n "${TOUCHED:-}" ]; then
		say ""
		say "PATH added to:"
		printf '%s\n' "$TOUCHED" | while IFS= read -r rc; do
			say "  $(tilde "$rc")"
		done
	fi

	# A script piped into `sh` cannot change the shell that piped it, so say
	# plainly what the one remaining manual step is.
	if usable_from_users_shell; then
		say ""
		say "Ready. Try:"
	elif [ -n "${TOUCHED:-}" ]; then
		say ""
		say "Reload your shell, then try:"
		say ""
		say "  exec $(basename "${SHELL:-sh}")"
	else
		say ""
		say "Nothing was added to your PATH, so add this yourself, then:"
		say ""
		path_block "$(rc_files | head -n 1)" | while IFS= read -r line; do
			say "  $line"
		done
	fi
	say ""
	say "  ry new myapp"
	say "  cd myapp && ry dev"
	say ""
}

main "$@"
