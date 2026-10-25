# Local Podman dev stack — `grimoire-cli podman`

A guide to `grimoire-cli podman`, which deploys and manages a local development
stack where **grimoire runs next to a stock WordPress container against one
shared MySQL database**. The command wraps `podman` (it shells out to the
binary; there is no podman SDK dependency) and carries the developer guidance at
the point of use — every subcommand's `-h` explains what it does and the
gotchas, and `up` prints the invariants as it runs.

> **We target Podman, not Docker, and the reason is licensing.** Docker Desktop
> requires a paid subscription for commercial use in larger organizations, while
> Podman is Apache-2.0, free for any use, and daemonless/rootless. These
> commands all target `podman`; the Docker equivalents are a portability concern
> only. (Content was rephrased for compliance with licensing restrictions.)

---

## What runs, and how it fits together

Three Podman **pods**, all on the `wp-net` bridge network so they resolve each
other by pod name via Podman DNS:

| Pod | Container | Image | Host port | Purpose |
|-----|-----------|-------|-----------|---------|
| `wp-db-pod`    | `wp-mysql`     | `docker.io/library/mysql:8.0`        | — (wp-net only) | Shared MySQL 8.0 database |
| `wp-web-pod`   | `wp-wordpress` | `docker.io/library/wordpress:latest` | `8090` → 80 | Stock WordPress + Apache |
| `grimoire-pod` | `grimoire`     | `localhost/grimoire:dev`             | `8080` → 8080 | grimoire HTTP server |

Volumes `wp-db-data`, `wp-html`, `wp-content` hold the data; secrets
`wp-db-password` and `wp-db-root-password` hold the credentials. Endpoints once
up:

- grimoire → `http://localhost:8080` (`/`, `/healthz`, `/wp-json/wp/v2/posts`)
- WordPress → `http://localhost:8090` (`/`, `/wp-admin`)
- MySQL is **not** published to the host — reachable only inside `wp-net`.

