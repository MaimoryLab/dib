package build

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MaimoryLab/dib/internal/config"
	"go.yaml.in/yaml/v3"
)

//go:embed launcher/*.txt
var launcherFiles embed.FS

const desktopPluginName = "@maimorylab/dsh-desktop"

const appCacheVersion = "2"

func Run(ctx context.Context, cfg config.Config) error {
	for _, target := range cfg.Targets {
		fmt.Printf("building %s/%s (%s)\n", target.OS, target.Arch, cfg.ModeFor(target))
		if err := buildTarget(ctx, cfg, target); err != nil {
			return fmt.Errorf("build %s/%s: %w", target.OS, target.Arch, err)
		}
	}
	return nil
}

func buildTarget(ctx context.Context, cfg config.Config, target config.Target) error {
	work, err := os.MkdirTemp("", "dib-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	name := fmt.Sprintf("dsh-%s-%s-%s", cfg.DSH.Version, target.OS, target.Arch)
	root := filepath.Join(work, name)
	payloadRoot := root
	launcherRoot := root
	appPath := ""
	dmgRoot := ""
	if target.OS == "darwin" && cfg.MacOS.Format == "dmg" {
		dmgRoot = filepath.Join(work, "dmg")
		appPath = filepath.Join(dmgRoot, cfg.MacOS.AppName+".app")
		payloadRoot = filepath.Join(appPath, "Contents", "Resources")
		launcherRoot = filepath.Join(appPath, "Contents", "MacOS")
	}
	if err := os.MkdirAll(filepath.Join(payloadRoot, "runtime", "app"), 0o755); err != nil {
		return err
	}
	if err := installNode(ctx, cfg, target, filepath.Join(payloadRoot, "runtime", "node")); err != nil {
		return err
	}
	if err := installDSH(ctx, cfg, target, filepath.Join(payloadRoot, "runtime", "app")); err != nil {
		return err
	}
	// npm installation alone does not add packages to DSH's Loader tree.
	if len(cfg.DSH.Plugins) > 0 {
		if err := writePluginPatch(filepath.Join(payloadRoot, "runtime", "app"), cfg.DSH.Plugins); err != nil {
			return err
		}
	}
	if err := buildLauncher(ctx, cfg, target, launcherRoot); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.Output, 0o755); err != nil {
		return err
	}
	switch target.OS {
	case "windows":
		if cfg.Windows.Format == "nsis" {
			return writeNSIS(ctx, cfg, target, name, root)
		}
		return writeZip(filepath.Join(cfg.Output, name+".zip"), work, name)
	case "darwin":
		if appPath != "" {
			return writeDMG(ctx, cfg, name, dmgRoot, appPath)
		}
		return writeTarGz(filepath.Join(cfg.Output, name+".tar.gz"), work, name)
	case "linux":
		for _, format := range cfg.Linux.Formats {
			if format == "tar.gz" {
				if err := writeTarGz(filepath.Join(cfg.Output, name+".tar.gz"), work, name); err != nil {
					return err
				}
				continue
			}
			if err := writeLinuxPackage(ctx, cfg, target, name, root, format); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("unsupported target OS %q", target.OS)
}

func installNode(ctx context.Context, cfg config.Config, target config.Target, dst string) error {
	ext := "tar.gz"
	platform := target.OS
	if target.OS == "windows" {
		ext = "zip"
		platform = "win"
	}
	arch := npmArch(target.Arch)
	filename := fmt.Sprintf("node-v%s-%s-%s.%s", cfg.Node.Version, platform, arch, ext)
	baseURL := strings.TrimRight(cfg.Node.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://nodejs.org/dist"
	}
	baseURL += "/v" + cfg.Node.Version
	archive, err := cachedDownload(ctx, cfg.Cache, baseURL+"/"+filename, baseURL+"/SHASUMS256.txt", filename)
	if err != nil {
		return fmt.Errorf("download Node.js: %w", err)
	}
	if ext == "zip" {
		return extractZip(archive, dst, true)
	}
	return extractTarGz(archive, dst, true)
}

func installDSH(ctx context.Context, cfg config.Config, target config.Target, dst string) error {
	specs := []string{cfg.DSH.Package + "@" + cfg.DSH.Version}
	plugins, packDirs, err := packLocalPluginSpecs(ctx, cfg.DSH.Plugins)
	if err != nil {
		for _, dir := range packDirs {
			_ = os.RemoveAll(dir)
		}
		return err
	}
	defer func() {
		for _, dir := range packDirs {
			_ = os.RemoveAll(dir)
		}
	}()
	specs = append(specs, plugins...)
	cacheSpecs := append([]string{cfg.DSH.Package + "@" + cfg.DSH.Version}, cfg.DSH.Plugins...)
	cacheKey, err := appCacheKey(cfg.Node.Version, target, cacheSpecs)
	if err != nil {
		return err
	}
	cacheDir := filepath.Join(cfg.Cache, "app", cacheKey)
	if _, err := os.Stat(filepath.Join(cacheDir, ".ready")); err == nil {
		fmt.Println("using cached DSH runtime")
		if err := os.CopyFS(dst, os.DirFS(filepath.Join(cacheDir, "root"))); err != nil {
			return err
		}
		return exposePluginDependencies(dst, cfg.DSH.Package, cfg.DSH.Plugins)
	}
	args := []string{"install", "--prefix", dst, "--cache", filepath.Join(cfg.Cache, "npm"), "--prefer-offline", "--omit=dev", "--package-lock=false", "--ignore-scripts", "--bin-links=false", "--os", npmOS(target.OS), "--cpu", npmArch(target.Arch)}
	args = append(args, specs...)
	cmd := exec.CommandContext(ctx, "npm", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("npm install: %w", err)
	}
	if err := exposePluginDependencies(dst, cfg.DSH.Package, cfg.DSH.Plugins); err != nil {
		return err
	}
	return storeAppCache(cacheDir, dst)
}

func appCacheKey(nodeVersion string, target config.Target, specs []string) (string, error) {
	hash := sha256.New()
	for _, value := range append([]string{appCacheVersion, nodeVersion, target.OS, target.Arch}, specs...) {
		if dir, ok := localPluginDir(value); ok {
			var err error
			value, err = directorySHA256(dir)
			if err != nil {
				return "", err
			}
		} else if info, err := os.Stat(value); err == nil && info.Mode().IsRegular() {
			value, err = fileSHA256(value)
			if err != nil {
				return "", err
			}
		}
		fmt.Fprintf(hash, "%d:%s", len(value), value)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func directorySHA256(root string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		value := ""
		if entry.Type().IsRegular() {
			value, err = fileSHA256(path)
		} else if entry.Type()&os.ModeSymlink != 0 {
			value, err = os.Readlink(path)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%d:%s%d:%s", len(rel), filepath.ToSlash(rel), len(value), value)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func storeAppCache(cacheDir, source string) error {
	parent := filepath.Dir(cacheDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, filepath.Base(cacheDir)+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.CopyFS(filepath.Join(tmp, "root"), os.DirFS(source)); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, ".ready"), nil, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, cacheDir); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	return nil
}

func packLocalPluginSpecs(ctx context.Context, specs []string) ([]string, []string, error) {
	result := append([]string(nil), specs...)
	var packDirs []string
	for index, spec := range specs {
		dir, ok := localPluginDir(spec)
		if !ok {
			continue
		}
		packDir, err := os.MkdirTemp("", "dib-plugin-pack-")
		if err != nil {
			return nil, packDirs, err
		}
		packDirs = append(packDirs, packDir)
		cmd := exec.CommandContext(ctx, "npm", "pack", "--ignore-scripts", "--pack-destination", packDir)
		cmd.Dir = dir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return nil, packDirs, fmt.Errorf("npm pack %s: %w", spec, err)
		}
		entries, err := os.ReadDir(packDir)
		if err != nil {
			return nil, packDirs, err
		}
		var archive string
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".tgz") {
				if archive != "" {
					return nil, packDirs, fmt.Errorf("npm pack %s produced multiple archives", spec)
				}
				archive = entry.Name()
			}
		}
		if archive == "" {
			return nil, packDirs, fmt.Errorf("npm pack %s produced no archive", spec)
		}
		result[index] = filepath.Join(packDir, archive)
	}
	return result, packDirs, nil
}

func localPluginDir(spec string) (string, bool) {
	value := strings.TrimSpace(spec)
	if after, ok := strings.CutPrefix(value, "file:"); ok {
		value = after
	} else if !strings.HasPrefix(value, ".") && !filepath.IsAbs(value) && !strings.ContainsAny(value, `/\\`) {
		return "", false
	}
	if value == "" {
		return "", false
	}
	dir, err := filepath.Abs(value)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(dir)
	return dir, err == nil && info.IsDir()
}

func exposePluginDependencies(dst, packageName string, specs []string) error {
	anchor := filepath.Join(dst, "node_modules", filepath.FromSlash(packageName), "package.json")
	raw, err := os.ReadFile(anchor)
	if err != nil {
		return fmt.Errorf("read DSH package manifest: %w", err)
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("parse DSH package manifest: %w", err)
	}
	dependencies := make(map[string]string)
	if rawDeps, ok := manifest["dependencies"]; ok {
		if err := json.Unmarshal(rawDeps, &dependencies); err != nil {
			return fmt.Errorf("parse DSH dependencies: %w", err)
		}
	}
	changed := false
	for _, spec := range specs {
		name, ok := pluginPackageName(spec)
		if !ok {
			continue
		}
		pluginManifestPath := filepath.Join(dst, "node_modules", filepath.FromSlash(name), "package.json")
		pluginRaw, err := os.ReadFile(pluginManifestPath)
		if err != nil {
			return fmt.Errorf("read installed plugin %s: %w", name, err)
		}
		var pluginManifest struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(pluginRaw, &pluginManifest); err != nil {
			return fmt.Errorf("parse installed plugin %s: %w", name, err)
		}
		if pluginManifest.Version == "" {
			return fmt.Errorf("installed plugin %s has no version", name)
		}
		if dependencies[name] == pluginManifest.Version {
			continue
		}
		dependencies[name] = pluginManifest.Version
		changed = true
	}
	if !changed {
		return nil
	}
	encoded, err := json.Marshal(dependencies)
	if err != nil {
		return err
	}
	manifest["dependencies"] = encoded
	encoded, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(anchor, encoded, 0o644); err != nil {
		return fmt.Errorf("update DSH package manifest: %w", err)
	}
	return nil
}

func pluginPackageName(spec string) (string, bool) {
	if dir, ok := localPluginDir(spec); ok {
		raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
		if err != nil {
			return "", false
		}
		var manifest struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Name == "" {
			return "", false
		}
		return manifest.Name, true
	}
	name := strings.TrimSpace(spec)
	name = strings.TrimPrefix(name, "npm:")
	if strings.HasPrefix(name, "@") {
		slash := strings.IndexByte(name, '/')
		if slash < 2 {
			return "", false
		}
		suffix := name[slash+1:]
		if at := strings.IndexByte(suffix, '@'); at >= 0 {
			suffix = suffix[:at]
		}
		if suffix == "" || strings.ContainsAny(suffix, "/:") {
			return "", false
		}
		return name[:slash+1] + suffix, true
	}
	if at := strings.IndexByte(name, '@'); at >= 0 {
		name = name[:at]
	}
	if name == "" || strings.ContainsAny(name, "/:") {
		return "", false
	}
	return name, true
}

type pluginPatch struct {
	Insert []pluginPatchEntry `yaml:"insert"`
}

type pluginPatchEntry struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
}

