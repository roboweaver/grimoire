// Command grimoire-cli provides operational subcommands for grimoire: "migrate"
// (apply embedded per-vendor migrations), "seed" (insert sample content),
// "createadmin" (bootstrap the first administrator), and "sessions gc" (delete
// expired sessions). All accept -config pointing at a grimoire YAML file.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/seed"
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
}

func runMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
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
	migFS, err := storage.MigrationsFS(cfg.Database.Vendor)
	if err != nil {
		return err
	}
	version, err := migrate.Apply(context.Background(), db, migFS, cfg.Database.Vendor, cfg.Database.TablePrefix)
	if err != nil {
		return err
	}
	fmt.Printf("migrated %s to schema version %d\n", cfg.Database.Vendor, version)
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
