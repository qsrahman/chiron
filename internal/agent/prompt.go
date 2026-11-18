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

	Notes       []ServerNote // what MCP servers say about their tools, when the user asked for it
	Tools       bool         // whether the model has tools: the prompt says nothing about tools when it has none
	Project     string       // contents of the project file, empty when there is none
	ProjectFile string
	Truncated   bool
	Limit       int
}

// ServerNote is the usage advice an MCP server gave about its own tools.
type ServerNote struct{ Server, Text string }

// BuildInstructions renders the system prompt for a session working in workDir: the general guidance (with
// the part about tools only if the model has any: tool language in a prompt without tools made some models type
// calls as text or answer nothing), the project's own AGENTS.md or CLAUDE.md if it has one, and last the
// environment (directory, platform, date, git state), so that the text that rarely changes comes first and can
// be cached. Notes from MCP servers, if any, come after the guidance and before the project's own file.
func BuildInstructions(ctx context.Context, workDir string, now time.Time, hasTools bool, notes ...ServerNote) (string, error) {
	data := promptData{
		Notes:    notes,
		Tools:    hasTools,
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

// GitBranch returns the current branch of the repository containing dir, or "" if there is none.
func GitBranch(ctx context.Context, dir string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitSummary describes the repository state in one line for the environment block.
func gitSummary(ctx context.Context, dir string) string {
	branch := GitBranch(ctx, dir)
	if branch == "" {
		return "no git repository detected"
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	status, err := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return "branch " + branch
	}
	state := "no uncommitted changes"
	if len(bytes.TrimSpace(status)) > 0 {
		state = "uncommitted changes present"
	}
	return fmt.Sprintf("branch %s, %s", branch, state)
}
