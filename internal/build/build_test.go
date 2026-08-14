package build

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MaimoryLab/dib/internal/config"
)

func TestExtractZipRejectsPathTraversal(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "bad.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("root/../../escaped")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("bad")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractZip(archivePath, t.TempDir(), true); err == nil {
		t.Fatal("extractZip accepted a path outside the destination")
	}
}

func TestNFPMIntegration(t *testing.T) {
	if os.Getenv("DIB_NFPM_INTEGRATION") == "" {
		t.Skip("set DIB_NFPM_INTEGRATION=1 to run")
	}
	work := t.TempDir()
	root := filepath.Join(work, "root")
	if err := os.MkdirAll(filepath.Join(root, "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dshbox"), []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "runtime", "node"), []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Output: filepath.Join(work, "out"),
		DSH:    config.DSH{Version: "1.2.3-rc.1"},
		Linux: config.Linux{
			PackageName: "dsh",
			AppName:     "DeepSeek Harness",
			Maintainer:  "DSH <dsh@example.com>",
			Description: "DSH",
			Vendor:      "DeepSeek",
			License:     "MIT",
			Depends:     config.LinuxDependencies{Deb: []string{"libgtk-4-1"}, RPM: []string{"gtk4"}},
		},
	}
	if err := os.MkdirAll(cfg.Output, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"deb", "rpm"} {
		if err := writeLinuxPackage(context.Background(), cfg, config.Target{Arch: "arm64"}, "dsh-test", root, format); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(cfg.Output, "dsh-test."+format))
		if err != nil || info.Size() == 0 {
			t.Fatalf("%s package was not created: %v", format, err)
		}
	}
}

func TestNSISIntegration(t *testing.T) {
	if os.Getenv("DIB_NSIS_INTEGRATION") == "" {
		t.Skip("set DIB_NSIS_INTEGRATION=1 to run")
	}
	work := t.TempDir()
	root := filepath.Join(work, "root")
	if err := os.MkdirAll(filepath.Join(root, "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dshbox.exe"), []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "runtime", "node.exe"), []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Output:  filepath.Join(work, "out"),
		DSH:     config.DSH{Version: "1.2.3"},
		Windows: config.Windows{AppName: "DSH", Publisher: "DeepSeek", InstallScope: "user"},
	}
	if err := os.MkdirAll(cfg.Output, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeNSIS(context.Background(), cfg, config.Target{Arch: "arm64"}, "dsh-test", root); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(cfg.Output, "dsh-test-installer.exe"))
	if err != nil || info.Size() == 0 {
		t.Fatalf("NSIS installer was not created: %v", err)
	}
}

func TestPackageMetadata(t *testing.T) {
	cfg := config.Config{
		DSH:     config.DSH{Version: "1.2.3"},
		Windows: config.Windows{AppName: "DSH", Publisher: "DeepSeek", InstallScope: "machine"},
		Linux: config.Linux{
			PackageName: "dsh",
			Maintainer:  "DSH <dsh@example.com>",
			Description: "DSH",
			Vendor:      "DeepSeek",
			License:     "MIT",
			Depends:     config.LinuxDependencies{Deb: []string{"deb-webview"}, RPM: []string{"rpm-webview"}},
		},
	}
	script := nsisScript(cfg, config.Target{Arch: "arm64"})
	for _, want := range []string{`RequestExecutionLevel admin`, `${IsNativeARM64}`, `WriteRegStr HKLM`} {
		if !strings.Contains(script, want) {
			t.Fatalf("NSIS script does not contain %q", want)
		}
	}

	nfpm := newNFPMConfig(cfg, config.Target{Arch: "arm64"}, "/tmp/root", "/tmp/dsh.desktop")
	if nfpm.Arch != "arm64" || nfpm.Contents[0].Type != "tree" {
		t.Fatalf("unexpected nFPM config: %+v", nfpm)
	}
	if got := nfpm.Overrides["rpm"].Depends; len(got) != 1 || got[0] != "rpm-webview" {
		t.Fatalf("RPM dependencies = %v", got)
	}
}

func TestLinuxGUILauncherUsesGTK4(t *testing.T) {
	source, err := launcherFiles.ReadFile("launcher/gui_linux.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "#cgo pkg-config: gtk4 webkitgtk-6.0") {
		t.Fatal("Linux GUI launcher does not use GTK4 and WebKitGTK 6.0")
	}
	for _, old := range []string{"gtk+-3.0", "webkit2gtk-4.0"} {
		if strings.Contains(text, old) {
			t.Fatalf("Linux GUI launcher still references %q", old)
		}
	}
}

func TestLinuxGUILauncherAllowsClipboard(t *testing.T) {
	source, err := launcherFiles.ReadFile("launcher/gui_linux.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "webkit_settings_set_javascript_can_access_clipboard(settings, TRUE)") {
		t.Fatal("Linux GUI launcher does not enable WebKit clipboard access")
	}
}

func TestDarwinGUILauncherHasEditMenu(t *testing.T) {
	source, err := launcherFiles.ReadFile("launcher/gui_menu_darwin.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, want := range []string{"dsh_menu_add_edit", "@selector(copy:)", "@selector(paste:)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("macOS GUI launcher does not provide %q", want)
		}
	}
}

func TestGUIFileCapabilitySources(t *testing.T) {
	common, err := launcherFiles.ReadFile("launcher/gui_files.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"dshbox:files-dropped", "text/uri-list", "validateExternalURL"} {
		if !strings.Contains(string(common), want) {
			t.Fatalf("file capability source does not contain %q", want)
		}
	}
	for _, target := range []string{"windows", "darwin", "linux"} {
		source, err := launcherFiles.ReadFile("launcher/gui_files_" + target + ".go.txt")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"nativeChooseFiles", "nativeOpenExternal"} {
			if !strings.Contains(string(source), want) {
				t.Fatalf("%s file capability source does not contain %q", target, want)
			}
		}
	}
}

func TestLinuxOptionalDesktopBridges(t *testing.T) {
	source, err := launcherFiles.ReadFile("launcher/gui_linux.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, want := range []string{"LookPath(\"xdg-open\")", "LookPath(\"notify-send\")", "dshboxOpenExternal", "dshboxNotify"} {
		if !strings.Contains(text, want) {
			t.Fatalf("Linux GUI launcher does not contain %q", want)
		}
	}
}
