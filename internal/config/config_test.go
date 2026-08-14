package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dib.yaml")
	contents := `version: 1
output: dist
cache: .cache
node: {version: 24.19.0}
dsh: {package: "@deepseek-ai/dsh", version: 0.1.0-rc.6, plugins: []}
runtime: {mode: gui, host: 127.0.0.1, port: 3080}
targets:
  - {os: linux, arch: arm64, mode: serve}
  - {os: windows, arch: amd64}
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ModeFor(cfg.Targets[0]); got != "serve" {
		t.Fatalf("mode = %q, want serve", got)
	}
	if cfg.Output != filepath.Join(dir, "dist") {
		t.Fatalf("output = %q", cfg.Output)
	}
	if cfg.MacOS.Format != "tar.gz" {
		t.Fatalf("macos format = %q, want tar.gz", cfg.MacOS.Format)
	}
	if cfg.Windows.Format != "zip" || len(cfg.Linux.Formats) != 1 || cfg.Linux.Formats[0] != "tar.gz" {
		t.Fatalf("unexpected package defaults: windows=%q linux=%v", cfg.Windows.Format, cfg.Linux.Formats)
	}
	if cfg.Linux.Depends.Deb[0] != "libgtk-4-1" || cfg.Linux.Depends.RPM[0] != "gtk4" {
		t.Fatalf("unexpected Linux GUI dependencies: %+v", cfg.Linux.Depends)
	}
	if err := cfg.SetDSHVersion("0.2.0-rc.1"); err != nil || cfg.DSH.Version != "0.2.0-rc.1" {
		t.Fatalf("set DSH version: version=%q err=%v", cfg.DSH.Version, err)
	}
	if err := cfg.SetDSHVersion("bad version"); err == nil {
		t.Fatal("invalid DSH version was accepted")
	}
	cfg.MacOS.Format = "dmg"
	cfg.MacOS.Sign.Enabled = true
	if err := cfg.validate(); err == nil {
		t.Fatal("signed DMG without an identity was accepted")
	}
}
