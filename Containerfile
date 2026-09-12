# grimoire container image.
#
# Two-stage, fully static (CGO_ENABLED=0) build. Nothing in grimoire needs cgo:
# SQLite is modernc.org/sqlite (pure Go), Postgres is bun/pgdriver, MySQL is
# go-sql-driver/mysql.
#
# Both binaries are included. cmd/grimoire is the HTTP server; cmd/grimoire-cli
# is needed in the same image because the overlay migration that creates
# grimoire's session table has to run before anyone can log in, and the server
# deliberately never applies migrations at startup.

FROM docker.io/library/golang:1.26-alpine AS build

WORKDIR /src

# Module cache layer: only re-download when the manifests change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# The React admin SPA is embedded via //go:embed all:dist and a placeholder
# internal/admin/dist is committed, so no Node toolchain is required here.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' \
        -o /out/grimoire ./cmd/grimoire \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' \
        -o /out/grimoire-cli ./cmd/grimoire-cli


FROM docker.io/library/alpine:3.22

# uid/gid 33 matches www-data in the Debian-based wordpress image, so grimoire
# and WordPress can share the wp-content volume without ownership conflicts.
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -g 33 grimoire \
 && adduser -u 33 -G grimoire -S -H -s /sbin/nologin grimoire

WORKDIR /app

COPY --from=build /out/grimoire /out/grimoire-cli /usr/local/bin/
# Themes are loaded from disk at runtime (filepath.Glob), not embedded.
COPY themes /app/themes
COPY configs /app/configs
COPY deploy/podman-entrypoint.sh /usr/local/bin/podman-entrypoint.sh

RUN chmod +x /usr/local/bin/podman-entrypoint.sh

USER 33:33

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/podman-entrypoint.sh"]
CMD ["grimoire", "-config", "/app/configs/grimoire.podman.yaml", "-themes", "/app/themes"]
