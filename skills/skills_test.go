package skills

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSkillHelpers runs each skill's starter helper Python tests, so `go test ./...` covers the
// helpers the skills ship. The test reads every Python file first: Go's test cache keys on the
// files a test opens, and the interpreter's reads are invisible to it.
func TestSkillHelpers(t *testing.T) {
	python := ""
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			python = path
			break
		}
	}
	if python == "" {
		t.Fatal("the skills' helper tests need python3 on PATH")
	}
	for _, skill := range []string{"robot-navigation", "camera-look-around"} {
		t.Run(skill, func(t *testing.T) {
			lib := filepath.Join(skill, "tricks", "lib")
			scripts, err := filepath.Glob(filepath.Join(skill, "tricks", "*", "*.py"))
			if err != nil || len(scripts) == 0 {
				t.Fatalf("no trick scripts found: %v", err)
			}
			for _, script := range scripts {
				if _, err := os.ReadFile(script); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(python, "-I", "-B", "-m", "unittest", "discover", "-s", lib, "-t", lib, "-p", "test_*.py")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("python helper tests failed: %v\n%s", err, out)
			}
			t.Logf("%s", out)
		})
	}
}

// skillCommands maps each shipped skill to commands its SKILL.md must name, so a rename in the CLI
// fails here until the skill follows.
var skillCommands = map[string][]string{
	"robot-navigation": {"port session hold", "port session release", "port message go-to-point"},
	"camera-look-around": {"port session hold", "--shared", "port message move-ptz", "--seconds 60", "port message stop-ptz", "port message step-ptz", "port message snapshot", "port session release",
		"hold_lease_required", "shared_hold_no_talkback", "session_expired", "session_closed", "LEFT_END", "BOTTOM_END", "panorama", "look_at",
		"gesture/run.py", "patrol/run.py", "follow/run.py"},
}

// TestSkillsAreRegistered checks every skill directory has a SKILL.md whose front matter names the
// directory and describes the skill, and that the skill names the commands it relies on.
func TestSkillsAreRegistered(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, known := skillCommands[entry.Name()]; !known {
			t.Errorf("skills/%s is not registered in skillCommands", entry.Name())
		}
	}
	for name, commands := range skillCommands {
		data, err := os.ReadFile(filepath.Join(name, "SKILL.md"))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		text := string(data)
		head, _, found := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---\n")
		if !strings.HasPrefix(text, "---\n") || !found {
			t.Errorf("%s: SKILL.md has no front matter", name)
			continue
		}
		if !strings.Contains(head, "\nname: "+name+"\n") && !strings.HasPrefix(head, "name: "+name+"\n") {
			t.Errorf("%s: front matter does not name the skill", name)
		}
		if !strings.Contains(head, "description: ") {
			t.Errorf("%s: front matter has no description", name)
		}
		for _, command := range commands {
			if !strings.Contains(text, command) {
				t.Errorf("%s: SKILL.md does not mention %q", name, command)
			}
		}
	}
}
