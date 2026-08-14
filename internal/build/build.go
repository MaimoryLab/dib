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
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MaimoryLab/dib/internal/config"
)

//go:embed launcher/*.txt
var launcherFiles embed.FS

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
	if err := buildLauncher(ctx, cfg, target, launcherRoot); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.Output, 0o755); err != nil {
		return err
	}
	if appPath != "" {
		return writeDMG(ctx, cfg, name, dmgRoot, appPath)
	}
	if target.OS == "windows" {
		return writeZip(filepath.Join(cfg.Output, name+".zip"), work, name)
	}
	return writeTarGz(filepath.Join(cfg.Output, name+".tar.gz"), work, name)
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
	specs = append(specs, cfg.DSH.Plugins...)
	args := []string{"install", "--prefix", dst, "--omit=dev", "--package-lock=false", "--ignore-scripts", "--bin-links=false", "--os", npmOS(target.OS), "--cpu", npmArch(target.Arch)}
	args = append(args, specs...)
	cmd := exec.CommandContext(ctx, "npm", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("npm install: %w", err)
	}
	return nil
}

func buildLauncher(ctx context.Context, cfg config.Config, target config.Target, outputDir string) error {
	sourceDir, err := os.MkdirTemp("", "dib-launcher-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(sourceDir)
	files := []string{"main.go.txt", cfg.ModeFor(target) + ".go.txt"}
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
	if cfg.ModeFor(target) == "gui" {
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
	ldflags := fmt.Sprintf("-s -w -X main.host=%s -X main.port=%d -X main.dshPackage=%s", cfg.Runtime.Host, cfg.Runtime.Port, cfg.DSH.Package)
	if target.OS == "windows" && cfg.ModeFor(target) == "gui" {
		ldflags += " -H windowsgui"
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-mod=mod", "-trimpath", "-ldflags", ldflags, "-o", output, ".")
	cmd.Dir = sourceDir
	cmd.Env = append(os.Environ(), "GOOS="+target.OS, "GOARCH="+target.Arch)
	if cfg.ModeFor(target) == "gui" {
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
	cmd := exec.CommandContext(ctx, "hdiutil", "create", "-volname", cfg.MacOS.VolumeName, "-srcfolder", dmgRoot, "-ov", "-format", "UDZO", output)
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
