package main

// podman_argv.go — pure argv builders.
//
// Every podman command the stack issues is constructed here as a []string, with
// zero execution. This is the testable core: a test can assert the exact argv a
// subcommand produces without a podman binary anywhere in sight. All argv is
// derived from podmanConfig, so a non-default config (different prefix, port,
// names) flows straight through.

import (
	"fmt"
	"strconv"
)

// publish formats a "host:container" port mapping for -p.
func publish(host, container int) string {
	return strconv.Itoa(host) + ":" + strconv.Itoa(container)
}

// --- network / volumes / secrets ---------------------------------------------

func (c podmanConfig) networkExistsArgv() []string {
	return []string{"network", "exists", c.Network}
}

func (c podmanConfig) networkCreateArgv() []string {
	return []string{"network", "create", c.Network}
}

func (c podmanConfig) volumeExistsArgv(name string) []string {
	return []string{"volume", "exists", name}
}

func (c podmanConfig) volumeCreateArgv(name string) []string {
	return []string{"volume", "create", name}
}

// volumes lists the three data volumes in creation order.
func (c podmanConfig) volumes() []string {
	return []string{c.DBVolume, c.HTMLVolume, c.ContentVolume}
}

func (c podmanConfig) secretExistsArgv(name string) []string {
	return []string{"secret", "exists", name}
}

// --- pods ---------------------------------------------------------------------

func (c podmanConfig) podExistsArgv(pod string) []string {
	return []string{"pod", "exists", pod}
}

// dbPodCreateArgv creates the DB pod with NO published port: MySQL stays
// reachable only from the bridge network, never from the host.
func (c podmanConfig) dbPodCreateArgv() []string {
	return []string{"pod", "create", "--name", c.DBPod, "--network", c.Network}
}

// webPodCreateArgv creates the WordPress pod, publishing WordPressPort:80 on the
// pod (not the container).
func (c podmanConfig) webPodCreateArgv() []string {
	return []string{"pod", "create", "--name", c.WebPod, "--network", c.Network,
		"-p", publish(c.WordPressPort, 80)}
}

// grimoirePodCreateArgv creates the grimoire pod, publishing GrimoirePort:8080.
func (c podmanConfig) grimoirePodCreateArgv() []string {
	return []string{"pod", "create", "--name", c.GrimoirePod, "--network", c.Network,
		"-p", publish(c.GrimoirePort, 8080)}
}

func (c podmanConfig) containerExistsArgv(ctr string) []string {
	return []string{"container", "exists", ctr}
}

func (c podmanConfig) podStartArgv(pod string) []string {
	return []string{"pod", "start", pod}
}

func (c podmanConfig) podStopArgv(pod string) []string {
	return []string{"pod", "stop", pod}
}

// podRmArgv force-removes a pod. Used by "down"; it removes pods only, never
// volumes/secrets/network, so data survives.
func (c podmanConfig) podRmArgv(pod string) []string {
	return []string{"pod", "rm", "-f", pod}
}

// --- containers ---------------------------------------------------------------

// dbRunArgv builds the "podman run" for wp-mysql: credentials via mounted
// secrets and the *_FILE convention (never plaintext env), an explicit memory
// cap, the healthcheck the up gate polls, and the MySQL server tuning the
// memory-constrained podman machine needs.
func (c podmanConfig) dbRunArgv() []string {
	return []string{
		"run", "-d", "--pod", c.DBPod, "--name", c.DBCtr,
		"--secret", c.RootSecret, "--secret", c.UserSecret,
		// *_FILE: the image reads the password from the mounted secret file, so
		// the credential never appears in env or in `podman inspect`.
		"-e", "MYSQL_ROOT_PASSWORD_FILE=/run/secrets/" + c.RootSecret,
		"-e", "MYSQL_PASSWORD_FILE=/run/secrets/" + c.UserSecret,
		"-e", "MYSQL_DATABASE=" + c.DBName,
		"-e", "MYSQL_USER=" + c.DBUser,
		"-v", c.DBVolume + ":/var/lib/mysql",
		"--memory", c.DBMemory,
		// Health gate: the up sequence polls {{.State.Health.Status}} until
		// this reports healthy before starting any dependent.
		"--health-cmd", "mysqladmin ping -h 127.0.0.1",
		"--health-interval", "10s", "--health-retries", "10", "--health-start-period", "40s",
		c.MySQLImage,
		"--character-set-server=utf8mb4",
		"--collation-server=utf8mb4_unicode_ci",
		"--innodb_buffer_pool_size=128M",
		"--max_connections=50",
		"--performance_schema=OFF",
	}
}

