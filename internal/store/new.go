package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// A new tutorial written by the hosted agent ("New tutorial" in lathe serve)
// starts as a reservation: the server picks the slug up front and writes a
// placeholder with status "writing", so the library shows it under "Being
// written" while the agent works. The agent's scoped Lathe MCP server
// (`lathe mcp --slug <slug> --new`) may then store only into that slug; a
// successful store replaces the placeholder (status unverified). A run that
// ends without storing removes the reservation (CancelNew), so it never leaves
// an empty tutorial behind.

// ErrSlugTaken is returned when a slug already names a tutorial: by
// ReserveNew for an explicitly requested slug, and by RestoreTrashed when a
// tutorial was stored under the trashed one's slug since.
var ErrSlugTaken = errors.New("a tutorial with that slug already exists")

// maxSlugWords and maxSlugLen keep a slug derived from a topic short enough
// to read in a URL.
const (
	maxSlugWords = 6
	maxSlugLen   = 60
)

// slugFiller are the topic words that carry no meaning in a slug:
// "build a digital synth in Zig" → digital-synth-zig, the same shape the
// /lathe skill picks.
var slugFiller = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true,
	"in": true, "on": true, "of": true, "for": true, "to": true, "with": true,
	"from": true, "into": true, "using": true, "via": true, "by": true,
	"how": true, "build": true, "building": true, "make": true, "making": true,
	"write": true, "writing": true, "create": true, "learn": true, "my": true,
	"your": true, "own": true, "simple": true, "tutorial": true,
}

// SlugFromTopic derives a kebab-case slug from a free-text topic: lowercase
// ASCII letters and digits, filler words dropped, at most six words. It never
// returns an invalid slug; a topic with nothing usable gives "tutorial".
func SlugFromTopic(topic string) string {
	var words, all []string
	for _, w := range strings.FieldsFunc(strings.ToLower(topic), func(r rune) bool {
		alnum := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		return !alnum
	}) {
		all = append(all, w)
		if !slugFiller[w] {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		words = all // all filler ("how to build"): keep what there is
	}
	if len(words) > maxSlugWords {
		words = words[:maxSlugWords]
	}
	slug := strings.Join(words, "-")
	if len(slug) > maxSlugLen {
		slug = strings.TrimRight(slug[:maxSlugLen], "-")
	}
	if slug == "" {
		return "tutorial"
	}
	return slug
}

// NewReservation describes the tutorial a hosted /lathe run is about to
// write. Everything but Topic is optional and pre-fills the placeholder's
// metadata; the agent's store replaces all of it.
type NewReservation struct {
	Topic string
	// Slug, when set, is used exactly (ErrSlugTaken if it exists); otherwise
	// the slug is derived from Topic and made unique with a -2, -3, … suffix.
	Slug       string
	Voice      string
	Repo       string
	RepoBranch string
	Tools      []Tool
}

// ReserveNew creates the placeholder for a new tutorial under tutorialsDir:
// a directory with metadata.json (status writing, no parts) and nothing else.
// The directory is created with Mkdir, so two reservations can never get the
// same slug. It returns the placeholder's metadata and directory.
func ReserveNew(tutorialsDir string, r NewReservation) (*Tutorial, string, error) {
	topic := strings.Join(strings.Fields(r.Topic), " ")
	if topic == "" {
		return nil, "", errors.New("topic is required")
	}
	if err := os.MkdirAll(tutorialsDir, 0o755); err != nil {
		return nil, "", err
	}
	explicit := strings.TrimSpace(r.Slug)
	base := explicit
	if base == "" {
		base = SlugFromTopic(topic)
	}
	if err := ValidateSlug(base); err != nil {
		return nil, "", err
	}
	var slug, dir string
	for n := 1; ; n++ {
		slug = base
		if n > 1 {
			slug = base + "-" + strconv.Itoa(n)
		}
		dir = filepath.Join(tutorialsDir, slug)
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", fmt.Errorf("reserve %q: %w", slug, err)
		}
		if explicit != "" {
			return nil, "", fmt.Errorf("%w: %q", ErrSlugTaken, slug)
		}
		if n >= 1000 {
			return nil, "", fmt.Errorf("no free slug for %q", base)
		}
	}
	repo := NormalizeRepo(r.Repo)
	branch := strings.TrimSpace(r.RepoBranch)
	if repo == "" {
		branch = ""
	}
	t := &Tutorial{
		Slug:       slug,
		Title:      topicTitle(topic),
		Topic:      topic,
		Created:    time.Now().UTC(),
		Status:     StatusWriting,
		Repo:       repo,
		RepoBranch: branch,
		Tools:      NormalizeTools(r.Tools),
		Voice:      NormalizeVoice(r.Voice),
	}
	if err := WriteMetadata(dir, t); err != nil {
		_ = os.RemoveAll(dir)
		return nil, "", err
	}
	return t, dir, nil
}

// topicTitle is the placeholder's display title: the topic as typed, with a
// capital first letter.
func topicTitle(topic string) string {
	r := []rune(topic)
	if len(r) == 0 {
		return topic
	}
	return string(unicode.ToUpper(r[0])) + string(r[1:])
}

// CancelNew removes a reservation whose run ended without storing the
// tutorial. It wraps ErrNotInFlight (and changes nothing) when the tutorial
// is not "writing" any more: the agent stored it, so it is a real tutorial.
func CancelNew(tutDir string) error {
	slug := filepath.Base(tutDir)
	tut, err := ReadMetadata(tutDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %q is gone", ErrNotInFlight, slug)
		}
		return fmt.Errorf("read metadata for %q: %w", slug, err)
	}
	if tut.Status != StatusWriting {
		return fmt.Errorf("%w: %q is not being written (status: %s)", ErrNotInFlight, slug, tut.Status)
	}
	if err := os.RemoveAll(tutDir); err != nil {
		return fmt.Errorf("remove reservation %q: %w", slug, err)
	}
	return nil
}
