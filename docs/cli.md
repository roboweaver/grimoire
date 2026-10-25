# CLI reference — `grimoire` and `grimoire-cli`

grimoire ships two binaries:

- **`grimoire`** — the public HTTP server. It loads a YAML config, opens the
  configured database, wires the content services and theme render engine into
  the router, serves the site, and shuts down gracefully.
- **`grimoire-cli`** — the operational toolbox: `migrate`, `seed`,
  `createadmin`, `sessions gc`, and the `podman` dev-stack group.

Configuration is a YAML file (see [`configs/`](../configs)); most fields can be
overridden by an environment variable, and the command-line flags take final
precedence. This page lists only flags and environment variables that actually
exist in the code — nothing is invented. For WordPress-interoperability detail,
see [`compatibility.md`](./compatibility.md); for the local Podman stack in
depth, see [`podman-dev-stack.md`](./podman-dev-stack.md).

---

## `grimoire` — the server

```bash
grimoire [-config path] [-themes dir]
```

| Flag | Default | Purpose |
|------|---------|---------|
| `-config` | `configs/grimoire.sqlite.yaml` | Path to the grimoire config YAML |
| `-themes` | `themes` | Path to the themes directory |

These are the server's only two flags. Everything else is config: the YAML
file, the `GRIMOIRE_*` environment overrides below, then the built-in defaults.

### Environment overrides

Every variable below overlays the value loaded from the config file. They are
read in `internal/config/config.go`:

| Env var | Config field | Default | Notes |
|---------|--------------|---------|-------|
| `GRIMOIRE_SERVER_ADDR` | `server.addr` | `:8080` | HTTP listen address |
| `GRIMOIRE_THEME` | `theme` | `default` | Active theme folder name |
| `GRIMOIRE_DATABASE_VENDOR` | `database.vendor` | — (required) | One of `mysql`, `postgres`, `sqlite` |
| `GRIMOIRE_DATABASE_DSN` | `database.dsn` | — (required) | Vendor DSN; redacted in logs |
| `GRIMOIRE_DATABASE_TABLE_PREFIX` | `database.table_prefix` | `wp_` | WordPress table prefix |
| `GRIMOIRE_SESSION_COOKIE_NAME` | `session.cookie_name` | `grimoire_session` | Session cookie name |
| `GRIMOIRE_SESSION_COOKIE_SECURE` | `session.cookie_secure` | `false` | `1`/`true` sets the cookie `Secure` attribute |
| `GRIMOIRE_SESSION_TTL_HOURS` | `session.ttl_hours` | `336` (14 days) | Rolling session lifetime, hours |
| `GRIMOIRE_REST_REQUIRE_TLS_FOR_APPLICATION_PASSWORDS` | `rest.require_tls_for_application_passwords` | `true` | `1`/`true` keeps the TLS/loopback gate; set `false` only for local dev |
| `GRIMOIRE_REST_TRUSTED_PROXY_HEADER` | `rest.trusted_proxy_header` | — (empty) | Header (e.g. `X-Forwarded-Proto`) a trusted TLS-terminating proxy sets |
| `GRIMOIRE_REST_PER_PAGE_MAX` | `rest.per_page_max` | `100` | Ceiling for the REST `per_page` parameter |
| `GRIMOIRE_SCHEDULER_INTERVAL_SECONDS` | `scheduler.interval_seconds` | `60` | Publish-scheduler poll interval, seconds |

### Config resolution order

For every field the effective value resolves as **file → env → defaults**:
`config.Load` reads the YAML file first, then `applyEnv` overlays any
`GRIMOIRE_*` variable that is set, then `applyDefaults` fills anything still
empty. (`rest.require_tls_for_application_passwords` is the one field that
defaults to `true` before the file is read, so omitting it keeps the secure
default; an explicit `false` in the file or env is still honored.) Finally the
config is validated: `database.vendor` must be one of the supported vendors and
`database.dsn` must be non-empty.

