package skills

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRobotNavigationHelper runs the starter helper's Python tests, so `go test ./...` covers the
// helper the navigation skill ships. The test reads every Python file first: Go's test cache keys
// on the files a test opens, and the interpreter's reads are invisible to it.
func TestRobotNavigationHelper(t *testing.T) {
	lib := filepath.Join("robot-navigation", "tricks", "lib")
	scripts, err := filepath.Glob(filepath.Join("robot-navigation", "tricks", "*", "*.py"))
	if err != nil || len(scripts) == 0 {
		t.Fatalf("no trick scripts found: %v", err)
	}
	for _, script := range scripts {
		if _, err := os.ReadFile(script); err != nil {
			t.Fatal(err)
		}
	}
	python := ""
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			python = path
			break
		}
	}
	if python == "" {
		t.Fatal("the navigation skill's helper tests need python3 on PATH")
	}
	cmd := exec.Command(python, "-I", "-B", "-m", "unittest", "discover", "-s", lib, "-t", lib, "-p", "test_*.py")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python helper tests failed: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
