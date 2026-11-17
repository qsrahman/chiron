package agent

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"time"
)

//go:embed prompt.md.tmpl
var promptTemplate string

// projectFiles are looked up in the working directory, first match wins. AGENTS.md is the shared
// convention; CLAUDE.md is accepted because many projects only have that one.
var projectFiles = []string{"AGENTS.md", "CLAUDE.md"}

// projectFileLimit keeps a huge project file from filling the context window.
const projectFileLimit = 32 * 1024

// promptData is what the template renders.
type promptData struct {
	WorkDir, Platform, Date, Git string

	Project     string // contents of the project file, empty when there is none
	ProjectFile string
	Truncated   bool
	Limit       int
}

// BuildInstructions renders the system prompt for a session working in workDir: the general
// guidance, an environment block (directory, platform, date, git state) and the project's own
// AGENTS.md or CLAUDE.md if it has one.
func BuildInstructions(ctx context.Context, workDir string, now time.Time) (string, error) {
	data := promptData{
		WorkDir:  workDir,
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Date:     now.Format(time.DateOnly),
		Git:      gitSummary(ctx, workDir),
		Limit:    projectFileLimit,
	}
	data.ProjectFile, data.Project, data.Truncated = readProjectFile(workDir)

	tmpl, err := template.New("prompt").Parse(promptTemplate)
	if err != nil {
		return "", fmt.Errorf("parsing system prompt template: %w", err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("rendering system prompt: %w", err)
	}
	return out.String(), nil
}

// readProjectFile returns the first project file that exists, cut to projectFileLimit bytes.
// Unreadable files are skipped: a broken AGENTS.md should not stop the agent from starting.
func readProjectFile(dir string) (name, content string, truncated bool) {
	for _, candidate := range projectFiles {
		f, err := os.Open(filepath.Join(dir, candidate))
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, projectFileLimit+1))
		f.Close()
		if err != nil {
			continue
		}
		truncated = len(data) > projectFileLimit
		if truncated {
			data = data[:projectFileLimit]
		}
		// A cut can land inside a multi-byte character.
		return candidate, strings.TrimSpace(strings.ToValidUTF8(string(data), "")), truncated
	}
	return "", "", false
}

// gitSummary describes the repository state in one line for the environment block.
func gitSummary(ctx context.Context, dir string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	branch, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "no git repository detected"
	}
	status, err := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return "branch " + strings.TrimSpace(string(branch))
	}
	state := "no uncommitted changes"
	if len(bytes.TrimSpace(status)) > 0 {
		state = "uncommitted changes present"
	}
	return fmt.Sprintf("branch %s, %s", strings.TrimSpace(string(branch)), state)
}