---

## `grimoire-cli`

```bash
grimoire-cli <migrate|seed|createadmin|sessions gc|podman> [flags]
```

Every subcommand that opens a database accepts `-config` (default
`configs/grimoire.sqlite.yaml`) and loads config through the same
file → env → defaults resolution as the server, so the `GRIMOIRE_*` overrides
above apply here too.

### `migrate` — apply schema changes

`migrate` runs in one of three modes. **Choosing the wrong one against a
populated WordPress database can fail or corrupt it — read this before running
it.**

```bash
grimoire-cli migrate            [-config path]   # greenfield (default)
grimoire-cli migrate -overlay   [-config path]   # adopt an existing WordPress DB
grimoire-cli migrate -check     [-config path]   # read-only preflight
```

| Flag | Default | Purpose |
|------|---------|---------|
| `-config` | `configs/grimoire.sqlite.yaml` | Path to the grimoire config YAML |
| `-overlay` | `false` | Adopt an existing WordPress database: apply only grimoire-owned, additive DDL (no `ALTER TABLE`) |
| `-check` | `false` | Report schema and permalink compatibility, then exit without writing |

`-overlay` and `-check` are mutually exclusive (`-check` never writes).

**When to use each:**

- **Bare `migrate` (greenfield).** Builds the whole WordPress-compatible schema
  from scratch. It uses plain `ALTER TABLE ... ADD COLUMN`, so it is for a fresh
  grimoire-owned database only. Use it for a new SQLite/MySQL/Postgres database
  that grimoire itself owns.

  > **Never run bare `migrate` against a populated WordPress database.** The
  > greenfield path issues `ALTER TABLE ADD COLUMN` against tables WordPress
  > already created with those columns, so it fails — and can corrupt — a real
  > WordPress database. For an existing WordPress site, use `-overlay`.

- **`migrate -overlay` (adopt an existing WordPress DB).** The additive path for
  an existing WordPress database. It creates only grimoire-owned tables (the
  session store and overlay version tracking), every statement guarded with
  `IF NOT EXISTS`, and issues **no** `ALTER TABLE`. It refuses to touch a
  database that is not actually WordPress-shaped (a wrong `table_prefix` or an
  empty database is rejected by a preflight) so it cannot leave a stray table
  behind.

- **`migrate -check` (read-only preflight).** Writes nothing. It reports whether
  the database has the WordPress tables and columns grimoire needs, the resolved
  permalink structure and whether grimoire can serve it, the resolved
  `category_base`/`tag_base`, any duplicate `user_nicename` values, and any
  pending grimoire-owned migrations. Safe to point at a production database —
  run it before `-overlay`.

### `seed` — insert sample content

```bash
grimoire-cli seed [-config path]
```

| Flag | Default | Purpose |
|------|---------|---------|
| `-config` | `configs/grimoire.sqlite.yaml` | Path to the grimoire config YAML |

Inserts sample posts, a page, a category, and options into the configured
database. Intended for a fresh grimoire-owned database, not an existing
WordPress site.

### `createadmin` — bootstrap the first administrator

```bash
GRIMOIRE_ADMIN_PASSWORD=... grimoire-cli createadmin -login admin -email admin@example.com [-config path]
```

| Flag | Default | Purpose |
|------|---------|---------|
| `-config` | `configs/grimoire.sqlite.yaml` | Path to the grimoire config YAML |
| `-login` | — (required) | Administrator `user_login` |
| `-email` | — (required) | Administrator `user_email` |
| `-password` | — | Administrator password (or set `GRIMOIRE_ADMIN_PASSWORD`) |
| `-display-name` | login | Display name; defaults to the login |

The password is read from `-password` or, if that is empty, the
`GRIMOIRE_ADMIN_PASSWORD` environment variable, so it need not land in shell
history; it is never echoed. The command refuses to overwrite an existing login:
if `-login` already exists it errors rather than modify the account. The new user
is created with the administrator role.

