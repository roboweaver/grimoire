// Command grimoire-cli provides operational subcommands for grimoire. In M1 it
// supports "migrate" (apply embedded per-vendor migrations) and "seed" (insert
// sample content). Both accept -config pointing at a grimoire YAML file.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/roboweaver/grimoire/internal/config"
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
	fmt.Fprintln(os.Stderr, "usage: grimoire-cli <migrate|seed> [-config path]")
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
