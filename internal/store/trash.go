package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
)

// Deleting a tutorial moves it to the Trash: ~/.lathe/trash/<slug>-<time>,
// beside the tutorials dir (like shelves.json). It stays there, restorable,
// until the retention setting (trash_days, default 30) passes or the reader
// deletes it for good; PurgeTrash, run when the server starts and on
// `lathe rm`, removes what has expired. The tutorial's shelf placement is
// kept while it is in the Trash, so a restore puts it back on its shelf; it
// is dropped only when the tutorial is deleted permanently.

// TrashDirName is the Trash folder's name, beside the tutorials dir.
const TrashDirName = "trash"

// trashTimeLayout is the <time> suffix of a Trash entry's folder name. It is
// fixed width, so the slug (which may contain hyphens) is everything before
// it, and sorts in time order.
const trashTimeLayout = "20060102T150405.000Z"

// ErrTrashNotFound is returned for a Trash entry ID that names nothing in the
// Trash.
var ErrTrashNotFound = errors.New("not in the Trash")

// ErrSlugTaken is returned when a restore would overwrite a tutorial stored
// under the same slug since it was trashed.
var ErrSlugTaken = errors.New("a tutorial with that name is already in the library")

// TrashEntry is one tutorial in the Trash.
type TrashEntry struct {
	// ID is the entry's folder name (<slug>-<time>), the handle for Restore
	// and DeleteTrashed.
	ID        string
	Slug      string
	Title     string
	TrashedAt time.Time
	// Path is the entry's folder.
	Path string
}

// TrashPath returns the Trash folder for a tutorials dir: <parent>/trash
// (~/.lathe/trash for ~/.lathe/tutorials).
func TrashPath(tutorialsDir string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(tutorialsDir)), TrashDirName)
}

// parseTrashID splits an entry ID into its slug and time. ok is false for a
// name that isn't a Trash entry.
func parseTrashID(id string) (slug string, at time.Time, ok bool) {
	n := len(trashTimeLayout)
	if len(id) < n+2 || id[len(id)-n-1] != '-' {
		return "", time.Time{}, false
	}
	at, err := time.Parse(trashTimeLayout, id[len(id)-n:])
	if err != nil {
		return "", time.Time{}, false
	}
	slug = id[:len(id)-n-1]
	if ValidateSlug(slug) != nil {
		return "", time.Time{}, false
	}
	return slug, at.UTC(), true
}

