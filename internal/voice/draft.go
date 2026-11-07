package voice

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/frontmatter"
)

// Drafts for Settings › Voices › New voice (issue #141), drafted by the
// reader's hosted agent.
//
// Reserve + scoped draft + approved save. The server reserves the new voice's
// name before the agent starts (ReserveDraft: a directory under
// ~/.lathe/voices/.drafts/<name>/, which List never reads, so a reservation is
// not a voice). The agent's Lathe MCP server is scoped to that name
// (`lathe mcp --voice-draft <name>`): lathe_voice_draft records each draft
// there (AddDraft), and lathe_voice_add saves only the draft the reader
// approved with Save voice (Approve, then SaveApproved), only under the
// reserved name. The CLI stays the only writer of the voice files; the agent
// never touches ~/.lathe.

// draftsDirName is the hidden folder under the voices dir that holds
// reservations. custom() skips directories, so nothing in it is a voice.
const draftsDirName = ".drafts"

// approvalFile records the draft the reader chose with Save voice.
const approvalFile = "approved.json"

var (
	// ErrDraftBusy is returned by ReserveDraft when the name is already
	// reserved for another draft series.
	ErrDraftBusy = errors.New("a draft of that voice is already in progress")
	// ErrNoReservation is returned when the reservation is gone (the series
	// was cancelled or has ended).
	ErrNoReservation = errors.New("the New voice draft was cancelled or has ended")
	// ErrNotApproved is returned by SaveApproved before the reader has
	// clicked Save voice.
	ErrNotApproved = errors.New("the reader hasn't clicked Save voice yet")
)

// Draft is one draft in a series.
type Draft struct {
	N   int    // 1-based
	Raw []byte // the spec, frontmatter included, guardrail preamble excluded
}

// Voice returns the draft as a Voice under name (for Body/Sample).
func (d Draft) Voice(name string) Voice {
	_, desc := frontmatter.Parse(d.Raw)
	return Voice{Name: name, Description: desc, Raw: d.Raw}
}

// Approval is what Save voice recorded.
type Approval struct {
	Draft int `json:"draft"`
	// MakeDefault: "Make it my default" was ticked.
	MakeDefault bool `json:"makeDefault,omitempty"`
}

// DraftDir is the reservation directory for name. It does not check that it
// exists.
func DraftDir(name string) (string, error) {
	key := normalizeName(name)
	if !IsSlug(key) {
		return "", fmt.Errorf("invalid voice name %q", name)
	}
	dir, err := config.VoicesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, draftsDirName, key), nil
}

// ReserveDraft reserves name for a new voice's draft series. The name must be
// a slug that names no voice (built-in or custom) and no other reservation.
func ReserveDraft(name string) (string, error) {
	key := normalizeName(name)
	if !IsSlug(key) {
		return "", fmt.Errorf("use lowercase letters, digits and hyphens for a voice name, like %q", "field-notes")
	}
	if _, err := Resolve(key); err == nil {
		return "", fmt.Errorf("%w: %q", ErrExists, key)
	}
	dir, err := DraftDir(key)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		if os.IsExist(err) {
			return "", fmt.Errorf("%w: %q", ErrDraftBusy, key)
		}
		return "", err
	}
	return dir, nil
}

// CancelDraft removes name's reservation and its drafts. A missing
// reservation is not an error.
func CancelDraft(name string) error {
	dir, err := DraftDir(name)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func reservation(name string) (string, error) {
	dir, err := DraftDir(name)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", ErrNoReservation
	}
	return dir, nil
}

func draftFile(dir string, n int) string {
	return filepath.Join(dir, fmt.Sprintf("draft-%02d.md", n))
}

