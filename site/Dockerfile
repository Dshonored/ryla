# The Ryla website.
#
# Two stages, and the second one is empty. The whole point of this framework is
# that a build produces one static binary carrying its own views, stylesheets,
# motion bundle and install script — so the image that ships it has no distro,
# no shell and no package manager in it. There is nothing to patch and nothing
# to exploit, because there is nothing there.
#
# Dokploy builds this from the repository root with site/Dockerfile, so the
# paths below are relative to the root rather than to site/.

# --- build -------------------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# The site is a nested module that builds against the framework beside it
# through a relative replace, so both have to be present before `go mod
# download` can resolve anything.
COPY go.mod go.sum ./
COPY site/go.mod site/go.sum ./site/

# Everything the replace directive points at. Copied before the site itself so
# a change to the page does not invalidate the dependency layer.
COPY . .

# The installer served at ryla.io/install.sh is the repository's own. Copying it
# in here is what keeps "the script on the site" and "the script in the repo"
# the same file rather than two that can drift.
RUN cp install.sh site/resources/install.sh

WORKDIR /src/site

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# templ compiles the views into Go. It comes from the project's own go.mod as a
# tool dependency, so there is no version to pin separately here.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go tool templ generate

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go test ./...

# CGO off is what makes the binary static, and static is what makes the stage
# below possible. -trimpath keeps build paths out of the binary; -s -w drops the
# symbol table and DWARF, which is most of the size.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/ryla-site ./cmd/app

# The binary runs as an unprivileged user in an image with no /etc/passwd, so
# the account has to be created here and carried across.
RUN echo 'ryla:x:10001:10001::/:/sbin/nologin' > /out/passwd

# storage/ is the only path the process writes. It is created here because a
# scratch image has no mkdir to create it at runtime.
RUN mkdir -p /out/storage && chown 10001:10001 /out/storage

# --- run ---------------------------------------------------------------------
FROM scratch

COPY --from=build /out/passwd /etc/passwd
COPY --from=build --chown=10001:10001 /out/storage /storage
COPY --from=build /out/ryla-site /ryla-site

USER 10001:10001
WORKDIR /

# Traefik reaches this over the Docker network, so binding all interfaces inside
# the container is correct — the container itself is the boundary. Nothing is
# published to the host, and the tunnel is what faces the internet.
ENV APP_ENV=production \
    HTTP_ADDR=0.0.0.0:8080 \
    DB_DSN=/storage/ryla-site.db

EXPOSE 8080

# No shell in this image, so this is the exec form by necessity as well as by
# preference: the binary is PID 1 and receives signals directly, which is what
# makes a redeploy a clean stop rather than a ten-second SIGKILL wait.
ENTRYPOINT ["/ryla-site"]
CMD ["serve"]
