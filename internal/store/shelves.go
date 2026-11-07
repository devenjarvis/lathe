package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/devenjarvis/lathe/internal/config"
)

// ShelvesFile is the library's shelf list: which shelves exist, their order,
// and which shelf each tutorial sits on. It lives at ~/.lathe/shelves.json,
// beside the tutorials dir rather than inside any tutorial, because a shelf is
// the reader's filing, not the tutorial's content: like workspace.json it is
// local reader state and never part of metadata.json, so re-storing a
// tutorial (which rewrites its directory) never loses its shelf.
const ShelvesFile = "shelves.json"

// UnsortedShelfID and UnsortedShelfName name the virtual shelf every tutorial
// without an assignment sits on. It is never stored in the shelf list: it
// always exists, always sorts last, and can't be renamed or deleted.
const (
	UnsortedShelfID   = "unsorted"
	UnsortedShelfName = "Unsorted"
)

// MaxShelfNameLen caps a shelf name, in characters.
const MaxShelfNameLen = 60

// MaxShelfDescriptionLen caps a shelf description, in characters.
const MaxShelfDescriptionLen = 200

// UnsortedShelfDescription is the virtual Unsorted shelf's fixed description.
const UnsortedShelfDescription = "Tutorials that aren't on a shelf yet. New tutorials land here."

const shelvesVersion = 1

// Shelf is one shelf in the library. ID is derived from the name at creation
// and stays fixed through renames, so links (?shelf=<id>) and assignments keep
// working when a shelf is renamed.
type Shelf struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Description is an optional one-line note shown under the shelf's title
	// on its page.
	Description string `json:"description,omitempty"`
}

// Unsorted reports whether s is the virtual Unsorted shelf.
func (s Shelf) Unsorted() bool { return s.ID == UnsortedShelfID }

// UnsortedShelf returns the virtual shelf for unassigned tutorials.
func UnsortedShelf() Shelf {
	return Shelf{ID: UnsortedShelfID, Name: UnsortedShelfName, Description: UnsortedShelfDescription}
}

// Shelves is the decoded shelves.json. Shelves is in display order; Placements maps
// a tutorial slug to a shelf ID. A slug with no entry, or one pointing at a
// shelf that no longer exists, is on the Unsorted shelf.
type Shelves struct {
	Version    int               `json:"version"`
	Shelves    []Shelf           `json:"shelves"`
	Placements map[string]string `json:"placements"`
}

// ErrShelfNotFound is returned when an operation names a shelf ID that is not
// in the list.
var ErrShelfNotFound = errors.New("no such shelf")

// ShelvesPath returns where shelves.json lives for a tutorials dir: its parent
// (~/.lathe for ~/.lathe/tutorials). The server and the CLI both derive it this
// way, so they always agree on the file.
func ShelvesPath(tutorialsDir string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(tutorialsDir)), ShelvesFile)
}

// DefaultShelvesPath is ShelvesPath for the user's tutorials dir.
func DefaultShelvesPath() (string, error) {
	dir, err := config.TutorialsDir()
	if err != nil {
		return "", err
	}
	return ShelvesPath(dir), nil
}

// ReadShelves loads the shelf list at path. A missing file is not an error: it
// yields an empty library, where every tutorial is Unsorted. A present but
// corrupt file surfaces its error, so a caller that is about to write can
// refuse rather than clobber the reader's filing.
func ReadShelves(path string) (*Shelves, error) {
	s := &Shelves{}
	if err := readJSONFile(path, s); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.normalize(), nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return s.normalize(), nil
}

// WriteShelves writes s to path atomically (temp file + rename), creating the
// parent dir if needed.
func WriteShelves(path string, s *Shelves) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	s.normalize()
	s.Version = shelvesVersion
	return writeJSONFile(path, s)
}

// shelvesMu serializes UpdateShelves within this process (the server's
// handlers). Writes are atomic, so another process (the CLI) can never see a
// torn file; it can at worst race a read-modify-write, which for a reader
// filing their own tutorials is an acceptable last-writer-wins.
var shelvesMu sync.Mutex

// UpdateShelves reads the shelf list at path, applies fn, and writes the result
// back if fn succeeds. It returns the updated list. A corrupt file is reported,
// not overwritten.
func UpdateShelves(path string, fn func(*Shelves) error) (*Shelves, error) {
	shelvesMu.Lock()
	defer shelvesMu.Unlock()
	s, err := ReadShelves(path)
	if err != nil {
		return nil, err
	}
	if err := fn(s); err != nil {
		return nil, err
	}
	if err := WriteShelves(path, s); err != nil {
		return nil, err
	}
	return s, nil
}