// AddDraft records the next draft of name's spec and returns its number.
// The spec must have a "## Sample" section (the preview shows it). A copy of
// the guardrail preamble in front is dropped (Lathe adds it at read time),
// and the frontmatter name is set to name.
func AddDraft(name, spec string) (int, error) {
	key := normalizeName(name)
	dir, err := reservation(key)
	if err != nil {
		return 0, err
	}
	spec = stripPreamble(spec)
	if strings.TrimSpace(spec) == "" {
		return 0, errors.New("the spec is empty")
	}
	v := Voice{Name: key, Raw: []byte(spec)}
	if strings.TrimSpace(v.Sample()) == "" {
		return 0, fmt.Errorf("the spec has no %q section: end it with the ring buffer paragraph in this voice (the reader's preview)", SampleHeading)
	}
	if strings.TrimSpace(v.Body()) == "" {
		return 0, errors.New("the spec has only a sample: add the Stance and register, Avoid and Calibration sections")
	}
	raw := renameFrontmatter(strings.TrimLeft(spec, "\r\n"), key)
	if !strings.HasSuffix(raw, "\n") {
		raw += "\n"
	}
	drafts, err := Drafts(key)
	if err != nil {
		return 0, err
	}
	n := 1
	if len(drafts) > 0 {
		n = drafts[len(drafts)-1].N + 1
	}
	if err := os.WriteFile(draftFile(dir, n), []byte(raw), 0o644); err != nil {
		return 0, err
	}
	return n, nil
}

// stripPreamble drops a leading copy of the guardrail preamble (up to and
// including its "---" rule), which `lathe voice show` prints and Lathe adds
// back at read time.
func stripPreamble(spec string) string {
	t := strings.TrimLeft(spec, " \t\r\n")
	if !strings.HasPrefix(t, "<!-- LATHE VOICE GUARDRAIL") {
		return spec
	}
	if i := strings.Index(t, "\n---\n"); i >= 0 {
		return strings.TrimLeft(t[i+len("\n---\n"):], "\r\n")
	}
	return spec
}

// Drafts lists name's drafts, oldest first. A missing reservation is
// ErrNoReservation.
func Drafts(name string) ([]Draft, error) {
	dir, err := reservation(name)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Draft
	for _, e := range entries {
		n, ok := draftNumber(e.Name())
		if !ok || e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, Draft{N: n, Raw: raw})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].N < out[j].N })
	return out, nil
}

func draftNumber(file string) (int, bool) {
	s, ok := strings.CutPrefix(file, "draft-")
	if !ok {
		return 0, false
	}
	s, ok = strings.CutSuffix(s, ".md")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n > 0
}

// Approve records the reader's Save voice on draft n.
func Approve(name string, a Approval) error {
	dir, err := reservation(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(draftFile(dir, a.Draft)); err != nil {
		return fmt.Errorf("there is no draft %d", a.Draft)
	}
	raw, _ := json.Marshal(a)
	return os.WriteFile(filepath.Join(dir, approvalFile), raw, 0o644)
}

// Unapprove clears a Save voice the agent has not acted on (the reader asked
// for another draft instead).
func Unapprove(name string) error {
	dir, err := reservation(name)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, approvalFile)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Approved returns the reader's Save voice, if any.
func Approved(name string) (Approval, bool) {
	dir, err := reservation(name)
	if err != nil {
		return Approval{}, false
	}
	raw, err := os.ReadFile(filepath.Join(dir, approvalFile))
	if err != nil {
		return Approval{}, false
	}
	var a Approval
	if json.Unmarshal(raw, &a) != nil || a.Draft < 1 {
		return Approval{}, false
	}
	return a, true
}

// SaveApproved saves the approved draft as the custom voice name — exactly
// as drafted. It refuses before the reader's Save voice, and refuses to
// replace a voice that appeared meanwhile.
func SaveApproved(name string) (Voice, int, error) {
	key := normalizeName(name)
	dir, err := reservation(key)
	if err != nil {
		return Voice{}, 0, err
	}
	a, ok := Approved(key)
	if !ok {
		return Voice{}, 0, ErrNotApproved
	}
	raw, err := os.ReadFile(draftFile(dir, a.Draft))
	if err != nil {
		return Voice{}, 0, fmt.Errorf("read draft %d: %w", a.Draft, err)
	}
	if _, err := Resolve(key); err == nil {
		return Voice{}, 0, fmt.Errorf("%w: %q", ErrExists, key)
	}
	if err := Add(key, raw); err != nil {
		return Voice{}, 0, err
	}
	v, err := Resolve(key)
	return v, a.Draft, err
}
