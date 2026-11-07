// Package agents is Lathe's small built-in registry of coding agents that
// speak the Agent Client Protocol, plus the PATH detection the web UI's
// "Bring your agent" picker shows. It runs nothing: it only looks for
// binaries and for installed Lathe skills, so the reader can pick which of
// their own agents `lathe serve` hosts.
package agents

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Agent is one registry entry: how to recognize the agent on PATH, the
// command that starts its ACP server, and how to get that command if it is
// missing.
type Agent struct {
	// ID is the stable key stored in ~/.lathe/config.json.
	ID string `json:"id"`
	// Name is the display name ("Claude Code").
	Name string `json:"name"`
	// Bin is the agent's own CLI, used to say "found on your PATH".
	Bin string `json:"bin"`
	// Command starts the agent's ACP server (run through /bin/sh).
	Command string `json:"command"`
	// Install tells the reader how to get the ACP command when it is missing.
	Install string `json:"install"`
	// Skills is the user-level skills dir, relative to the home dir, that the
	// agent reads SKILL.md files from (nil when it has none). The hosted agent
	// gets the skill text in its prompt, so this is informational only.
	Skills []string `json:"-"`
}

// Registry is the built-in list, in display order. Keep entries to agents
// with a documented ACP entry point.
var Registry = []Agent{
	{
		ID: "claude", Name: "Claude Code", Bin: "claude",
		Command: "claude-agent-acp",
		Install: "npm install -g @agentclientprotocol/claude-agent-acp",
		Skills:  []string{".claude", "skills"},
	},
	{
		ID: "codex", Name: "Codex", Bin: "codex",
		Command: "codex-acp",
		Install: "Install Zed's codex-acp adapter: https://github.com/zed-industries/codex-acp",
		Skills:  []string{".agents", "skills"},
	},
	{
		ID: "gemini", Name: "Gemini CLI", Bin: "gemini",
		Command: "gemini --experimental-acp",
		Install: "npm install -g @google/gemini-cli",
		Skills:  []string{".gemini", "skills"},
	},
	{
		ID: "opencode", Name: "opencode", Bin: "opencode",
		Command: "opencode acp",
		Install: "npm install -g opencode-ai",
		Skills:  []string{".config", "opencode", "skills"},
	},
	{
		ID: "goose", Name: "Goose", Bin: "goose",
		Command: "goose acp",
		Install: "Install Goose: https://block.github.io/goose/docs/getting-started/installation",
	},
}

// ByID returns the registry entry with that id.
func ByID(id string) (Agent, bool) {
	id = strings.TrimSpace(id)
	for _, a := range Registry {
		if a.ID == id {
			return a, true
		}
	}
	return Agent{}, false
}

// ByCommand returns the registry entry whose ACP command is command (after
// trimming), so a `--agent claude-agent-acp` run still shows "Claude Code".
func ByCommand(command string) (Agent, bool) {
	command = strings.Join(strings.Fields(command), " ")
	for _, a := range Registry {
		if a.Command == command {
			return a, true
		}
	}
	return Agent{}, false
}

// DisplayName names a hosted agent command: the registry name when it is a
// known command, else the command's first word.
func DisplayName(command string) string {
	if a, ok := ByCommand(command); ok {
		return a.Name
	}
	f := strings.Fields(command)
	if len(f) == 0 {
		return ""
	}
	return filepath.Base(f[0])
}

// Detected is one registry entry plus what Lathe found for it on this
// machine.
type Detected struct {
	Agent
	// Found: the agent's own CLI is on PATH.
	Found bool `json:"found"`
	// Ready: the ACP command's program is on PATH, so it can be started.
	Ready bool `json:"ready"`
	// SkillsInstalled: Lathe's skills are in the agent's user-level skills
	// dir. Nil when the agent has no such dir.
	SkillsInstalled *bool `json:"skillsInstalled,omitempty"`
}

// LookPath finds a program; tests swap it.
type LookPath func(file string) (string, error)

// Detect checks every registry entry against lookPath (exec.LookPath when
// nil) and the home dir's skills folders.
func Detect(lookPath LookPath, home string) []Detected {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	out := make([]Detected, 0, len(Registry))
	for _, a := range Registry {
		d := Detected{Agent: a}
		if _, err := lookPath(a.Bin); err == nil {
			d.Found = true
		}
		if prog := firstWord(a.Command); prog != "" {
			if _, err := lookPath(prog); err == nil {
				d.Ready = true
			}
		}
		// An ACP adapter on PATH counts as finding the agent, even if its CLI
		// lives somewhere else.
		if d.Ready {
			d.Found = true
		}
		if len(a.Skills) > 0 && home != "" {
			p := filepath.Join(append(append([]string{home}, a.Skills...), "lathe", "SKILL.md")...)
			_, err := os.Stat(p)
			ok := err == nil
			d.SkillsInstalled = &ok
		}
		out = append(out, d)
	}
	return out
}

func firstWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}
