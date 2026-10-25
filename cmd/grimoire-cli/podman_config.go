package main

// podman_config.go — the configuration surface for the "podman" subcommand
// group.
//
// Every site-specific value the local Podman dev stack needs lives in one
// podmanConfig struct with sensible, documented defaults. Nothing is hardcoded
// at the point of use: the argv builders, the orchestration and the autostart
// code all read from this struct, so the command is genuinely reusable and not
// welded to one site. Values resolve in the precedence flag > env var >
// built-in default.

import "flag"

// TablePrefixDefault is the WordPress table prefix the stack uses by default.
//
// THIS IS THE SINGLE MOST ERROR-PRONE VALUE IN THE WHOLE STACK. WordPress
// concatenates the prefix with the base table name with NO separator inserted,
// so this prefix produces tables named "accuweaverposts", "accuweaveroptions",
// … — there is NO trailing underscore. Stock WordPress ("wp_posts") carries the
// underscore; this site does not. Getting it wrong fails quietly: grimoire (and
// WordPress) find no tables and offer a fresh-install wizard on top of a fully
// populated database. The same value is passed to grimoire as
// GRIMOIRE_DATABASE_TABLE_PREFIX and to WordPress as WORDPRESS_TABLE_PREFIX, so
// the two always agree about which tables they share.
const TablePrefixDefault = "accuweaver"

// Default resource names, images, ports and tuning for the stack. These mirror
// the live local setup (and the reference bash scripts) exactly. They are
// defaults, not constants: podmanConfig.addFlags and applyEnv let any of them be
// overridden so another site can reuse the command unchanged.
const (
	defaultNetwork        = "wp-net"
	defaultDBPod          = "wp-db-pod"
	defaultWebPod         = "wp-web-pod"
	defaultGrimoirePod    = "grimoire-pod"
	defaultDBCtr          = "wp-mysql"
	defaultWebCtr         = "wp-wordpress"
	defaultGrimoireCtr    = "grimoire"
	defaultDBVolume       = "wp-db-data"
	defaultHTMLVolume     = "wp-html"
	defaultContentVolume  = "wp-content"
	defaultRootSecret     = "wp-db-root-password"
	defaultUserSecret     = "wp-db-password"
	defaultMySQLImage     = "docker.io/library/mysql:8.0"
	defaultWordPressImage = "docker.io/library/wordpress:latest"
	defaultGrimoireImage  = "localhost/grimoire:dev"
	defaultGrimoirePort   = 8080
	defaultWordPressPort  = 8090
	defaultUploadsMount   = "/var/www/html/wp-content"
	defaultDBName         = "wordpress"
	defaultDBUser         = "wordpress"
	defaultDBMemory       = "640m"
	defaultWebMemory      = "448m"
	defaultGrimoireMemory = "256m"
	// defaultConfig is the grimoire config baked into the image. It is a
	// generic default (prefix "wp_"); the real prefix is supplied at run time
	// as GRIMOIRE_DATABASE_TABLE_PREFIX, which overrides the file.
	defaultConfig = "/app/configs/grimoire.podman.yaml"
	// defaultMachine is the podman machine VM the macOS LaunchAgent starts at
	// login (autostart Layer B).
	defaultMachine = "podman-machine-default"
)

// podmanConfig holds every value the podman subcommands need. Each field
// documents the invariant it encodes so the knowledge the bash runbook carried
// now travels with the code.
type podmanConfig struct {
	// Network is the bridge network every pod joins. The pods are separate
	// network namespaces and therefore cannot talk over localhost; they resolve
	// each other by pod name on this network instead (see WordPressDBHost).
	Network string

	// Pod names.
	DBPod       string // MySQL pod (no published ports).
	WebPod      string // WordPress pod (publishes WordPressPort).
	GrimoirePod string // grimoire pod (publishes GrimoirePort).

	// Container names.
	DBCtr       string // MySQL container; health is gated on this one.
	WebCtr      string // WordPress + Apache container.
	GrimoireCtr string // grimoire HTTP server container.

	// Volume names. Data lives here and MUST survive "down".
	DBVolume      string // MySQL data dir.
	HTMLVolume    string // WordPress core (rebuildable).
	ContentVolume string // wp-content: plugins/themes/uploads, SHARED with grimoire.

	// Secret names. DB credentials live in podman secrets mounted at
	// /run/secrets/<name>, never plaintext env vars, never in `podman inspect`.
	RootSecret string // MySQL root password.
	UserSecret string // wordpress DB-user password (shared by all three apps).

	// Images.
	MySQLImage     string
	WordPressImage string
	GrimoireImage  string

	// Published host ports.
	GrimoirePort  int // grimoire: GrimoirePort:8080.
	WordPressPort int // WordPress: WordPressPort:80. MySQL is deliberately unpublished.

	// TablePrefix defaults to TablePrefixDefault ("accuweaver", no trailing
	// underscore). See that constant's comment — this is the value most likely
	// to be set wrong.
	TablePrefix string

	// UploadsMount is where the shared wp-content tree is mounted inside the
	// grimoire and WordPress containers.
	UploadsMount string

	// DB identifiers.
	DBName string
	DBUser string

	// Memory limits. The default podman machine is memory constrained, so each
	// container gets an explicit cap and MySQL is tuned down from its defaults.
	DBMemory       string
	WebMemory      string
	GrimoireMemory string

	// Config is the grimoire config path inside the image.
	Config string

	// Machine is the podman machine VM name used by autostart Layer B.
	Machine string
}

