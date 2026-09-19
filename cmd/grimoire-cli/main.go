// Command grimoire-cli provides operational subcommands for grimoire: "migrate"
// (apply embedded per-vendor migrations), "seed" (insert sample content),
// "createadmin" (bootstrap the first administrator), and "sessions gc" (delete
// expired sessions). All accept -config pointing at a grimoire YAML file.
//
// To adopt an existing WordPress database, use "migrate -overlay" rather than
// bare "migrate"; see runMigrate for the distinction.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/seed"
	"github.com/roboweaver/grimoire/internal/storage/wprepo"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	sub := os.Args[1]
	args := os.Args[2:]
	var err error
	switch sub {
	case "migrate":
		err = runMigrate(args)
	case "seed":
		err = runSeed(args)
	case "createadmin":
		err = runCreateAdmin(args)
	case "sessions":
		err = runSessions(args)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "grimoire-cli:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: grimoire-cli <migrate|seed|createadmin|sessions gc> [-config path]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  migrate            provision a greenfield grimoire schema")
	fmt.Fprintln(os.Stderr, "  migrate -overlay   adopt an existing WordPress database (additive, no ALTER TABLE)")
	fmt.Fprintln(os.Stderr, "  migrate -check     report schema and permalink compatibility without writing anything")
}

// runMigrate applies schema changes in one of three modes:
//
//	migrate           provision a greenfield grimoire schema (default)
//	migrate -overlay  adopt an existing WordPress database, additively
//	migrate -check    report schema compatibility, writing nothing
//
// The default mode builds the whole WordPress-compatible schema and uses plain
// ALTER TABLE ... ADD COLUMN, so it fails against a real WordPress database
// whose tables already have those columns. -overlay is the mode for that case:
// it creates only grimoire-owned tables, every statement guarded with IF NOT
// EXISTS, and issues no ALTER TABLE at all.
func runMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	cfgPath := fs.String("config", "configs/grimoire.sqlite.yaml", "path to grimoire config YAML")
	overlayMode := fs.Bool("overlay", false,
		"adopt an existing WordPress database: apply only grimoire-owned, additive DDL (no ALTER TABLE)")
	checkMode := fs.Bool("check", false,
		"report whether the database has the WordPress tables/columns grimoire needs "+
			"and whether its permalink structure is supported, then exit without writing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *overlayMode && *checkMode {
		return errors.New("migrate: -overlay and -check are mutually exclusive (-check never writes)")
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	db, err := storage.OpenSQL(cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()

	vendor, prefix := cfg.Database.Vendor, cfg.Database.TablePrefix
	ctx := context.Background()

	if *checkMode {
		return reportPreflight(ctx, db, vendor, prefix)
	}

	if *overlayMode {
		// Refuse to touch a database that is not actually WordPress-shaped: a
		// wrong table_prefix or an empty database would otherwise leave a stray
		// sessions table behind and fail confusingly later, at login.
		report, err := migrate.Preflight(ctx, db, vendor, prefix)
		if err != nil {
			return err
		}
		if err := report.Err(); err != nil {
			return err
		}
		migFS, err := storage.OverlayMigrationsFS(vendor)
		if err != nil {
			return err
		}
		version, err := migrate.ApplyOverlay(ctx, db, migFS, vendor, prefix)
		if err != nil {
			return err
		}
		fmt.Printf("overlaid %s (prefix %q) to grimoire overlay version %d; "+
			"existing WordPress tables unchanged\n", vendor, prefix, version)
		return nil
	}

	migFS, err := storage.MigrationsFS(vendor)
	if err != nil {
		return err
	}
	version, err := migrate.Apply(ctx, db, migFS, vendor, prefix)
	if err != nil {
		return err
	}
	fmt.Printf("migrated %s to schema version %d\n", vendor, version)
	return nil
}

// reportPreflight prints the -check summary: schema compatibility, the resolved
// permalink structure and whether grimoire can serve it, and any pending
// grimoire-owned migrations. It reads only -- zero-row SELECTs for the schema
// probe and three single-row option reads for the permalink report -- so it is
// safe to point at a production database.
func reportPreflight(ctx context.Context, db *sql.DB, vendor, prefix string) error {
	report, err := migrate.Preflight(ctx, db, vendor, prefix)
	if err != nil {
		return err
	}
	if err := report.Err(); err != nil {
		return err
	}
	tables := migrate.RequiredSchema()
	fmt.Printf("%s database (prefix %q) has all %d WordPress tables grimoire reads.\n",
		vendor, prefix, len(tables))

	// Report the permalink configuration next (M9a Req 4.5). This is read-only
	// and reached only after the report above is clean, so the {prefix}options
	// table is known to exist -- it is part of RequiredSchema. A failure to
	// read an option is not fatal here: OptionService maps any read error to
	// the empty string, which reports as WordPress's "plain" setting, and a
	// -check that aborted on it would withhold the pending-migration summary
	// below over a line of diagnostics.
	bunDB, err := storage.NewBunDB(vendor, db)
	if err != nil {
		return err
	}
	reportPermalinks(ctx, os.Stdout, content.NewOptionService(wprepo.NewOptionRepo(bunDB, prefix)))

	overlayFS, err := storage.OverlayMigrationsFS(vendor)
	if err != nil {
		return err
	}
	pending, err := migrate.PendingOverlay(ctx, db, overlayFS, prefix)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		fmt.Println("Grimoire-owned schema is already installed; nothing to apply.")
		return nil
	}
	fmt.Printf("Pending grimoire-owned migrations (%d): %s\n", len(pending), strings.Join(pending, ", "))
	fmt.Println("Apply them with: grimoire-cli migrate -overlay")
	return nil
}