### `sessions gc` — delete expired sessions

```bash
grimoire-cli sessions gc [-config path]
```

| Flag | Default | Purpose |
|------|---------|---------|
| `-config` | `configs/grimoire.sqlite.yaml` | Path to the grimoire config YAML |

Deletes expired rows from grimoire's own session store and reports how many were
removed. `gc` is currently the only `sessions` action.

### `podman` — the local Podman dev stack

The `podman` group deploys and manages a local development stack where grimoire
runs next to a stock WordPress container against **one shared MySQL database**.
It shells out to the `podman` binary (no SDK dependency). This section is a
reference; the full guide — the two-layer autostart model, the shared-fate
warning, cross-pod DNS, and troubleshooting — is in
[`podman-dev-stack.md`](./podman-dev-stack.md).

```bash
grimoire-cli podman up            # create/start the full stack in dependency order
grimoire-cli podman down          # stop/remove pods, preserving all data
grimoire-cli podman status        # pod/container state + endpoint probes
grimoire-cli podman build         # build localhost/grimoire:dev from the Containerfile
grimoire-cli podman autostart install|uninstall   # macOS auto-start
```

Run any subcommand with `-h` for its full guidance text. A bare `podman` with no
subcommand, and `podman -h`/`--help`/`help`, print the group usage.

#### Configuration flags and env overrides

Every site-specific value is a flag with a documented default; the most
machine-varying ones also read a `GRIMOIRE_PODMAN_*` environment variable.
Resolution order is **flag > env var > built-in default** (the flag set is
seeded from defaults, env is overlaid, then flags are parsed).

| Flag | Env var | Default | Notes |
|------|---------|---------|-------|
| `-table-prefix` | `GRIMOIRE_PODMAN_TABLE_PREFIX` | `accuweaver` | Concatenated with no separator, so this yields `accuweaverposts` — **no trailing underscore** |
| `-grimoire-port` | `GRIMOIRE_PODMAN_GRIMOIRE_PORT` | `8080` | Host port; the container listens on 8080 |
| `-wordpress-port` | `GRIMOIRE_PODMAN_WORDPRESS_PORT` | `8090` | Host port; the container listens on 80 |
| `-network` | `GRIMOIRE_PODMAN_NETWORK` | `wp-net` | Podman bridge network name |
| `-image` | `GRIMOIRE_PODMAN_IMAGE` | `localhost/grimoire:dev` | grimoire image tag |
| `-machine` | `GRIMOIRE_PODMAN_MACHINE` | `podman-machine-default` | Podman machine VM name (autostart on macOS) |
| `-mysql-image` | — | `docker.io/library/mysql:8.0` | MySQL image |
| `-wordpress-image` | — | `docker.io/library/wordpress:latest` | WordPress image |
| `-db-name` | — | `wordpress` | MySQL database name |
| `-db-user` | — | `wordpress` | MySQL application user name |
| `-uploads-mount` | — | `/var/www/html/wp-content` | Path the shared `wp-content` tree mounts at inside the containers |
| `-restart-always` (on `up`) | — | off | Set `--restart always` on the stack containers (see below) |
| `-context` (on `build`) | — | repo root (walked up) | Build context directory |

The six `GRIMOIRE_PODMAN_*` variables above are the complete env surface;
`-mysql-image`, `-wordpress-image`, `-db-name`, `-db-user` and `-uploads-mount`
are flag- and default-driven only, with no env override.

#### `up` and the `-restart-always` / autostart reapply behavior

`up` ensures the network, volumes, secrets and pods exist, starts the DB pod,
waits for MySQL to report healthy, runs the read-only `migrate -check` preflight
and the additive `migrate -overlay` migration, then starts WordPress and
grimoire. It is idempotent: an existing pod or container is *started*, not
recreated, so data is preserved.

`up` takes one extra flag:

