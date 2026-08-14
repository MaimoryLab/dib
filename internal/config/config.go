package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

var (
	nodeVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	dshVersionPattern  = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]*$`)
	packagePattern     = regexp.MustCompile(`^(?:@[0-9A-Za-z._-]+/)?[0-9A-Za-z._-]+$`)
	bundleIDPattern    = regexp.MustCompile(`^[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)+$`)
)

type Config struct {
	Version int      `yaml:"version"`
	Output  string   `yaml:"output"`
	Cache   string   `yaml:"cache"`
	Node    Node     `yaml:"node"`
	DSH     DSH      `yaml:"dsh"`
	MacOS   MacOS    `yaml:"macos,omitempty"`
	Runtime Runtime  `yaml:"runtime"`
	Targets []Target `yaml:"targets"`
}

type Node struct {
	Version string `yaml:"version"`
	BaseURL string `yaml:"base_url,omitempty"`
}

type DSH struct {
	Package string   `yaml:"package"`
	Version string   `yaml:"version"`
	Plugins []string `yaml:"plugins"`
}

type MacOS struct {
	Format     string  `yaml:"format,omitempty"`
	AppName    string  `yaml:"app_name,omitempty"`
	BundleID   string  `yaml:"bundle_id,omitempty"`
	VolumeName string  `yaml:"volume_name,omitempty"`
	Sign       Signing `yaml:"sign,omitempty"`
}

type Signing struct {
	Enabled  bool   `yaml:"enabled"`
	Identity string `yaml:"identity,omitempty"`
}

type Runtime struct {
	Mode string `yaml:"mode"`
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type Target struct {
	OS   string `yaml:"os"`
	Arch string `yaml:"arch"`
	Mode string `yaml:"mode,omitempty"`
	CC   string `yaml:"cc,omitempty"`
	CXX  string `yaml:"cxx,omitempty"`
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	var cfg Config
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Config{}, fmt.Errorf("resolve config directory: %w", err)
	}
	cfg.Output = resolvePath(base, cfg.Output)
	cfg.Cache = resolvePath(base, cfg.Cache)
	return cfg, nil
}

func (cfg *Config) setDefaults() {
	if cfg.MacOS.Format == "" {
		cfg.MacOS.Format = "tar.gz"
	}
	if cfg.MacOS.AppName == "" {
		cfg.MacOS.AppName = "DeepSeek Harness"
	}
	if cfg.MacOS.BundleID == "" {
		cfg.MacOS.BundleID = "ai.deepseek.dsh"
	}
	if cfg.MacOS.VolumeName == "" {
		cfg.MacOS.VolumeName = cfg.MacOS.AppName
	}
}

func (cfg Config) ModeFor(target Target) string {
	if target.Mode != "" {
		return target.Mode
	}
	return cfg.Runtime.Mode
}

func (cfg Config) validate() error {
	if cfg.Version != 1 {
		return fmt.Errorf("version must be 1, got %d", cfg.Version)
	}
	if cfg.Output == "" || cfg.Cache == "" {
		return errors.New("output and cache are required")
	}
	if cfg.Node.Version == "" || cfg.DSH.Package == "" || cfg.DSH.Version == "" {
		return errors.New("node.version, dsh.package, and dsh.version are required")
	}
	if !nodeVersionPattern.MatchString(cfg.Node.Version) {
		return errors.New("node.version must be a full numeric version such as 22.23.2")
	}
	if !packagePattern.MatchString(cfg.DSH.Package) {
		return errors.New("dsh.package must be an npm package name")
	}
	if !dshVersionPattern.MatchString(cfg.DSH.Version) {
		return errors.New("dsh.version contains invalid characters")
	}
	if cfg.MacOS.Format != "tar.gz" && cfg.MacOS.Format != "dmg" {
		return errors.New("macos.format must be tar.gz or dmg")
	}
	if cfg.MacOS.Sign.Enabled {
		if cfg.MacOS.Format != "dmg" {
			return errors.New("macos.sign requires macos.format: dmg")
		}
		if strings.TrimSpace(cfg.MacOS.Sign.Identity) == "" {
			return errors.New("macos.sign.identity is required when signing is enabled")
		}
	}
	if strings.TrimSpace(cfg.MacOS.AppName) == "" || strings.TrimSpace(cfg.MacOS.VolumeName) == "" {
		return errors.New("macos.app_name and macos.volume_name cannot be blank")
	}
	if !bundleIDPattern.MatchString(cfg.MacOS.BundleID) {
		return errors.New("macos.bundle_id contains invalid characters")
	}
	if cfg.Runtime.Host != "127.0.0.1" {
		return errors.New("runtime.host must be 127.0.0.1")
	}
	if cfg.Runtime.Port < 1 || cfg.Runtime.Port > 65535 {
		return errors.New("runtime.port must be between 1 and 65535")
	}
	if len(cfg.Targets) == 0 {
		return errors.New("at least one target is required")
	}
	seen := make(map[string]bool, len(cfg.Targets))
	for _, target := range cfg.Targets {
		key := target.OS + "/" + target.Arch
		if !supportedTarget(target) {
			return fmt.Errorf("unsupported target %s", key)
		}
		if seen[key] {
			return fmt.Errorf("duplicate target %s", key)
		}
		seen[key] = true
		if err := validateMode(cfg.ModeFor(target)); err != nil {
			return fmt.Errorf("target %s: %w", key, err)
		}
	}
	for _, plugin := range cfg.DSH.Plugins {
		if strings.TrimSpace(plugin) == "" {
			return errors.New("dsh.plugins cannot contain an empty package spec")
		}
	}
	return nil
}

func supportedTarget(target Target) bool {
	return (target.OS == "windows" || target.OS == "darwin" || target.OS == "linux") &&
		(target.Arch == "amd64" || target.Arch == "arm64")
}

func validateMode(mode string) error {
	if mode != "gui" && mode != "serve" {
		return fmt.Errorf("mode must be gui or serve, got %q", mode)
	}
	return nil
}

func resolvePath(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}
