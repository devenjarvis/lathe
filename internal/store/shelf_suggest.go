package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Shelf suggestions are what the reader's agent proposes for the library's
// filing (issue #135): a shelf for every tutorial ("Sort my shelves…") and a
// two-way split of a shelf that has grown past the split threshold. They are
// only proposals: recording one moves nothing. The reader reviews it and
// applies it with ApplySort / ApplySplit, which are the only writes here that
// touch shelves.json.
//
// They live in ~/.lathe/shelf-suggestions.json, beside shelves.json, with the
// split banner's "Not now" dismissals.
const ShelfSuggestionsFile = "shelf-suggestions.json"

// SplitRegrowth is how many more tutorials a shelf must gain after "Not now"
// before its split banner shows again.
const SplitRegrowth = 3

const suggestionsVersion = 1

// ShelfSuggestions is the decoded shelf-suggestions.json.
type ShelfSuggestions struct {
	Version int `json:"version"`
	// Sort is the pending "Sort my shelves" proposal (nil for none).
	Sort *SortProposal `json:"sort,omitempty"`
	// Splits maps a shelf ID to its pending split proposal.
	Splits map[string]*SplitProposal `json:"splits,omitempty"`
	// Dismissed maps a shelf ID to its size when the reader chose "Not now"
	// on its split banner.
	Dismissed map[string]int `json:"dismissed,omitempty"`
}

// SortProposal maps tutorials to the shelf the agent proposes for each.
type SortProposal struct {
	Created time.Time `json:"created"`
	// Picks maps a slug to a shelf: an existing shelf's ID or name, or the
	// name of a shelf to create.
	Picks map[string]string `json:"picks"`
}

// SplitProposal is a proposed split of one shelf into two.
type SplitProposal struct {
	Created time.Time `json:"created"`
	// Count is how many tutorials the shelf held when it was proposed.
	Count  int          `json:"count"`
	Groups []SplitGroup `json:"groups"`
}

// SplitGroup is one side of a split: a shelf name and its tutorials.
type SplitGroup struct {
	Name  string   `json:"name"`
	Slugs []string `json:"slugs"`
}

// ShelfSuggestionsPath is where shelf-suggestions.json lives for a tutorials
// dir (beside shelves.json).
func ShelfSuggestionsPath(tutorialsDir string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(tutorialsDir)), ShelfSuggestionsFile)
}

// ReadShelfSuggestions loads the suggestions for a tutorials dir. A missing
// file is an empty set.
func ReadShelfSuggestions(tutorialsDir string) (*ShelfSuggestions, error) {
	s := &ShelfSuggestions{}
	path := ShelfSuggestionsPath(tutorialsDir)
	if err := readJSONFile(path, s); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.normalize(), nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return s.normalize(), nil
}

func (s *ShelfSuggestions) normalize() *ShelfSuggestions {
	if s.Splits == nil {
		s.Splits = map[string]*SplitProposal{}
	}
	if s.Dismissed == nil {
		s.Dismissed = map[string]int{}
	}
	if s.Sort != nil && s.Sort.Picks == nil {
		s.Sort.Picks = map[string]string{}
	}
	return s
}

var suggestionsMu sync.Mutex

