package frontmatter

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantName string
		wantDesc string
	}{
		{
			name:     "well-formed",
			raw:      "---\nname: plainspoken\ndescription: Honest and precise.\n---\n\nbody",
			wantName: "plainspoken",
			wantDesc: "Honest and precise.",
		},
		{
			name:     "quoted values",
			raw:      "---\nname: \"companion\"\ndescription: 'A friend.'\n---\nbody",
			wantName: "companion",
			wantDesc: "A friend.",
		},
		{
			name:     "no fence returns empty",
			raw:      "name: nope\ndescription: nope",
			wantName: "",
			wantDesc: "",
		},
		{
			name:     "unclosed fence returns empty",
			raw:      "---\nname: nope\ndescription: nope",
			wantName: "",
			wantDesc: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotDesc := Parse([]byte(tt.raw))
			if gotName != tt.wantName || gotDesc != tt.wantDesc {
				t.Errorf("Parse() = (%q, %q), want (%q, %q)", gotName, gotDesc, tt.wantName, tt.wantDesc)
			}
		})
	}
}

func TestStrip(t *testing.T) {
	got := Strip("---\nname: x\n---\n\nhello\nworld")
	if got != "hello\nworld" {
		t.Errorf("Strip() = %q, want %q", got, "hello\nworld")
	}
	// No frontmatter: untouched.
	if got := Strip("hello"); got != "hello" {
		t.Errorf("Strip() = %q, want %q", got, "hello")
	}
}