// webRunArgv builds the "podman run" for wp-wordpress.
//
// WORDPRESS_DB_HOST is "<DBPod>:3306", NOT localhost: the pods are separate
// network namespaces, so WordPress reaches the database by the DB pod's name on
// the shared network. The DB password comes from the mounted secret via
// WORDPRESS_DB_PASSWORD_FILE, never a plaintext env var. WORDPRESS_TABLE_PREFIX
// carries the same prefix grimoire uses, so they share the same tables.
func (c podmanConfig) webRunArgv() []string {
	return []string{
		"run", "-d", "--pod", c.WebPod, "--name", c.WebCtr,
		"--secret", c.UserSecret,
		"-e", "WORDPRESS_DB_HOST=" + c.WordPressDBHost(),
		"-e", "WORDPRESS_DB_USER=" + c.DBUser,
		"-e", "WORDPRESS_DB_PASSWORD_FILE=/run/secrets/" + c.UserSecret,
		"-e", "WORDPRESS_DB_NAME=" + c.DBName,
		"-e", "WORDPRESS_TABLE_PREFIX=" + c.TablePrefix,
		"-e", "WORDPRESS_DEBUG=1",
		"-v", c.HTMLVolume + ":/var/www/html",
		"-v", c.ContentVolume + ":" + c.UploadsMount,
		"--memory", c.WebMemory,
		c.WordPressImage,
	}
}

// grimoireRunArgv builds the "podman run" for the grimoire container.
//
// There is deliberately NO DSN env and NO plaintext DB password: the image's
// entrypoint (deploy/podman-entrypoint.sh) composes GRIMOIRE_DATABASE_DSN at
// start from the mounted wp-db-password secret, so the credential stays out of
// `podman inspect`. GRIMOIRE_DATABASE_TABLE_PREFIX is passed because it is
// site-specific and overrides the generic "wp_" in the baked config. The shared
// wp-content volume is mounted at the same path WordPress uses, which is what
// makes the two share uploads (shared-fate).
func (c podmanConfig) grimoireRunArgv() []string {
	return []string{
		"run", "-d", "--pod", c.GrimoirePod, "--name", c.GrimoireCtr,
		"--secret", c.UserSecret,
		"-e", "GRIMOIRE_DATABASE_TABLE_PREFIX=" + c.TablePrefix,
		"-v", c.ContentVolume + ":" + c.UploadsMount,
		"--memory", c.GrimoireMemory,
		c.GrimoireImage,
	}
}

// WordPressDBHost is the cross-pod address WordPress uses to reach MySQL:
// "<DBPod>:3306". Separate pods do not share a network namespace, so this is the
// pod name on the bridge network, never localhost.
func (c podmanConfig) WordPressDBHost() string {
	return c.DBPod + ":3306"
}

// --- grimoire-cli one-shots ---------------------------------------------------

// migrateCheckArgv builds the read-only preflight: "grimoire-cli migrate
// -check". It writes nothing and runs before the overlay so a mismatched prefix
// is caught before any change is attempted.
func (c podmanConfig) migrateCheckArgv() []string {
	return c.cliOneShotArgv("migrate", "-check")
}

