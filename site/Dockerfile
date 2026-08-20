# The Ryla website.
#
# Two stages, and the second one is empty. The whole point of this framework is
# that a build produces one static binary carrying its own views, stylesheets,
# motion bundle and install script — so the image that ships it has no distro,
# no shell and no package manager in it. There is nothing to patch and nothing
# to exploit, because there is nothing there.
#
# The build context is this directory. The site depends on a published release
# of the framework rather than the tree above it, exactly as any other Ryla
# application does, which is also what makes ryla.Version() report a real
# version: it comes from the build info, so the page cannot advertise a release
# that does not exist.

# --- build -------------------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# Dependencies first, so editing a page does not re-download the module graph.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

# templ compiles the views into Go. It is a tool dependency in go.mod, so there
# is no separate version to pin here.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go tool templ generate

# The feature tests drive the real router: they render the page, check the
# install script is served verbatim, and check the crawler files agree. A page
# that fails them should not reach a container.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go test ./...

# CGO off is what makes the binary static, and static is what makes the stage
# below possible. -trimpath keeps build paths out of it; -s -w drops the symbol
# table and DWARF, which is most of the size.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/ryla-site ./cmd/app

# The binary runs unprivileged in an image with no /etc/passwd, so the account
# has to be made here and carried across.
RUN echo 'ryla:x:10001:10001::/:/sbin/nologin' > /out/passwd

# storage/ is the only path the process writes, and a scratch image has no
# mkdir to create it at runtime.
RUN mkdir -p /out/storage && chown 10001:10001 /out/storage

# --- run ---------------------------------------------------------------------
FROM scratch

COPY --from=build /out/passwd /etc/passwd
COPY --from=build --chown=10001:10001 /out/storage /storage
COPY --from=build /out/ryla-site /ryla-site

USER 10001:10001
WORKDIR /

# Traefik reaches this over the Docker network, so binding every interface
# inside the container is right — the container is the boundary. Nothing is
# published to the host, and the tunnel is what faces the internet.
ENV APP_ENV=production \
    HTTP_ADDR=0.0.0.0:8080 \
    DB_DSN=/storage/ryla-site.db

EXPOSE 8080

# No shell in this image, so the exec form is a necessity as well as a
# preference: the binary is PID 1 and receives signals directly, which makes a
# redeploy a clean stop rather than a ten-second wait for SIGKILL.
ENTRYPOINT ["/ryla-site"]
CMD ["serve"]