func writePluginPatch(dst string, specs []string) error {
	entries := make([]pluginPatchEntry, 0, len(specs))
	seen := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		name, ok := pluginPackageName(spec)
		if !ok {
			return fmt.Errorf("resolve plugin %q for Cordis patch: expected an npm package spec or local package directory", spec)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		id := name
		if name == desktopPluginName {
			id = "dsh-desktop"
		}
		entries = append(entries, pluginPatchEntry{ID: id, Name: name})
	}
	if len(entries) == 0 {
		return nil
	}
	data, err := yaml.Marshal([]pluginPatch{{Insert: entries}})
	if err != nil {
		return fmt.Errorf("encode Cordis plugin patch: %w", err)
	}
	return os.WriteFile(filepath.Join(dst, "dsh-desktop.patch.yml"), data, 0o644)
}

func buildLauncher(ctx context.Context, cfg config.Config, target config.Target, outputDir string) error {
	sourceDir, err := os.MkdirTemp("", "dib-launcher-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(sourceDir)
	mode := cfg.ModeFor(target)
	files := []string{"main.go.txt", mode + ".go.txt"}
	if mode == "gui" {
		backend := "gui_webview.go.txt"
		if target.OS == "linux" {
			backend = "gui_linux.go.txt"
		}
		files = append(files, backend, "gui_files.go.txt", "gui_files_"+target.OS+".go.txt", "gui_notify_"+target.OS+".go.txt")
		if target.OS == "linux" {
			files = append(files, "gui_files_linux_action.go.txt", "gui_notify_linux_action.go.txt")
		}
		if target.OS == "windows" || target.OS == "darwin" {
			files = append(files, "gui_menu_"+target.OS+".go.txt", "gui_menu_action.go.txt")
		}
		if target.OS != "linux" {
			files = append(files, "gui_tray_"+target.OS+".go.txt")
		}
	}
	for _, name := range files {
		data, err := launcherFiles.ReadFile("launcher/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(sourceDir, strings.TrimSuffix(name, ".txt")), data, 0o644); err != nil {
			return err
		}
	}
	mod := "module dib-launcher\n\ngo 1.23\n"
	if mode == "gui" && target.OS != "linux" {
		mod += "\nrequire github.com/webview/webview_go v0.0.0-20240831120633-6173450d4dd6\n"
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "go.mod"), []byte(mod), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	output := filepath.Join(outputDir, "dshbox")
	if target.OS == "windows" {
		output += ".exe"
	}
	ldflags := fmt.Sprintf("-s -w -X main.host=%s -X main.port=%d -X main.dshPackage=%s -X main.dshVersion=%s", cfg.Runtime.Host, cfg.Runtime.Port, cfg.DSH.Package, cfg.DSH.Version)
	if target.OS == "windows" && mode == "gui" {
		ldflags += " -H windowsgui"
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-mod=mod", "-trimpath", "-ldflags", ldflags, "-o", output, ".")
	cmd.Dir = sourceDir
	cmd.Env = append(os.Environ(), "GOOS="+target.OS, "GOARCH="+target.Arch)
	if mode == "gui" {
		cmd.Env = append(cmd.Env, "CGO_ENABLED=1")
		if target.CC != "" {
			cmd.Env = append(cmd.Env, "CC="+target.CC)
		}
		if target.CXX != "" {
			cmd.Env = append(cmd.Env, "CXX="+target.CXX)
		}
	} else {
		cmd.Env = append(cmd.Env, "CGO_ENABLED=0")
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("compile launcher (gui targets require a native C/C++ cross-toolchain): %w", err)
	}
	return nil
}

func writeDMG(ctx context.Context, cfg config.Config, name, dmgRoot, appPath string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("creating a DMG requires macOS")
	}
	if err := writeInfoPlist(cfg, appPath); err != nil {
		return err
	}
	if err := os.Symlink("/Applications", filepath.Join(dmgRoot, "Applications")); err != nil {
		return err
	}
	if cfg.MacOS.Sign.Enabled {
		if err := codesign(ctx, cfg.MacOS.Sign.Identity, appPath, true); err != nil {
			return err
		}
	}
	output := filepath.Join(cfg.Output, name+".dmg")
	cmd := exec.CommandContext(ctx, "hdiutil", "create", "-volname", cfg.MacOS.VolumeName, "-srcfolder", dmgRoot, "-fs", "HFS+", "-nospotlight", "-ov", "-format", "ULMO", output)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create DMG: %w", err)
	}
	if cfg.MacOS.Sign.Enabled {
		if err := codesign(ctx, cfg.MacOS.Sign.Identity, output, false); err != nil {
			return err
		}
	}
	return nil
}

