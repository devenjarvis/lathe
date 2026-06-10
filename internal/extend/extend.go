package extend

import "fmt"

// NextPartFilename returns the zero-padded part-NN.md filename that follows
// the last entry in parts, or "part-01.md" when parts is empty. The actual
// generation now happens in the user's interactive coding-agent session via the
// /lathe-extend skill; this helper is shared by the CLI commands that reserve
// and commit the part.
func NextPartFilename(parts []string) string {
	return fmt.Sprintf("part-%02d.md", len(parts)+1)
}