// trashEntryPath resolves an entry ID to its folder, refusing anything that
// isn't a well-formed ID (so it can never name a path outside the Trash).
func trashEntryPath(tutorialsDir, id string) (string, string, time.Time, error) {
	slug, at, ok := parseTrashID(id)
	if !ok || strings.ContainsAny(id, `/\`) {
		return "", "", time.Time{}, fmt.Errorf("%w: %q", ErrTrashNotFound, id)
	}
	p := filepath.Join(TrashPath(tutorialsDir), id)
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		return "", "", time.Time{}, fmt.Errorf("%w: %q", ErrTrashNotFound, id)
	}
	return p, slug, at, nil
}

// existingTutorial resolves slug to its folder under tutorialsDir, which must
// exist and be a directory.
func existingTutorial(tutorialsDir, slug string) (string, error) {
	if err := ValidateSlug(slug); err != nil {
		return "", err
	}
	target := filepath.Join(tutorialsDir, slug)
	if !strings.HasPrefix(target, filepath.Clean(tutorialsDir)+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid slug: %q", slug)
	}
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("tutorial %q not found", slug)
		}
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a tutorial directory: %q", slug)
	}
	return target, nil
}

// MoveToTrash moves a stored tutorial into the Trash and returns its entry.
// Its shelf placement is kept, so RestoreTrashed puts it back where it was.
func MoveToTrash(tutorialsDir, slug string, now time.Time) (TrashEntry, error) {
	src, err := existingTutorial(tutorialsDir, slug)
	if err != nil {
		return TrashEntry{}, err
	}
	title := slug
	if t, err := ReadMetadata(src); err == nil && t.Title != "" {
		title = t.Title
	}
	trash := TrashPath(tutorialsDir)
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return TrashEntry{}, err
	}
	at := now.UTC().Truncate(time.Millisecond)
	var id, dst string
	// Two deletes of the same slug within a millisecond: step the time.
	for {
		id = slug + "-" + at.Format(trashTimeLayout)
		dst = filepath.Join(trash, id)
		if _, err := os.Lstat(dst); errors.Is(err, os.ErrNotExist) {
			break
		}
		at = at.Add(time.Millisecond)
	}
	if err := os.Rename(src, dst); err != nil {
		return TrashEntry{}, fmt.Errorf("move %s to the Trash: %w", slug, err)
	}
	return TrashEntry{ID: id, Slug: slug, Title: title, TrashedAt: at, Path: dst}, nil
}

// ListTrash returns the Trash, most recently trashed first. A missing Trash
// is empty. Folders that aren't Trash entries are ignored.
func ListTrash(tutorialsDir string) ([]TrashEntry, error) {
	trash := TrashPath(tutorialsDir)
	entries, err := os.ReadDir(trash)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []TrashEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug, at, ok := parseTrashID(e.Name())
		if !ok {
			continue
		}
		p := filepath.Join(trash, e.Name())
		title := SlugToTitle(slug)
		if t, err := ReadMetadata(p); err == nil && t.Title != "" {
			title = t.Title
		}
		out = append(out, TrashEntry{ID: e.Name(), Slug: slug, Title: title, TrashedAt: at, Path: p})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].TrashedAt.After(out[b].TrashedAt) })
	return out, nil
}

// RestoreTrashed moves a Trash entry back into the library under its slug and
// returns the slug. It refuses (ErrSlugTaken) when a tutorial with that slug
// has been stored since. The shelf placement was never removed, so the
// tutorial is back on its shelf.
func RestoreTrashed(tutorialsDir, id string) (string, error) {
	p, slug, _, err := trashEntryPath(tutorialsDir, id)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(tutorialsDir, slug)
	if _, err := os.Lstat(dst); err == nil {
		return "", fmt.Errorf("%w: %q", ErrSlugTaken, slug)
	}
	if err := os.MkdirAll(tutorialsDir, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(p, dst); err != nil {
		return "", fmt.Errorf("restore %s: %w", slug, err)
	}
	return slug, nil
}

// DeleteTrashed deletes a Trash entry permanently, and with it the slug's
// shelf placement unless a tutorial with that slug is back in the library.
func DeleteTrashed(tutorialsDir, id string) error {
	p, slug, _, err := trashEntryPath(tutorialsDir, id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(p); err != nil {
		return err
	}
	return forgetPlacement(tutorialsDir, slug)
}

// DeleteNow deletes a stored tutorial permanently, skipping the Trash, and
// drops its shelf placement.
func DeleteNow(tutorialsDir, slug string) error {
	target, err := existingTutorial(tutorialsDir, slug)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	return forgetPlacement(tutorialsDir, slug)
}

// PurgeTrash permanently deletes the Trash entries trashed more than days
// days before now and returns how many it removed. days <= 0 means keep
// forever (the "Never" setting): nothing is purged.
func PurgeTrash(tutorialsDir string, days int, now time.Time) (int, error) {
	if days <= 0 {
		return 0, nil
	}
	entries, err := ListTrash(tutorialsDir)
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	n := 0
	var errs []error
	for _, e := range entries {
		if !e.TrashedAt.Before(cutoff) {
			continue
		}
		if err := DeleteTrashed(tutorialsDir, e.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

// forgetPlacement drops slug's shelf placement once nothing in the library
// or the Trash goes by that slug any more. A missing shelves.json needs no
// change and is not created.
func forgetPlacement(tutorialsDir, slug string) error {
	if _, err := os.Stat(filepath.Join(tutorialsDir, slug)); err == nil {
		return nil
	}
	if entries, err := ListTrash(tutorialsDir); err == nil {
		for _, e := range entries {
			if e.Slug == slug {
				return nil
			}
		}
	}
	path := ShelvesPath(tutorialsDir)
	cur, err := ReadShelves(path)
	if err != nil {
		return err
	}
	if _, ok := cur.Placements[slug]; !ok {
		return nil
	}
	_, err = UpdateShelves(path, func(s *Shelves) error {
		delete(s.Placements, slug)
		return nil
	})
	return err
}

// Delete moves the user's tutorial slug to the Trash (see MoveToTrash).
func Delete(slug string) (TrashEntry, error) {
	dir, err := config.TutorialsDir()
	if err != nil {
		return TrashEntry{}, err
	}
	return MoveToTrash(dir, slug, time.Now())
}
