package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 11, 17, 21, 0, 0, 0, time.UTC)

// plainDir returns a temp directory that git cannot mistake for part of a repository.
func plainDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	return dir
}

func render(t *testing.T, dir string) string {
	t.Helper()
	out, err := BuildInstructions(context.Background(), dir, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestInstructionsContainEnvironment(t *testing.T) {
	dir := plainDir(t)
	out := render(t, dir)

	for _, want := range []string{
		"You are desk-agent",
		"Working directory: " + dir,
		"Platform: " + runtime.GOOS + "/" + runtime.GOARCH,
		"Today's date: 2026-11-17",
		"Git: no git repository detected",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("instructions are missing %q", want)
		}
	}
	if strings.Contains(out, "Project instructions") {
		t.Error("a project section appeared although the directory has no project file")
	}
}

func TestInstructionsIncludeProjectFile(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string // text that must appear
		not   string // text that must not appear
		in    string // file name expected in the heading
	}{
		{"AGENTS.md", map[string]string{"AGENTS.md": "use tabs"}, "use tabs", "", "AGENTS.md"},
		{"CLAUDE.md fallback", map[string]string{"CLAUDE.md": "be brief"}, "be brief", "", "CLAUDE.md"},
		{"AGENTS.md wins", map[string]string{"AGENTS.md": "from agents", "CLAUDE.md": "from claude"}, "from agents", "from claude", "AGENTS.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := plainDir(t)
			for name, content := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out := render(t, dir)
			if !strings.Contains(out, "# Project instructions") || !strings.Contains(out, "`"+tt.in+"`") || !strings.Contains(out, tt.want) {
				t.Errorf("project file not included correctly:\n%s", out)
			}
			if tt.not != "" && strings.Contains(out, tt.not) {
				t.Errorf("%q should not be included", tt.not)
			}
		})
	}
}

func TestLargeProjectFileIsCut(t *testing.T) {
	dir := plainDir(t)
	big := strings.Repeat("x", projectFileLimit) + "TAIL-MARKER"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	out := render(t, dir)
	if strings.Contains(out, "TAIL-MARKER") {
		t.Error("text beyond the limit was included")
	}
	if !strings.Contains(out, "has been cut") {
		t.Error("the model is not told that the file was cut")
	}
}

func TestGitSummary(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := plainDir(t)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("checkout", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "init")

	if got := gitSummary(context.Background(), dir); got != "branch main, no uncommitted changes" {
		t.Errorf("clean repo: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := gitSummary(context.Background(), dir); got != "branch main, uncommitted changes present" {
		t.Errorf("dirty repo: %q", got)
	}
}
