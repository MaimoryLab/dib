package build

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestTrayLauncherTemplates(t *testing.T) {
	for _, target := range []string{"windows", "darwin"} {
		name := "launcher/gui_tray_" + target + ".go.txt"
		source, err := launcherFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), name, source, parser.AllErrors); err != nil {
			t.Errorf("%s: %v", target, err)
		}
		for _, function := range []string{"nativeTrayInit", "nativeTrayClear"} {
			if !strings.Contains(string(source), "func "+function+"(") {
				t.Errorf("%s launcher is missing %s", target, function)
			}
		}
	}
}

func TestTrayOwnsWindowLifecycle(t *testing.T) {
	windows, err := launcherFiles.ReadFile("launcher/gui_tray_windows.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	darwin, err := launcherFiles.ReadFile("launcher/gui_tray_darwin.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NIF_MESSAGE", "WM_CLOSE && !dsh_tray_exiting", "WM_RBUTTONUP", "PostMessageW(dsh_tray_window, WM_CLOSE"} {
		if !strings.Contains(string(windows), want) {
			t.Errorf("Windows tray is missing %q", want)
		}
	}
	for _, want := range []string{"windowShouldClose", "orderOut:nil", "NSEventTypeRightMouseUp", "performClose:nil"} {
		if !strings.Contains(string(darwin), want) {
			t.Errorf("macOS tray is missing %q", want)
		}
	}
	for _, want := range []string{"initWithContentsOfFile", "icon.png", "icon.template = NO"} {
		if !strings.Contains(string(darwin), want) {
			t.Errorf("macOS tray icon handling is missing %q", want)
		}
	}
	webview, err := launcherFiles.ReadFile("launcher/gui_webview.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(string(webview), "uninstallNativeMenu") > strings.Index(string(webview), "nativeTrayClear") {
		t.Error("native window hooks are not removed in reverse installation order")
	}
}

func TestWindowsHeadersUseWin32TypesBeforeShellAPI(t *testing.T) {
	for _, name := range []string{"launcher/gui_tray_windows.go.txt", "launcher/gui_files_windows.go.txt"} {
		source, err := launcherFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(source)
		if strings.Index(text, "#include <windows.h>") > strings.Index(text, "#include <shellapi.h>") {
			t.Errorf("%s includes shellapi.h before windows.h", name)
		}
	}
}