func writeInfoPlist(cfg config.Config, appPath string) error {
	escape := func(value string) string {
		var result strings.Builder
		_ = xml.EscapeText(&result, []byte(value))
		return result.String()
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDisplayName</key><string>%s</string>
  <key>CFBundleExecutable</key><string>dshbox</string>
  <key>CFBundleIdentifier</key><string>%s</string>
  <key>CFBundleName</key><string>%s</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
`, escape(cfg.MacOS.AppName), escape(cfg.MacOS.BundleID), escape(cfg.MacOS.AppName))
	return os.WriteFile(filepath.Join(appPath, "Contents", "Info.plist"), []byte(plist), 0o644)
}

func codesign(ctx context.Context, identity, path string, deep bool) error {
	args := []string{"--force", "--sign", identity}
	if deep {
		args = append(args, "--deep")
	}
	args = append(args, path)
	cmd := exec.CommandContext(ctx, "codesign", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sign %s: %w", filepath.Base(path), err)
	}
	verifyArgs := []string{"--verify", "--strict"}
	if deep {
		verifyArgs = append(verifyArgs, "--deep")
	}
	verifyArgs = append(verifyArgs, path)
	if output, err := exec.CommandContext(ctx, "codesign", verifyArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("verify signature for %s: %w: %s", filepath.Base(path), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func writeNSIS(ctx context.Context, cfg config.Config, target config.Target, name, root string) error {
	if _, err := exec.LookPath("makensis"); err != nil {
		return errors.New("makensis is required for windows.format: nsis")
	}
	script := nsisScript(cfg, target)
	work := filepath.Dir(root)
	scriptPath := filepath.Join(work, "installer.nsi")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		return err
	}
	output := filepath.Join(cfg.Output, name+"-installer.exe")
	cmd := exec.CommandContext(ctx, "makensis", "-WX", "-DPAYLOAD="+root, "-DOUTFILE="+output, scriptPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create NSIS installer: %w", err)
	}
	return nil
}

func nsisScript(cfg config.Config, target config.Target) string {
	scope := "SetShellVarContext current"
	registry := "HKCU"
	installDir := `$LOCALAPPDATA\Programs\${PRODUCT}`
	requestLevel := "user"
	if cfg.Windows.InstallScope == "machine" {
		scope = "SetShellVarContext all"
		registry = "HKLM"
		installDir = `$PROGRAMFILES64\${PUBLISHER}\${PRODUCT}`
		requestLevel = "admin"
	}
	archCheck := `${IfNot} ${IsNativeAMD64}`
	if target.Arch == "arm64" {
		archCheck = `${IfNot} ${IsNativeARM64}`
	}
	return fmt.Sprintf(`Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "WinVer.nsh"
!include "x64.nsh"

!define PRODUCT "%s"
!define PUBLISHER "%s"
!define VERSION "%s"
!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\DSH"

Name "${PRODUCT}"
OutFile "${OUTFILE}"
InstallDir "%s"
RequestExecutionLevel %s
SetCompressor /SOLID lzma
ManifestDPIAware true

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Function .onInit
  ${IfNot} ${AtLeastWin10}
    MessageBox MB_OK|MB_ICONSTOP "${PRODUCT} requires Windows 10 or later."
    Abort
  ${EndIf}
  %s
    MessageBox MB_OK|MB_ICONSTOP "This installer requires Windows %s."
    Abort
  ${EndIf}
  SetRegView 64
  ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  ReadRegStr $1 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  ${If} $0 == ""
  ${AndIf} $1 == ""
    MessageBox MB_OK|MB_ICONSTOP "${PRODUCT} requires the Microsoft Edge WebView2 Runtime."
    Abort
  ${EndIf}
FunctionEnd

Section "Install"
  %s
  SetRegView 64
  SetOutPath "$INSTDIR"
  File /r "${PAYLOAD}\*"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateDirectory "$SMPROGRAMS\${PRODUCT}"
  CreateShortcut "$SMPROGRAMS\${PRODUCT}\${PRODUCT}.lnk" "$INSTDIR\dshbox.exe"
  CreateShortcut "$DESKTOP\${PRODUCT}.lnk" "$INSTDIR\dshbox.exe"
  WriteRegStr %s "${UNINSTALL_KEY}" "DisplayName" "${PRODUCT}"
  WriteRegStr %s "${UNINSTALL_KEY}" "Publisher" "${PUBLISHER}"
  WriteRegStr %s "${UNINSTALL_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr %s "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\dshbox.exe"
  WriteRegStr %s "${UNINSTALL_KEY}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
  WriteRegStr %s "${UNINSTALL_KEY}" "QuietUninstallString" "$\"$INSTDIR\uninstall.exe$\" /S"
SectionEnd

Section "Uninstall"
  %s
  SetRegView 64
  Delete "$DESKTOP\${PRODUCT}.lnk"
  RMDir /r "$SMPROGRAMS\${PRODUCT}"
  DeleteRegKey %s "${UNINSTALL_KEY}"
  RMDir /r "$INSTDIR"
SectionEnd
`, cfg.Windows.AppName, cfg.Windows.Publisher, cfg.DSH.Version, installDir, requestLevel, archCheck, target.Arch, scope, registry, registry, registry, registry, registry, registry, scope, registry)
}

type nfpmConfig struct {
	Name        string                   `yaml:"name"`
	Arch        string                   `yaml:"arch"`
	Platform    string                   `yaml:"platform"`
	Version     string                   `yaml:"version"`
	Release     string                   `yaml:"release"`
	Section     string                   `yaml:"section"`
	Priority    string                   `yaml:"priority"`
	Maintainer  string                   `yaml:"maintainer"`
	Description string                   `yaml:"description"`
	Vendor      string                   `yaml:"vendor"`
	Homepage    string                   `yaml:"homepage,omitempty"`
	License     string                   `yaml:"license"`
	Depends     []string                 `yaml:"depends,omitempty"`
	Overrides   map[string]nfpmOverrides `yaml:"overrides,omitempty"`
	Contents    []nfpmContent            `yaml:"contents"`
}

type nfpmOverrides struct {
	Depends []string `yaml:"depends,omitempty"`
}

type nfpmContent struct {
	Source string `yaml:"src"`
	Dest   string `yaml:"dst"`
	Type   string `yaml:"type,omitempty"`
}

func writeLinuxPackage(ctx context.Context, cfg config.Config, target config.Target, name, root, format string) error {
	if _, err := exec.LookPath("nfpm"); err != nil {
		return errors.New("nfpm is required for linux deb/rpm packages")
	}
	work := filepath.Dir(root)
	desktopPath := filepath.Join(work, cfg.Linux.PackageName+".desktop")
	terminal := "false"
	if cfg.ModeFor(target) == "serve" {
		terminal = "true"
	}
	desktop := fmt.Sprintf("[Desktop Entry]\nVersion=1.0\nName=%s\nExec=/opt/%s/dshbox\nTerminal=%s\nType=Application\nCategories=Development;\n", cfg.Linux.AppName, cfg.Linux.PackageName, terminal)
	if err := os.WriteFile(desktopPath, []byte(desktop), 0o644); err != nil {
		return err
	}
	nfpm := newNFPMConfig(cfg, target, root, desktopPath)
	data, err := yaml.Marshal(nfpm)
	if err != nil {
		return err
	}
	configPath := filepath.Join(work, "nfpm.yaml")
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return err
	}
	output := filepath.Join(cfg.Output, name+"."+format)
	cmd := exec.CommandContext(ctx, "nfpm", "package", "--config", configPath, "--packager", format, "--target", output)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create %s package: %w", format, err)
	}
	return nil
}

func newNFPMConfig(cfg config.Config, target config.Target, root, desktopPath string) nfpmConfig {
	installRoot := "/opt/" + cfg.Linux.PackageName
	return nfpmConfig{
		Name:        cfg.Linux.PackageName,
		Arch:        target.Arch,
		Platform:    "linux",
		Version:     cfg.DSH.Version,
		Release:     "1",
		Section:     "devel",
		Priority:    "optional",
		Maintainer:  cfg.Linux.Maintainer,
		Description: cfg.Linux.Description,
		Vendor:      cfg.Linux.Vendor,
		Homepage:    cfg.Linux.Homepage,
		License:     cfg.Linux.License,
		Depends:     cfg.Linux.Depends.Deb,
		Overrides:   map[string]nfpmOverrides{"rpm": {Depends: cfg.Linux.Depends.RPM}},
		Contents: []nfpmContent{
			{Source: filepath.ToSlash(root) + "/", Dest: installRoot, Type: "tree"},
			{Source: installRoot + "/dshbox", Dest: "/usr/bin/dshbox", Type: "symlink"},
			{Source: desktopPath, Dest: "/usr/share/applications/" + cfg.Linux.PackageName + ".desktop"},
		},
	}
}

func cachedDownload(ctx context.Context, cacheDir, url, sumsURL, filename string) (string, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	want, err := checksum(ctx, sumsURL, filename)
	if err != nil {
		return "", err
	}
	path := filepath.Join(cacheDir, filename)
	if got, err := fileSHA256(path); err == nil && got == want {
		return path, nil
	}
	tmp, err := os.CreateTemp(cacheDir, filename+"-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	response, err := get(ctx, url)
	if err != nil {
		tmp.Close()
		return "", err
	}
	_, copyErr := io.Copy(tmp, response.Body)
	closeErr := errors.Join(response.Body.Close(), tmp.Close())
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", err
	}
	if got, err := fileSHA256(tmpPath); err != nil || got != want {
		return "", fmt.Errorf("checksum mismatch for %s", filename)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", err
	}
	return path, nil
}

func checksum(ctx context.Context, url, filename string) (string, error) {
	response, err := get(ctx, url)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == filename {
			if _, err := hex.DecodeString(fields[0]); err != nil || len(fields[0]) != 64 {
				return "", fmt.Errorf("invalid checksum for %s", filename)
			}
			return fields[0], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("checksum for %s not found", filename)
}

func get(ctx context.Context, url string) (*http.Response, error) {
	var lastErr error
	for attempt := range 3 {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		response, err := http.DefaultClient.Do(request)
		if err == nil && response.StatusCode == http.StatusOK {
			return response, nil
		}
		if response != nil {
			response.Body.Close()
			lastErr = fmt.Errorf("GET %s: %s", url, response.Status)
			if response.StatusCode < http.StatusInternalServerError {
				return nil, lastErr
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 200 * time.Millisecond):
		}
	}
	return nil, lastErr
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func npmOS(goos string) string {
	if goos == "windows" {
		return "win32"
	}
	return goos
}

func npmArch(goarch string) string {
	if goarch == "amd64" {
		return "x64"
	}
	return goarch
}

func extractZip(src, dst string, stripRoot bool) error {
	archive, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer archive.Close()
	for _, file := range archive.File {
		name := file.Name
		if stripRoot {
			name = stripFirst(name)
		}
		if name == "" {
			continue
		}
		if err := writeExtracted(dst, name, file.Mode(), file.FileInfo().IsDir(), file.Open); err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(src, dst string, stripRoot bool) error {
	file, err := os.Open(src)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := header.Name
		if stripRoot {
			name = stripFirst(name)
		}
		if name == "" {
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := writeExtracted(dst, name, os.FileMode(header.Mode), true, nil); err != nil {
				return err
			}
		case tar.TypeReg:
			open := func() (io.ReadCloser, error) { return io.NopCloser(reader), nil }
			if err := writeExtracted(dst, name, os.FileMode(header.Mode), false, open); err != nil {
				return err
			}
		}
	}
}

func stripFirst(path string) string {
	_, rest, found := strings.Cut(filepath.ToSlash(path), "/")
	if !found {
		return ""
	}
	return rest
}

func writeExtracted(dst, name string, mode os.FileMode, directory bool, open func() (io.ReadCloser, error)) error {
	path := filepath.Join(dst, filepath.FromSlash(name))
	rel, err := filepath.Rel(dst, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("archive path escapes destination: %q", name)
	}
	if directory {
		return os.MkdirAll(path, mode.Perm())
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	source, err := open()
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(destination, source)
	return errors.Join(copyErr, destination.Close())
}

func writeZip(path, base, root string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(file)
	err = walkArchive(filepath.Join(base, root), func(full, name string, info os.FileInfo) error {
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(filepath.Join(root, name))
		header.Method = zip.Deflate
		if info.IsDir() {
			header.Name += "/"
			_, err = writer.CreateHeader(header)
			return err
		}
		destination, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(full)
			if err != nil {
				return err
			}
			_, err = io.WriteString(destination, target)
			return err
		}
		source, err := os.Open(full)
		if err != nil {
			return err
		}
		defer source.Close()
		_, err = io.Copy(destination, source)
		return err
	})
	return errors.Join(err, writer.Close(), file.Close())
}

func writeTarGz(path, base, root string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(file)
	writer := tar.NewWriter(gz)
	err = walkArchive(filepath.Join(base, root), func(full, name string, info os.FileInfo) error {
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			var err error
			link, err = os.Readlink(full)
			if err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(filepath.Join(root, name))
		if err := writer.WriteHeader(header); err != nil || !info.Mode().IsRegular() {
			return err
		}
		source, err := os.Open(full)
		if err != nil {
			return err
		}
		defer source.Close()
		if _, err = io.Copy(writer, source); err != nil {
			return fmt.Errorf("archive %s: %w", name, err)
		}
		return nil
	})
	return errors.Join(err, writer.Close(), gz.Close(), file.Close())
}

func walkArchive(root string, visit func(full, name string, info os.FileInfo) error) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		return visit(path, name, info)
	})
}