// migrateOverlayArgv builds the additive overlay migration: "grimoire-cli
// migrate -overlay". It creates only grimoire-owned tables (sessions, overlay
// version tracking) with IF NOT EXISTS and issues no ALTER TABLE.
//
// It is NEVER the bare greenfield "migrate": that issues ALTER TABLE ADD COLUMN
// against WordPress-owned tables and WILL fail (and could corrupt) a populated
// WordPress database. Nothing here ever constructs a bare "migrate" argv.
func (c podmanConfig) migrateOverlayArgv() []string {
	return c.cliOneShotArgv("migrate", "-overlay")
}

// cliOneShotArgv builds a throwaway "podman run --rm" for grimoire-cli on the
// shared network. The table prefix is passed so -check/-overlay resolve the
// schema through the right tables; the DB password comes from the mounted
// secret, so no credential appears here.
func (c podmanConfig) cliOneShotArgv(cliArgs ...string) []string {
	argv := []string{
		"run", "--rm", "--network", c.Network, "--secret", c.UserSecret,
		"-e", "GRIMOIRE_DATABASE_TABLE_PREFIX=" + c.TablePrefix,
		c.GrimoireImage, "grimoire-cli",
	}
	argv = append(argv, cliArgs...)
	argv = append(argv, "-config", c.Config)
	return argv
}

// --- build --------------------------------------------------------------------

// buildArgv builds "podman build -t <image> -f <Containerfile> <context>".
func (c podmanConfig) buildArgv(containerfile, context string) []string {
	return []string{"build", "-t", c.GrimoireImage, "-f", containerfile, context}
}

// --- inspect / health ---------------------------------------------------------

// healthStatusArgv builds the inspect that reports the DB container's health,
// which the up gate polls until it reads "healthy".
func (c podmanConfig) healthStatusArgv() []string {
	return []string{"inspect", c.DBCtr, "--format", "{{.State.Health.Status}}"}
}

// --- status probe endpoints ---------------------------------------------------

// statusProbe names one endpoint the status command checks.
type statusProbe struct {
	Label string
	URL   string
}

// statusProbes returns the endpoints status probes: grimoire's health and REST
// post list, and the WordPress home page. Ports come from the config so a
// remapped port is probed correctly.
func (c podmanConfig) statusProbes() []statusProbe {
	return []statusProbe{
		{Label: fmt.Sprintf("grimoire :%d /healthz", c.GrimoirePort),
			URL: hostURL(c.GrimoirePort, "/healthz")},
		{Label: fmt.Sprintf("grimoire :%d /wp-json/wp/v2/posts", c.GrimoirePort),
			URL: hostURL(c.GrimoirePort, "/wp-json/wp/v2/posts")},
		{Label: fmt.Sprintf("WordPress :%d /", c.WordPressPort),
			URL: hostURL(c.WordPressPort, "/")},
	}
}

// --- autostart ----------------------------------------------------------------

// restartContainers lists the three stack containers autostart Layer A targets.
// The runbook's fourth container (ms365-login) is an external MCP helper, not
// part of the WordPress+grimoire stack, so it is deliberately out of scope here.
func (c podmanConfig) restartContainers() []string {
	return []string{c.DBCtr, c.WebCtr, c.GrimoireCtr}
}

// updateRestartArgv builds "podman update --restart <policy> <containers...>".
// Layer A install uses "always"; uninstall uses "no".
func (c podmanConfig) updateRestartArgv(policy string) []string {
	argv := []string{"update", "--restart", policy}
	return append(argv, c.restartContainers()...)
}

// machineSSHSystemctlArgv builds the in-VM systemctl call that enables (or
// disables) podman-restart.service so containers with --restart always return
// when the VM restarts.
func (c podmanConfig) machineSSHSystemctlArgv(action string) []string {
	return []string{"machine", "ssh", c.Machine,
		"systemctl --user " + action + " podman-restart.service"}
}

// machineStartArgv builds "podman machine start <machine>", which the macOS
// LaunchAgent (Layer B) runs at login.
func (c podmanConfig) machineStartArgv() []string {
	return []string{"machine", "start", c.Machine}
}