// normalize makes a decoded (possibly hand-edited) list safe to use: a non-nil
// map, no shelves without an ID, no duplicate IDs, and no stored shelf
// claiming the reserved Unsorted ID.
func (s *Shelves) normalize() *Shelves {
	if s.Placements == nil {
		s.Placements = map[string]string{}
	}
	seen := map[string]bool{UnsortedShelfID: true}
	kept := s.Shelves[:0]
	for _, sh := range s.Shelves {
		sh.ID = strings.TrimSpace(sh.ID)
		sh.Name = strings.TrimSpace(sh.Name)
		sh.Description = strings.Join(strings.Fields(sh.Description), " ")
		if sh.ID == "" || seen[sh.ID] {
			continue
		}
		if sh.Name == "" {
			sh.Name = sh.ID
		}
		seen[sh.ID] = true
		kept = append(kept, sh)
	}
	s.Shelves = kept
	for slug, id := range s.Placements {
		if id == "" || id == UnsortedShelfID {
			delete(s.Placements, slug)
		}
	}
	return s
}

// Get returns the shelf with id. The Unsorted ID always resolves.
func (s *Shelves) Get(id string) (Shelf, bool) {
	if id == UnsortedShelfID {
		return UnsortedShelf(), true
	}
	for _, sh := range s.Shelves {
		if sh.ID == id {
			return sh, true
		}
	}
	return Shelf{}, false
}

// Find resolves a shelf by ID, else by name (case-insensitively). It is the
// lookup for user-typed input, such as `lathe shelf set <slug> "Systems"`.
func (s *Shelves) Find(idOrName string) (Shelf, bool) {
	q := strings.TrimSpace(idOrName)
	if sh, ok := s.Get(q); ok {
		return sh, true
	}
	for _, sh := range s.Shelves {
		if strings.EqualFold(sh.Name, q) {
			return sh, true
		}
	}
	if strings.EqualFold(q, UnsortedShelfName) {
		return UnsortedShelf(), true
	}
	return Shelf{}, false
}

// ShelfOf returns the shelf a tutorial sits on: its assigned shelf, or Unsorted
// when it has none or its shelf was removed.
func (s *Shelves) ShelfOf(slug string) Shelf {
	if id, ok := s.Placements[slug]; ok {
		if sh, ok := s.Get(id); ok {
			return sh
		}
	}
	return UnsortedShelf()
}

// Create appends a new shelf named name and returns it. The name must be
// non-empty, unique (case-insensitively, Unsorted included) and at most
// MaxShelfNameLen characters; its ID is a slug of the name, suffixed to stay
// unique.
func (s *Shelves) Create(name string) (Shelf, error) {
	name, err := s.checkName(name, "")
	if err != nil {
		return Shelf{}, err
	}
	sh := Shelf{ID: s.uniqueID(shelfSlug(name)), Name: name}
	s.Shelves = append(s.Shelves, sh)
	return sh, nil
}

// Rename changes a shelf's display name. Its ID, position and tutorials stay.
func (s *Shelves) Rename(id, name string) error {
	if id == UnsortedShelfID {
		return errors.New("the Unsorted shelf can't be renamed")
	}
	i := s.index(id)
	if i < 0 {
		return fmt.Errorf("%w: %q", ErrShelfNotFound, id)
	}
	name, err := s.checkName(name, id)
	if err != nil {
		return err
	}
	s.Shelves[i].Name = name
	return nil
}

// Describe sets a shelf's one-line description; "" clears it. Whitespace
// (newlines included) collapses to single spaces.
func (s *Shelves) Describe(id, description string) error {
	if id == UnsortedShelfID {
		return errors.New("the Unsorted shelf's description can't be changed")
	}
	i := s.index(id)
	if i < 0 {
		return fmt.Errorf("%w: %q", ErrShelfNotFound, id)
	}
	description = strings.Join(strings.Fields(description), " ")
	if n := utf8.RuneCountInString(description); n > MaxShelfDescriptionLen {
		return fmt.Errorf("shelf description is %d characters; the limit is %d", n, MaxShelfDescriptionLen)
	}
	for _, r := range description {
		if unicode.IsControl(r) {
			return errors.New("shelf description contains a control character")
		}
	}
	s.Shelves[i].Description = description
	return nil
}

// Delete removes a shelf. Its tutorials are not deleted: they move to
// Unsorted.
func (s *Shelves) Delete(id string) error {
	if id == UnsortedShelfID {
		return errors.New("the Unsorted shelf can't be deleted")
	}
	i := s.index(id)
	if i < 0 {
		return fmt.Errorf("%w: %q", ErrShelfNotFound, id)
	}
	s.Shelves = append(s.Shelves[:i], s.Shelves[i+1:]...)
	for slug, sid := range s.Placements {
		if sid == id {
			delete(s.Placements, slug)
		}
	}
	return nil
}

// Assign puts a tutorial on a shelf, taking it off whichever shelf it was on
// (a tutorial sits on exactly one). Assigning to UnsortedShelfID unassigns it.
// The slug is validated but not looked up, so filing works before the
// tutorial is stored and survives it being re-stored.
func (s *Shelves) Assign(slug, id string) error {
	if err := ValidateSlug(slug); err != nil {
		return err
	}
	if id == UnsortedShelfID {
		delete(s.Placements, slug)
		return nil
	}
	if s.index(id) < 0 {
		return fmt.Errorf("%w: %q", ErrShelfNotFound, id)
	}
	s.Placements[slug] = id
	return nil
}