- **`-restart-always`** sets `--restart always` on the three long-lived stack
  containers (`wp-mysql`, `wp-wordpress`, `grimoire`) at create time, so an
  autostart restart policy survives a `down`/`up` recreate.
- Even **without** the flag, `up` auto-reapplies `--restart always` when
  autostart is already configured — detected by the marker file that
  `autostart install` writes, or, as a fallback, the presence of the LaunchAgent
  plist. When it does, it prints a one-line notice that it is reapplying the
  policy. This closes a bug where the autostart policy was silently lost the
  next time `up` recreated a container after a `down`.

The restart policy is only ever applied to the three stack containers; the
`migrate` one-shots are `--rm` throwaways and never carry it.

#### `autostart`

`autostart install` wires two layers (Layer A: `--restart always` plus the
in-VM `podman-restart.service`; Layer B: a macOS LaunchAgent that starts the
podman machine at login) and writes a marker file so `up` can reapply the
restart policy on a later recreate. `autostart uninstall` reverses both layers
and removes the marker.

`autostart` is help-friendly and consistent with the other subcommands: a bare
`autostart` with no action, and `autostart -h`/`--help`/`help`, print the
subcommand usage and exit `0` (they no longer error). `autostart install -h` and
`autostart uninstall -h` likewise print help and exit `0`. A genuinely unknown
action — `autostart frobnicate` — still errors with `unknown action`.

See [`podman-dev-stack.md`](./podman-dev-stack.md) for the autostart caveats
(the LaunchAgent fires at user login, not the pre-login boot screen) and the
full flag/troubleshooting tables.

---

## Common workflows

### SQLite development loop

Zero external services — a local file database:

```bash
go run ./cmd/grimoire-cli migrate -config configs/grimoire.sqlite.yaml
go run ./cmd/grimoire-cli seed    -config configs/grimoire.sqlite.yaml
GRIMOIRE_ADMIN_PASSWORD=... go run ./cmd/grimoire-cli createadmin \
  -config configs/grimoire.sqlite.yaml -login admin -email admin@example.com
go run ./cmd/grimoire -config configs/grimoire.sqlite.yaml
# browse http://localhost:8080/
```

`make migrate` / `make seed` / `make run` wrap the first, second and last of
these for the default SQLite config.

### Adopting an existing WordPress database

Point the config's DSN at the WordPress database and set `table_prefix` to match
the site. Then:

```bash
# 1. Preflight first — writes nothing, reports compatibility.
grimoire-cli migrate -check -config configs/grimoire.mysql.yaml

# 2. If the preflight is clean, apply the additive overlay (NOT bare migrate).
grimoire-cli migrate -overlay -config configs/grimoire.mysql.yaml

# 3. Serve.
grimoire -config configs/grimoire.mysql.yaml
```

Set **`media.uploads_dir`** in the config to the site's real
`wp-content/uploads` directory on disk. grimoire reads media rows from the
database but never copies the upload files, so if `uploads_dir` still points at
the default relative `wp-content/uploads` (which will not exist for an external
install), `/wp-content/uploads/*` requests and admin media thumbnails return
`404` even though the database rows resolve. See
[`user-guide.md`](./user-guide.md) for more on running against an existing
WordPress database.

### Local Podman stack (grimoire + WordPress + MySQL)

```bash
go run ./cmd/grimoire-cli podman build        # build localhost/grimoire:dev
go run ./cmd/grimoire-cli podman up           # start the stack in dependency order
go run ./cmd/grimoire-cli podman status       # probe the endpoints
# grimoire → http://localhost:8080  ·  WordPress → http://localhost:8090
go run ./cmd/grimoire-cli podman down         # stop/remove pods, keep all data
```

To survive a reboot or podman restart on macOS, run
`grimoire-cli podman autostart install` once; `up` then reapplies the restart
policy automatically on any later recreate. The full guide, including the
shared-fate warning and why the stack targets Podman rather than Docker, is in
[`podman-dev-stack.md`](./podman-dev-stack.md).