// updateSuggestions is the locked read-modify-write of the suggestions file.
func updateSuggestions(tutorialsDir string, fn func(*ShelfSuggestions) error) error {
	suggestionsMu.Lock()
	defer suggestionsMu.Unlock()
	s, err := ReadShelfSuggestions(tutorialsDir)
	if err != nil {
		return err
	}
	if err := fn(s); err != nil {
		return err
	}
	if s.Sort != nil && len(s.Sort.Picks) == 0 {
		s.Sort = nil
	}
	path := ShelfSuggestionsPath(tutorialsDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	s.Version = suggestionsVersion
	return writeJSONFile(path, s)
}

// librarySlugs lists the stored tutorials' slugs (directories with readable
// metadata), sorted.
func librarySlugs(tutorialsDir string) []string {
	entries, err := os.ReadDir(tutorialsDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := ReadMetadata(filepath.Join(tutorialsDir, e.Name())); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// SlugsOnShelf lists the stored tutorials on shelf id, sorted.
func SlugsOnShelf(tutorialsDir string, shelves *Shelves, id string) []string {
	var out []string
	for _, slug := range librarySlugs(tutorialsDir) {
		if shelves.ShelfOf(slug).ID == id {
			out = append(out, slug)
		}
	}
	return out
}

// ResetSortProposal drops any pending sort proposal, so a new run starts
// from a clean slate.
func ResetSortProposal(tutorialsDir string) error {
	return updateSuggestions(tutorialsDir, func(s *ShelfSuggestions) error {
		s.Sort = nil
		return nil
	})
}

// ProposeShelf records the agent's pick of a shelf for one tutorial in the
// pending sort proposal. shelf is an existing shelf's ID or name, or the name
// of a new shelf. It moves nothing. It returns a sentence for the agent.
func ProposeShelf(tutorialsDir, slug, shelf string) (string, error) {
	if err := ValidateSlug(slug); err != nil {
		return "", err
	}
	if _, err := ReadMetadata(filepath.Join(tutorialsDir, slug)); err != nil {
		return "", fmt.Errorf("no tutorial %q", slug)
	}
	name, err := CleanShelfName(shelf)
	if err != nil {
		return "", err
	}
	shelves, err := ReadShelves(ShelvesPath(tutorialsDir))
	if err != nil {
		return "", err
	}
	label := "a new shelf"
	if sh, ok := shelves.Find(name); ok {
		name, label = sh.Name, "an existing shelf"
		if sh.Unsorted() {
			label = "no shelf"
		}
	}
	err = updateSuggestions(tutorialsDir, func(s *ShelfSuggestions) error {
		if s.Sort == nil {
			s.Sort = &SortProposal{Created: time.Now().UTC(), Picks: map[string]string{}}
		}
		s.Sort.Picks[slug] = name
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Proposed %q (%s) for %s. Nothing moves until the reader applies the proposal.", name, label, slug), nil
}

// ProposeSplit records a proposed split of shelf id into exactly two groups.
// Every tutorial on the shelf must be in exactly one group, and each group
// needs a name and at least one tutorial. It moves nothing.
func ProposeSplit(tutorialsDir, id string, groups []SplitGroup) (string, error) {
	shelves, err := ReadShelves(ShelvesPath(tutorialsDir))
	if err != nil {
		return "", err
	}
	sh, ok := shelves.Get(id)
	if !ok {
		if sh, ok = shelves.Find(id); !ok {
			return "", fmt.Errorf("%w: %q", ErrShelfNotFound, id)
		}
	}
	if sh.Unsorted() {
		return "", errors.New("the Unsorted shelf isn't split; sort it with Sort my shelves instead")
	}
	if len(groups) != 2 {
		return "", fmt.Errorf("a split has exactly two groups, got %d", len(groups))
	}
	on := SlugsOnShelf(tutorialsDir, shelves, sh.ID)
	onShelf := map[string]bool{}
	for _, slug := range on {
		onShelf[slug] = true
	}
	seen := map[string]bool{}
	clean := make([]SplitGroup, 0, 2)
	for i, g := range groups {
		name, err := CleanShelfName(g.Name)
		if err != nil {
			return "", fmt.Errorf("group %d: %w", i+1, err)
		}
		if strings.EqualFold(name, UnsortedShelfName) {
			return "", fmt.Errorf("group %d: %q is reserved", i+1, UnsortedShelfName)
		}
		if i == 1 && strings.EqualFold(name, clean[0].Name) {
			return "", errors.New("the two groups need different names")
		}
		if len(g.Slugs) == 0 {
			return "", fmt.Errorf("group %d (%s) has no tutorials", i+1, name)
		}
		cg := SplitGroup{Name: name}
		for _, slug := range g.Slugs {
			slug = strings.TrimSpace(slug)
			switch {
			case !onShelf[slug]:
				return "", fmt.Errorf("%q is not on the shelf %s", slug, sh.Name)
			case seen[slug]:
				return "", fmt.Errorf("%q is in both groups", slug)
			}
			seen[slug] = true
			cg.Slugs = append(cg.Slugs, slug)
		}
		clean = append(clean, cg)
	}
	var missing []string
	for _, slug := range on {
		if !seen[slug] {
			missing = append(missing, slug)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("every tutorial on %s must be in a group; missing: %s", sh.Name, strings.Join(missing, ", "))
	}
	err = updateSuggestions(tutorialsDir, func(s *ShelfSuggestions) error {
		s.Splits[sh.ID] = &SplitProposal{Created: time.Now().UTC(), Count: len(on), Groups: clean}
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Proposed splitting %s into %s (%d) and %s (%d). Nothing moves until the reader applies it.",
		sh.Name, clean[0].Name, len(clean[0].Slugs), clean[1].Name, len(clean[1].Slugs)), nil
}

// DismissSplit remembers "Not now" on shelf id's split banner at its current
// size: the banner stays away until the shelf grows by SplitRegrowth.
func DismissSplit(tutorialsDir, id string, count int) error {
	return updateSuggestions(tutorialsDir, func(s *ShelfSuggestions) error {
		s.Dismissed[id] = count
		return nil
	})
}

// SplitDue reports whether a shelf of count tutorials should show the split
// banner: the threshold is on (> 0), the shelf is over it, and it was not
// dismissed at a size within SplitRegrowth of now.
func SplitDue(count, threshold, dismissedAt int) bool {
	if threshold <= 0 || count <= threshold {
		return false
	}
	return dismissedAt == 0 || count >= dismissedAt+SplitRegrowth
}

// CurrentSplit returns the split proposal for shelf id as it applies now:
// tutorials no longer on the shelf are dropped, and ones added since go to
// the first group (the side that keeps the shelf). ok is false when there is
// no proposal or a group ended up empty.
func (s *ShelfSuggestions) CurrentSplit(on []string, id string) (groups []SplitGroup, ok bool) {
	p := s.Splits[id]
	if p == nil || len(p.Groups) != 2 {
		return nil, false
	}
	onShelf := map[string]bool{}
	for _, slug := range on {
		onShelf[slug] = true
	}
	placed := map[string]bool{}
	for _, g := range p.Groups {
		ng := SplitGroup{Name: g.Name}
		for _, slug := range g.Slugs {
			if onShelf[slug] && !placed[slug] {
				placed[slug] = true
				ng.Slugs = append(ng.Slugs, slug)
			}
		}
		groups = append(groups, ng)
	}
	for _, slug := range on {
		if !placed[slug] {
			groups[0].Slugs = append(groups[0].Slugs, slug)
		}
	}
	if len(groups[0].Slugs) == 0 || len(groups[1].Slugs) == 0 {
		return nil, false
	}
	return groups, true
}

// SortResult says what ApplySort did.
type SortResult struct {
	Moved   int
	Created []Shelf
}

// ApplySort applies the pending sort proposal: each tutorial goes on its
// proposed shelf, creating shelves that don't exist yet. only, when non-nil,
// limits it to those slugs; the rest of the proposal is dropped either way
// (the reader reviewed it). Picks for tutorials no longer in the library are
// skipped.
func ApplySort(tutorialsDir string, only []string) (SortResult, error) {
	var res SortResult
	suggestionsMu.Lock()
	sug, err := ReadShelfSuggestions(tutorialsDir)
	suggestionsMu.Unlock()
	if err != nil {
		return res, err
	}
	if sug.Sort == nil || len(sug.Sort.Picks) == 0 {
		return res, errors.New("there is no shelf proposal to apply")
	}
	stored := map[string]bool{}
	for _, slug := range librarySlugs(tutorialsDir) {
		stored[slug] = true
	}
	slugs := make([]string, 0, len(sug.Sort.Picks))
	for slug := range sug.Sort.Picks {
		if stored[slug] && (only == nil || slices.Contains(only, slug)) {
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)
	_, err = UpdateShelves(ShelvesPath(tutorialsDir), func(sh *Shelves) error {
		for _, slug := range slugs {
			before := sh.ShelfOf(slug).ID
			target, created, err := sh.FindOrCreate(sug.Sort.Picks[slug])
			if err != nil {
				return fmt.Errorf("%s: %w", slug, err)
			}
			if created {
				res.Created = append(res.Created, target)
			}
			if err := sh.Assign(slug, target.ID); err != nil {
				return err
			}
			if target.ID != before {
				res.Moved++
			}
		}
		return nil
	})
	if err != nil {
		return SortResult{}, err
	}
	return res, DiscardSort(tutorialsDir)
}

// DiscardSort drops the pending sort proposal.
func DiscardSort(tutorialsDir string) error { return ResetSortProposal(tutorialsDir) }

// SplitResult says what ApplySplit did: Kept is the original shelf (renamed
// to the first group's name), Into the shelf the second group went to.
type SplitResult struct {
	Kept, Into Shelf
	Moved      int
}

// ApplySplit applies shelf id's split proposal. The first group keeps the
// shelf (its ID, place and links), renamed to the group's name; the second
// group moves to a shelf of its name — an existing one, or a new one placed
// right after the original.
func ApplySplit(tutorialsDir, id string) (SplitResult, error) {
	var res SplitResult
	suggestionsMu.Lock()
	sug, err := ReadShelfSuggestions(tutorialsDir)
	suggestionsMu.Unlock()
	if err != nil {
		return res, err
	}
	shelvesPath := ShelvesPath(tutorialsDir)
	cur, err := ReadShelves(shelvesPath)
	if err != nil {
		return res, err
	}
	groups, ok := sug.CurrentSplit(SlugsOnShelf(tutorialsDir, cur, id), id)
	if !ok {
		return res, errors.New("there is no split to apply for this shelf")
	}
	_, err = UpdateShelves(shelvesPath, func(sh *Shelves) error {
		i := sh.index(id)
		if i < 0 {
			return fmt.Errorf("%w: %q", ErrShelfNotFound, id)
		}
		keep := sh.Shelves[i]
		if !strings.EqualFold(keep.Name, groups[0].Name) {
			if other, ok := sh.Find(groups[0].Name); ok && other.ID != id {
				// The first group's name is another shelf: its tutorials
				// join that one, and the original keeps its name.
				for _, slug := range groups[0].Slugs {
					if err := sh.Assign(slug, other.ID); err != nil {
						return err
					}
					res.Moved++
				}
			} else if err := sh.Rename(id, groups[0].Name); err != nil {
				return err
			}
		}
		into, created, err := sh.FindOrCreate(groups[1].Name)
		if err != nil {
			return err
		}
		if into.ID == id {
			return errors.New("the second group can't keep the original shelf")
		}
		if created {
			if err := sh.Move(into.ID, sh.index(id)+1); err != nil {
				return err
			}
		}
		for _, slug := range groups[1].Slugs {
			if err := sh.Assign(slug, into.ID); err != nil {
				return err
			}
			res.Moved++
		}
		res.Kept, _ = sh.Get(id)
		res.Into = into
		return nil
	})
	if err != nil {
		return SplitResult{}, err
	}
	err = updateSuggestions(tutorialsDir, func(s *ShelfSuggestions) error {
		delete(s.Splits, id)
		delete(s.Dismissed, id)
		return nil
	})
	return res, err
}
