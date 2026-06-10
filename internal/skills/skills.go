// Package skills bundles the Lathe skills into the binary so
// `lathe skills install` can write them out with no repo clone. The SKILL.md
// format is a cross-tool standard, so the same embedded bytes ship to every
// supported agent (Claude Code, Codex, Gemini CLI, opencode, Cline, Windsurf);
// only Cursor needs a translation (see cursor.go).
//
// The embedded copies under data/ are generated from the human-edited source at
// .claude/skills by `mage skills`; a `mage skillsCheck` parity gate (wired into
// `mage check`) keeps the two from drifting. go:embed cannot reach paths that
// begin with "." (so it can't read .claude/skills directly) -- that is the whole
// reason the data/ mirror exists.
package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/devenjarvis/lathe/internal/frontmatter"
)

//go:embed data
var dataFS embed.FS

// Skill is one bundled skill: its slug (directory name), the raw SKILL.md bytes
// (YAML frontmatter included), and the name/description parsed from the
// frontmatter.
type Skill struct {
	Slug        string
	Name        string
	Description string
	Raw         []byte
}

// All returns every bundled skill, sorted by slug. It returns an error only if
// the embedded tree is malformed (which would be a build-time bug).
func All() ([]Skill, error) {
	entries, err := fs.ReadDir(dataFS, "data")
	if err != nil {
		return nil, fmt.Errorf("read embedded skills: %w", err)
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()
		raw, err := dataFS.ReadFile("data/" + slug + "/SKILL.md")
		if err != nil {
			return nil, fmt.Errorf("read embedded skill %q: %w", slug, err)
		}
		name, desc := frontmatter.Parse(raw)
		if name == "" {
			name = slug
		}
		out = append(out, Skill{Slug: slug, Name: name, Description: desc, Raw: raw})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}
