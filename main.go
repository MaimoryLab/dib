package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/MaimoryLab/dib/internal/build"
	"github.com/MaimoryLab/dib/internal/config"
)

func main() {
	configPath := flag.String("config", "dib.yaml", "build configuration")
	targetName := flag.String("target", "all", "target to build (os/arch or all)")
	dryRun := flag.Bool("dry-run", false, "validate and print targets without building")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if *targetName != "all" {
		cfg.Targets = selectTarget(cfg.Targets, *targetName)
		if len(cfg.Targets) == 0 {
			log.Fatalf("target %q is not configured", *targetName)
		}
	}
	if *dryRun {
		for _, target := range cfg.Targets {
			fmt.Printf("%s/%s (%s)\n", target.OS, target.Arch, cfg.ModeFor(target))
		}
		return
	}
	if err := build.Run(context.Background(), cfg); err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stdout, "built %d target(s) in %s\n", len(cfg.Targets), cfg.Output)
}

func selectTarget(targets []config.Target, name string) []config.Target {
	for _, target := range targets {
		if target.OS+"/"+target.Arch == name {
			return []config.Target{target}
		}
	}
	return nil
}
