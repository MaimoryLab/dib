package build

import (
	"go/parser"
	"go/token"
	"testing"
)

func TestNotificationLauncherSyntax(t *testing.T) {
	for _, target := range []string{"windows", "darwin", "linux"} {
		name := "launcher/gui_notify_" + target + ".go.txt"
		source, err := launcherFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), name, source, parser.AllErrors); err != nil {
			t.Errorf("%s: %v", target, err)
		}
	}
	action, err := launcherFiles.ReadFile("launcher/gui_notify_linux_action.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "launcher/gui_notify_linux_action.go.txt", action, parser.AllErrors); err != nil {
		t.Errorf("linux action: %v", err)
	}
}