// defaultPodmanConfig returns the verified defaults for the live local stack.
func defaultPodmanConfig() podmanConfig {
	return podmanConfig{
		Network:        defaultNetwork,
		DBPod:          defaultDBPod,
		WebPod:         defaultWebPod,
		GrimoirePod:    defaultGrimoirePod,
		DBCtr:          defaultDBCtr,
		WebCtr:         defaultWebCtr,
		GrimoireCtr:    defaultGrimoireCtr,
		DBVolume:       defaultDBVolume,
		HTMLVolume:     defaultHTMLVolume,
		ContentVolume:  defaultContentVolume,
		RootSecret:     defaultRootSecret,
		UserSecret:     defaultUserSecret,
		MySQLImage:     defaultMySQLImage,
		WordPressImage: defaultWordPressImage,
		GrimoireImage:  defaultGrimoireImage,
		GrimoirePort:   defaultGrimoirePort,
		WordPressPort:  defaultWordPressPort,
		TablePrefix:    TablePrefixDefault,
		UploadsMount:   defaultUploadsMount,
		DBName:         defaultDBName,
		DBUser:         defaultDBUser,
		DBMemory:       defaultDBMemory,
		WebMemory:      defaultWebMemory,
		GrimoireMemory: defaultGrimoireMemory,
		Config:         defaultConfig,
		Machine:        defaultMachine,
	}
}

// addFlags registers the overridable values on fs. The flag defaults are taken
// from the receiver, so call this on a config already seeded by
// defaultPodmanConfig and applyEnv to get the precedence flag > env > default.
func (c *podmanConfig) addFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.TablePrefix, "table-prefix", c.TablePrefix,
		"WordPress table prefix; concatenated literally with NO separator, so "+
			"\"accuweaver\" means tables like \"accuweaverposts\" (no trailing underscore)")
	fs.IntVar(&c.GrimoirePort, "grimoire-port", c.GrimoirePort,
		"host port to publish grimoire on (container listens on 8080)")
	fs.IntVar(&c.WordPressPort, "wordpress-port", c.WordPressPort,
		"host port to publish WordPress on (container listens on 80)")
	fs.StringVar(&c.Network, "network", c.Network, "podman bridge network name")
	fs.StringVar(&c.GrimoireImage, "image", c.GrimoireImage, "grimoire image tag")
	fs.StringVar(&c.MySQLImage, "mysql-image", c.MySQLImage, "MySQL image")
	fs.StringVar(&c.WordPressImage, "wordpress-image", c.WordPressImage, "WordPress image")
	fs.StringVar(&c.DBName, "db-name", c.DBName, "MySQL database name")
	fs.StringVar(&c.DBUser, "db-user", c.DBUser, "MySQL application user name")
	fs.StringVar(&c.UploadsMount, "uploads-mount", c.UploadsMount,
		"path the shared wp-content tree mounts at inside the containers")
	fs.StringVar(&c.Machine, "machine", c.Machine,
		"podman machine VM name (used by autostart on macOS)")
}

// applyEnv overlays GRIMOIRE_PODMAN_* environment variables onto the receiver,
// before flags are parsed, so the precedence is flag > env > default. Only the
// values most likely to vary per machine are wired; the rest stay flag- and
// default-driven to keep the env surface small and predictable.
func (c *podmanConfig) applyEnv(getenv func(string) string) {
	if v := getenv("GRIMOIRE_PODMAN_TABLE_PREFIX"); v != "" {
		c.TablePrefix = v
	}
	if v := getenv("GRIMOIRE_PODMAN_NETWORK"); v != "" {
		c.Network = v
	}
	if v := getenv("GRIMOIRE_PODMAN_IMAGE"); v != "" {
		c.GrimoireImage = v
	}
	if v := getenv("GRIMOIRE_PODMAN_MACHINE"); v != "" {
		c.Machine = v
	}
	if n, ok := atoiOK(getenv("GRIMOIRE_PODMAN_GRIMOIRE_PORT")); ok {
		c.GrimoirePort = n
	}
	if n, ok := atoiOK(getenv("GRIMOIRE_PODMAN_WORDPRESS_PORT")); ok {
		c.WordPressPort = n
	}
}