// Move repositions a shelf in the display order. index is clamped to the list,
// so 0 moves it first and a large index moves it last. Unsorted always stays
// last and can't be moved.
func (s *Shelves) Move(id string, index int) error {
	if id == UnsortedShelfID {
		return errors.New("the Unsorted shelf always sorts last")
	}
	i := s.index(id)
	if i < 0 {
		return fmt.Errorf("%w: %q", ErrShelfNotFound, id)
	}
	sh := s.Shelves[i]
	s.Shelves = append(s.Shelves[:i], s.Shelves[i+1:]...)
	index = max(0, min(index, len(s.Shelves)))
	s.Shelves = append(s.Shelves, Shelf{})
	copy(s.Shelves[index+1:], s.Shelves[index:])
	s.Shelves[index] = sh
	return nil
}

func (s *Shelves) index(id string) int {
	for i, sh := range s.Shelves {
		if sh.ID == id {
			return i
		}
	}
	return -1
}

// checkName trims and validates a shelf name; self is the ID of the shelf being
// renamed (it may keep its own name in a different case).
func (s *Shelves) checkName(name, self string) (string, error) {
	name, err := CleanShelfName(name)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(name, UnsortedShelfName) {
		return "", fmt.Errorf("%q is reserved for tutorials that aren't on a shelf", UnsortedShelfName)
	}
	for _, sh := range s.Shelves {
		if sh.ID != self && strings.EqualFold(sh.Name, name) {
			return "", fmt.Errorf("a shelf named %q already exists", sh.Name)
		}
	}
	return name, nil
}

func (s *Shelves) uniqueID(base string) string {
	id := base
	for n := 2; ; n++ {
		if _, taken := s.Get(id); !taken {
			return id
		}
		id = base + "-" + strconv.Itoa(n)
	}
}

// shelfSlug turns a shelf name into an ID: lowercase letters and digits, runs
// of anything else collapsed to one hyphen ("Compilers & languages" →
// "compilers-languages"). A name with no letters or digits becomes "shelf".
func shelfSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		dash = true
	}
	if b.Len() == 0 {
		return "shelf"
	}
	return b.String()
}

// CleanShelfName collapses a shelf name's whitespace and checks it is
// non-empty, at most MaxShelfNameLen characters and free of control
// characters. It does not check uniqueness (Create and Rename do).
func CleanShelfName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", errors.New("shelf name is empty")
	}
	if n := utf8.RuneCountInString(name); n > MaxShelfNameLen {
		return "", fmt.Errorf("shelf name is %d characters; the limit is %d", n, MaxShelfNameLen)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("shelf name contains a control character")
		}
	}
	return name, nil
}

// FindOrCreate resolves a shelf by ID or name like Find, else creates a shelf
// with that name. created reports which.
func (s *Shelves) FindOrCreate(idOrName string) (sh Shelf, created bool, err error) {
	if sh, ok := s.Find(idOrName); ok {
		return sh, false, nil
	}
	sh, err = s.Create(idOrName)
	return sh, err == nil, err
}

// SetShelf puts the tutorial slug (stored, or reserved by a hosted New
// tutorial run) on a shelf named by ID or name. With create, a name no shelf
// has yet makes a new shelf; without it that is an error. It is `lathe shelf
// set` and the lathe_shelf_set MCP tool.
func SetShelf(tutorialsDir, slug, shelf string, create bool) (sh Shelf, created bool, err error) {
	if err := ValidateSlug(slug); err != nil {
		return Shelf{}, false, err
	}
	if _, err := ReadMetadata(filepath.Join(tutorialsDir, slug)); err != nil {
		return Shelf{}, false, fmt.Errorf("no tutorial %q", slug)
	}
	_, err = UpdateShelves(ShelvesPath(tutorialsDir), func(s *Shelves) error {
		var ok bool
		if sh, ok = s.Find(shelf); !ok {
			if !create {
				return fmt.Errorf("no shelf %q (list them with lathe shelf list; pass create to make it)", strings.TrimSpace(shelf))
			}
			var cerr error
			if sh, cerr = s.Create(shelf); cerr != nil {
				return cerr
			}
			created = true
		}
		return s.Assign(slug, sh.ID)
	})
	if err != nil {
		return Shelf{}, false, err
	}
	return sh, created, nil
}

// ShelfListing is one shelf with its tutorial count, for `lathe shelf list`
// and the lathe_shelf_list tool.
type ShelfListing struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Count       int    `json:"count"`
}

// ListShelves returns every shelf in display order, Unsorted last, with how
// many stored tutorials each holds.
func ListShelves(tutorialsDir string) ([]ShelfListing, error) {
	s, err := ReadShelves(ShelvesPath(tutorialsDir))
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, slug := range librarySlugs(tutorialsDir) {
		counts[s.ShelfOf(slug).ID]++
	}
	var out []ShelfListing
	for _, sh := range append(append([]Shelf{}, s.Shelves...), UnsortedShelf()) {
		out = append(out, ShelfListing{ID: sh.ID, Name: sh.Name, Description: sh.Description, Count: counts[sh.ID]})
	}
	return out, nil
}
