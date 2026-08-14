package build

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestTrayLauncherTemplates(t *testing.T) {
	for _, target := range []string{"windows", "darwin", "linux"} {
		name := "launcher/gui_tray_" + target + ".go.txt"
		source, err := launcherFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), name, source, parser.AllErrors); err != nil {
			t.Errorf("%s: %v", target, err)
		}
		for _, function := range []string{"nativeTraySupported", "nativeTraySet", "nativeTrayClear", "nativeTrayShow", "nativeTrayHide", "nativeTrayTerminate"} {
			if !strings.Contains(string(source), "func "+function+"(") {
				t.Errorf("%s launcher is missing %s", target, function)
			}
		}
	}
}
