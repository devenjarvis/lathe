// Package frontmatter parses the tiny leading YAML frontmatter block the Lathe
// skills and voice specs share: a "---"-delimited fence carrying only scalar
// name: and description: keys. Both internal/skills and internal/voice need it,
// so it lives here rather than being duplicated; a line scanner beats pulling in
// a YAML dependency for two keys.
package frontmatter

import "strings"

// Parse pulls name: and description: out of a leading YAML frontmatter block
// delimited by "---" lines.
//
// If there is no well-formed frontmatter block (no leading fence, or an
// unclosed one), it returns empty strings rather than harvesting key-looking
// lines from the body.
func Parse(raw []byte) (name, description string) {
	// strings.Split always returns at least one element, so lines[0] is safe.
	lines := strings.Split(string(raw), "\n")
	// The first line must be exactly the opening fence (tolerate a trailing \r).
	if strings.TrimRight(lines[0], "\r") != "---" {
		return "", ""
	}
	closed := false
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			closed = true
			break
		}
		if v, ok := value(line, "name"); ok {
			name = v
		} else if v, ok := value(line, "description"); ok {
			description = v
		}
	}
	if !closed {
		return "", ""
	}
	return name, description
}

// Strip removes a leading "---"-delimited YAML block (if present) and returns
// the remaining body with leading blank lines trimmed. If there is no
// well-formed block, the input is returned untouched.
func Strip(s string) string {
	// strings.Split always returns at least one element, so lines[0] is safe.
	lines := strings.Split(s, "\n")
	if strings.TrimRight(lines[0], "\r") != "---" {
		return s
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		// Malformed frontmatter: leave the content untouched.
		return s
	}
	body := strings.Join(lines[end+1:], "\n")
	return strings.TrimLeft(body, "\n")
}

// value returns the value for "key:" on a frontmatter line, trimming whitespace
// and surrounding quotes.
func value(line, key string) (string, bool) {
	prefix := key + ":"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	v := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	v = strings.Trim(v, `"'`)
	return v, true
}