func runSeed(args []string) error {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	cfgPath := fs.String("config", "configs/grimoire.sqlite.yaml", "path to grimoire config YAML")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	db, err := storage.OpenSQL(cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := seed.Run(context.Background(), db, cfg.Database.Vendor, cfg.Database.TablePrefix); err != nil {
		return err
	}
	fmt.Printf("seeded %s sample content\n", cfg.Database.Vendor)
	return nil
}

// runCreateAdmin bootstraps the first administrator account. The password is
// read from -password or the GRIMOIRE_ADMIN_PASSWORD environment variable so it
// need not appear in shell history; it is never echoed. The command refuses to
// overwrite an existing login.
func runCreateAdmin(args []string) error {
	fs := flag.NewFlagSet("createadmin", flag.ExitOnError)
	cfgPath := fs.String("config", "configs/grimoire.sqlite.yaml", "path to grimoire config YAML")
	login := fs.String("login", "", "administrator user_login (required)")
	email := fs.String("email", "", "administrator user_email (required)")
	pwFlag := fs.String("password", "", "administrator password (or set GRIMOIRE_ADMIN_PASSWORD)")
	display := fs.String("display-name", "", "display name (defaults to login)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *login == "" || *email == "" {
		return errors.New("createadmin: -login and -email are required")
	}
	pw := *pwFlag
	if pw == "" {
		pw = os.Getenv("GRIMOIRE_ADMIN_PASSWORD")
	}
	if pw == "" {
		return errors.New("createadmin: password required via -password or GRIMOIRE_ADMIN_PASSWORD")
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	repos, err := storage.New(cfg.Database)
	if err != nil {
		return err
	}
	defer repos.Close()

	ctx := context.Background()
	if _, err := repos.Users.ByLogin(ctx, *login); err == nil {
		return fmt.Errorf("createadmin: user %q already exists", *login)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}

	dn := *display
	if dn == "" {
		dn = *login
	}
	svc := content.NewUserService(repos.Users, repos.UserMeta, cfg.Database.TablePrefix)
	id, err := svc.Bootstrap(ctx, domain.User{
		Login:       *login,
		Nicename:    *login,
		DisplayName: dn,
		Email:       *email,
	}, pw, auth.RoleAdministrator)
	if err != nil {
		return err
	}
	fmt.Printf("created administrator %q (ID %d)\n", *login, id)
	return nil
}

// runSessions dispatches the "sessions" subcommands (currently only "gc").
func runSessions(args []string) error {
	if len(args) < 1 || args[0] != "gc" {
		return errors.New("usage: grimoire-cli sessions gc [-config path]")
	}
	fs := flag.NewFlagSet("sessions gc", flag.ExitOnError)
	cfgPath := fs.String("config", "configs/grimoire.sqlite.yaml", "path to grimoire config YAML")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	repos, err := storage.New(cfg.Database)
	if err != nil {
		return err
	}
	defer repos.Close()

	sm := &auth.SessionManager{
		Users:    repos.Users,
		Meta:     repos.UserMeta,
		Sessions: repos.Sessions,
		TTL:      cfg.Session.TTL(),
		Prefix:   cfg.Database.TablePrefix,
	}
	n, err := sm.GC(context.Background())
	if err != nil {
		return err
	}
	fmt.Printf("deleted %d expired session(s)\n", n)
	return nil
}