All of these names, images and ports are **defaults**, not hardcoded constants —
see [Configuration](#configuration).

### Shared-fate warning

WordPress and grimoire share the `wp-content` volume and the same tables. There
is **no isolation**: a write in one is immediately visible in the other. Treat
the pair as one environment; the way back from a bad write is a restore from
your own backups. (Backup/restore is out of scope for this command — it is the
concern of the external WordPress stack. A fresh `up` provisions empty volumes,
so grimoire points at an empty database until WordPress is populated
separately.)

### Invariants that bite if forgotten

`up` prints each of these as it runs, and the code comments explain them where
they are encoded:

- **Table prefix is concatenated with no separator.** The default `accuweaver`
  produces tables like `accuweaverposts`, `accuweaveroptions` — **no trailing
  underscore**. Stock WordPress (`wp_posts`) carries the underscore; this does
  not. Set it wrong and both apps quietly offer a fresh-install wizard over a
  full database, and grimoire's `migrate -check` reports a good DB as
  unadoptable. The same value is passed to grimoire as
  `GRIMOIRE_DATABASE_TABLE_PREFIX` and to WordPress as `WORDPRESS_TABLE_PREFIX`.
- **DB credentials come from podman secrets**, mounted at `/run/secrets/*`.
  Never plaintext env vars, never visible in `podman inspect`. The grimoire
  container carries **no DSN and no password**: the entrypoint composes the DSN
  at start from the mounted secret.
- **Schema setup is the additive overlay migration.** `up` runs the read-only
  `migrate -check` preflight, then `migrate -overlay`, which creates only
  grimoire-owned tables (session store, overlay version tracking) with
  `IF NOT EXISTS` and issues no `ALTER TABLE`. The greenfield `migrate` (no
  `-overlay`) is **never** run against the populated WordPress database.
- **Startup is gated on DB health.** `up` polls
  `podman inspect wp-mysql --format '{{.State.Health.Status}}'` until it reports
  `healthy` before starting WordPress or grimoire, so dependents never race an
  unready database.
- **Cross-pod DNS by pod name.** The pods are separate network namespaces, so
  WordPress reaches the database as `wp-db-pod:3306`, not `localhost`.

---

## Subcommands

```bash
grimoire-cli podman up           # create/start the full stack in dependency order
grimoire-cli podman down         # stop/remove pods, preserving all data
grimoire-cli podman status       # pod/container state + endpoint probes
grimoire-cli podman build        # build localhost/grimoire:dev from the Containerfile
grimoire-cli podman autostart install|uninstall   # macOS auto-start (see below)
```

Run any of them with `-h` for the full per-subcommand guidance.

- **`up`** — ensures the network, volumes and secrets exist, then starts the DB
  pod, waits for health, runs `migrate -check` and `migrate -overlay`, and
  finally starts WordPress and grimoire. Idempotent: if a pod or container
  already exists it is **started**, not recreated, so data is preserved. The two
  secrets must already exist; `up` prints the `openssl … | podman secret create`
  command to run if one is missing (secrets are created out of band on purpose
  so no password ever lands in a flag or this process's arguments).
  `-restart-always` sets `--restart always` on the three stack containers so an
  autostart policy survives a down/up recreate; even without the flag, `up`
  auto-reapplies that policy when autostart is already configured (see
  [autostart](#macos-autostart--two-layers)).
- **`down`** — removes the three pods but keeps the volumes, secrets and
  network, so the database, the `wp-content` tree and the generated passwords
  all survive. There is deliberately no `destroy` here.
- **`status`** — prints `podman pod ps` and container state, then probes
  grimoire `/healthz` and `/wp-json/wp/v2/posts` and WordPress `/`, printing each
  HTTP status code (or `down`).
- **`build`** — builds the image from the repo's `Containerfile`, resolving the
  build context by walking up from the current directory (override with
  `-context`).

---

## macOS autostart — two layers

Podman on macOS runs inside a Linux VM. Two things stop independently, so
autostart has two layers, and `autostart install` wires both (and `uninstall`
reverses both):

**Layer A — containers return when the VM restarts.** Sets `--restart always` on
the three stack containers and enables `podman-restart.service` inside the VM
(`podman machine ssh 'systemctl --user enable podman-restart.service'`). The
VM's core user has `Linger=yes` by default, so that user service runs without an
interactive login. (The runbook's fourth `ms365-login` container is an external
MCP helper, not part of this stack, so it is intentionally out of scope here.)

**Layer B — the VM starts at Mac login.** Writes a LaunchAgent at
`~/Library/LaunchAgents/dev.grimoire.podman-machine.plist` that runs
`podman machine start <machine>` with `RunAtLoad`, logging to
`/tmp/podman-machine-autostart.log`, then `launchctl load`s it.

**The restart policy survives a recreate.** `autostart install` also writes a
small marker file (under `~/.local/share/grimoire/podman-autostart`) recording
that autostart is configured. The former lost-on-recreate behavior is fixed: the
`--restart always` policy Layer A sets used to be dropped the next time `up`
recreated a container after a `down`, because a recreated container loses its
restart policy. Now `up` **auto-reapplies `--restart always`** whenever it sees
that marker (or, as a fallback, the LaunchAgent plist), printing a one-line
notice when it does — so an autostart policy can no longer be silently lost. You
can also force the policy without configuring autostart at all with
`up -restart-always`, which sets `--restart always` on the three stack
containers at create time. `autostart uninstall` removes the marker along with
both layers.

> **Caveat:** a LaunchAgent fires at **user login**, not at the pre-login boot
> screen, so the stack returns after you log in, not before. Starting the VM
> before login would need a rootful machine plus a system LaunchDaemon — a
> bigger change that this command does not make.

**Help is consistent across the group.** A bare `autostart` with no action, and
`autostart -h` / `--help` / `help`, now print the subcommand usage and exit `0`
(they previously errored with `unknown action "-h"`). `autostart install -h` and
`autostart uninstall -h` print help and exit `0` as well; a genuinely unknown
action such as `autostart frobnicate` still errors.

---

## Configuration

Every site-specific value is a flag with a documented default, so the command is
genuinely reusable and not welded to one site. The most machine-varying ones
also read an environment variable. Resolution order is **flag > env var >
built-in default**.

| Flag | Env var | Default | Notes |
|------|---------|---------|-------|
| `-table-prefix` | `GRIMOIRE_PODMAN_TABLE_PREFIX` | `accuweaver` | No trailing underscore; see the invariant above |
| `-grimoire-port` | `GRIMOIRE_PODMAN_GRIMOIRE_PORT` | `8080` | Host port; container listens on 8080 |
| `-wordpress-port` | `GRIMOIRE_PODMAN_WORDPRESS_PORT` | `8090` | Host port; container listens on 80 |
| `-network` | `GRIMOIRE_PODMAN_NETWORK` | `wp-net` | Bridge network |
| `-image` | `GRIMOIRE_PODMAN_IMAGE` | `localhost/grimoire:dev` | grimoire image tag |
| `-machine` | `GRIMOIRE_PODMAN_MACHINE` | `podman-machine-default` | VM name for autostart |
| `-mysql-image` | — | `docker.io/library/mysql:8.0` | |
| `-wordpress-image` | — | `docker.io/library/wordpress:latest` | |
| `-db-name` / `-db-user` | — | `wordpress` / `wordpress` | |
| `-uploads-mount` | — | `/var/www/html/wp-content` | Shared wp-content mount path |
| `-restart-always` (on `up`) | — | off | Set `--restart always` on the stack containers so autostart survives a down/up recreate; auto-applied when autostart is already configured |

---

## Troubleshooting

| Symptom | First check | Likely fix |
|---------|-------------|------------|
| Nothing on `:8080`/`:8090` | `podman machine list` | VM is down → `podman machine start` |
| VM up, endpoints down | `podman pod ps` | Pods stopped → `grimoire-cli podman up` (idempotent) |
| grimoire 500s / DB errors | `podman inspect wp-mysql --format '{{.State.Health.Status}}'` | Wait for `healthy`, then re-run `up` |
| WordPress shows the install wizard | table prefix | Must match the site's prefix (default `accuweaver`, no `_`) |
| `migrate -check` says "unadoptable" | `-table-prefix` value | Must match the real tables; `up` passes it automatically |
| Cross-pod DNS fails | `podman exec wp-wordpress getent hosts wp-db-pod` | All pods must be on the same network |
| `up` aborts on a missing secret | `podman secret ls` | Create it: `openssl rand -base64 24 \| tr -d '/+=\n' \| podman secret create wp-db-password -` |

---

## Docker, for portability reference only

Docker is not the target here (see the licensing note above). If this were ever
ported: Docker has no `docker machine` VM to autostart on Docker Desktop, pods
map to a Compose project / user-defined network, and `podman secret` has no
local equivalent without Swarm. The command itself is Podman-specific (secrets,
pods, `podman machine`, `systemctl --user`) and would need rewriting for Docker.
