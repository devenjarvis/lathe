package skills

import (
	"fmt"
	"strings"

	"github.com/devenjarvis/lathe/internal/frontmatter"
)

// CursorCommand renders a skill as a Cursor command file.
//
// Cursor invokes the file at .cursor/commands/<slug>.md as the slash command
// /<slug>. Cursor does not use Claude Code's YAML frontmatter, so we strip it
// and prepend a plain Markdown title + description header. The body (the bash
// `lathe ...` calls and prose guidance) is preserved verbatim -- it reads fine
// as Cursor instructions. We deliberately do not port the interactive-handoff
// runtime model here; trigger + body only (see AGENTS.md / the install caveats).
func CursorCommand(s Skill) string {
	body := frontmatter.Strip(string(s.Raw))

	var b strings.Builder
	fmt.Fprintf(&b, "# /%s\n\n", s.Slug)
	if s.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", s.Description)
	}
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// CursorFilename maps a skill slug to its Cursor command filename.
func CursorFilename(s Skill) string {
	return s.Slug + ".md"
}
