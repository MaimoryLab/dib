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
node: {version: 22.23.2}
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
}
