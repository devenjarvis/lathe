package serve

import (
	"errors"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/store"
)

// The library's write endpoints. Every one is a sameOrigin-checked POST that
// takes a form post (the page's forms, which work with scripts off and are
// answered with a 303 back to the page, carrying a ?toast= parameter) or
// JSON (answered with JSON; the page script uses this):
//
//	POST /-/delete/{slug}                      move a tutorial to the Trash
//	POST /-/trash/{id}/restore                 put it back (409 if the slug is taken)
//	POST /-/trash/{id}/delete                  delete one Trash entry for good
//	POST /-/trash/empty                        delete everything in the Trash
//	POST /-/shelves/assign      {slug, shelf}  move a tutorial to a shelf
//	POST /-/shelves/{id}/rename {name}
//	POST /-/shelves/{id}/describe {description}
//	POST /-/shelves/{id}/delete                its tutorials go to Unsorted

// readFields reads a form or JSON body of string fields. isJSON reports a
// JSON request or a client asking for JSON back. ok is false once an error
// response has been written.
func readFields(w http.ResponseWriter, r *http.Request, names ...string) (fields map[string]string, isJSON, ok bool) {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	wantsJSON := ct == "application/json" || strings.Contains(r.Header.Get("Accept"), "application/json")
	fields = map[string]string{}
	if ct == "application/json" {
		var body map[string]any
		if !readJSONBody(w, r, maxShelfBody, &body) {
			return nil, true, false
		}
		for _, n := range names {
			if v, ok := body[n].(string); ok {
				fields[n] = v
			}
		}
		return fields, true, true
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxShelfBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "could not read request", http.StatusBadRequest)
		return nil, wantsJSON, false
	}
	for _, n := range names {
		fields[n] = r.PostFormValue(n)
	}
	return fields, wantsJSON, true
}

// backTo is where a form post returns: the library page it came from (the
// Referer, when it is one of ours), with the toast parameters set. Earlier
// toast parameters are dropped, so they never stack up.
func backTo(r *http.Request, fallback string, params ...string) string {
	target := fallback
	if ref := r.Header.Get("Referer"); ref != "" && isSameOriginURL(r, ref) {
		if u, err := url.Parse(ref); err == nil && u.Path == "/" {
			target = "/?" + u.RawQuery
		}
	}
	u, err := url.Parse(target)
	if err != nil {
		u = &url.URL{Path: "/"}
	}
	q := u.Query()
	for _, k := range []string{"trashed", "restored", "moved", "to", "shelfdeleted"} {
		q.Del(k)
	}
	for i := 0; i+1 < len(params); i += 2 {
		q.Set(params[i], params[i+1])
	}
	u.RawQuery = q.Encode()
	if u.RawQuery == "" {
		return u.Path
	}
	return u.Path + "?" + u.RawQuery
}

// handleDelete moves a tutorial to the Trash. It is undoable, so the page asks
// no confirmation; the toast it returns to offers Undo.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	slug := r.PathValue("slug")
	tutDir, ok := s.safeTutorialPath(slug)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if info, err := os.Stat(tutDir); err != nil || !info.IsDir() {
		http.NotFound(w, r)
		return
	}
	entry, err := store.MoveToTrash(s.tutorialsDir, slug, time.Now())
	if err != nil {
		log.Printf("lathe: move %s to the Trash: %v", slug, err)
		http.Error(w, "could not move the tutorial to the Trash", http.StatusInternalServerError)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, map[string]string{
			"id":      entry.ID,
			"slug":    entry.Slug,
			"title":   entry.Title,
			"restore": "/-/trash/" + url.PathEscape(entry.ID) + "/restore",
		})
		return
	}
	http.Redirect(w, r, backTo(r, "/", "trashed", entry.ID), http.StatusSeeOther)
}

// trashError answers a Trash operation's error.
func trashError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrTrashNotFound):
		http.Error(w, "That tutorial is no longer in the Trash.", http.StatusNotFound)
	case errors.Is(err, store.ErrSlugTaken):
		http.Error(w, "A tutorial with the same name is in your library now. Move it to the Trash first, then restore this one.", http.StatusConflict)
	default:
		log.Printf("lathe: trash: %v", err)
		http.Error(w, "could not change the Trash", http.StatusInternalServerError)
	}
}

func (s *Server) handleTrashRestore(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	wantsJSON := strings.Contains(r.Header.Get("Accept"), "application/json")
	slug, err := store.RestoreTrashed(s.tutorialsDir, r.PathValue("id"))
	if err != nil {
		trashError(w, err)
		return
	}
	if wantsJSON {
		writeJSON(w, map[string]string{"slug": slug, "url": "/" + url.PathEscape(slug) + "/"})
		return
	}
	http.Redirect(w, r, backTo(r, "/", "restored", slug), http.StatusSeeOther)
}

func (s *Server) handleTrashDelete(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := store.DeleteTrashed(s.tutorialsDir, r.PathValue("id")); err != nil {
		trashError(w, err)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, backTo(r, viewHref("view", viewTrash)), http.StatusSeeOther)
}

func (s *Server) handleTrashEmpty(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	list, err := store.ListTrash(s.tutorialsDir)
	if err != nil {
		trashError(w, err)
		return
	}
	for _, e := range list {
		if err := store.DeleteTrashed(s.tutorialsDir, e.ID); err != nil && !errors.Is(err, store.ErrTrashNotFound) {
			trashError(w, err)
			return
		}
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, backTo(r, viewHref("view", viewTrash)), http.StatusSeeOther)
}

// updateShelves runs a shelf change and answers its error: a validation
// error (errShelfInput or a missing shelf) is the reader's 400/404, anything
// else a 500. ok is false once an error response has been written.
func (s *Server) updateShelves(w http.ResponseWriter, what string, fn func(*store.Shelves) error) (*store.Shelves, bool) {
	sh, err := store.UpdateShelves(s.shelvesPath(), func(sh *store.Shelves) error {
		if err := fn(sh); err != nil {
			if errors.Is(err, store.ErrShelfNotFound) {
				return err
			}
			return errShelfInput{err}
		}
		return nil
	})
	if err != nil {
		var bad errShelfInput
		switch {
		case errors.Is(err, store.ErrShelfNotFound):
			http.Error(w, "There's no such shelf.", http.StatusNotFound)
		case errors.As(err, &bad):
			http.Error(w, bad.err.Error(), http.StatusBadRequest)
		default:
			log.Printf("lathe: %s: %v", what, err)
			http.Error(w, "could not save shelves", http.StatusInternalServerError)
		}
		return nil, false
	}
	return sh, true
}

// handleShelfAssign moves a tutorial to a shelf: the drop of a dragged cover
// on a sidebar shelf, and the cover menu's Move to shelf.
func (s *Server) handleShelfAssign(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	f, isJSON, ok := readFields(w, r, "slug", "shelf")
	if !ok {
		return
	}
	slug, id := strings.TrimSpace(f["slug"]), strings.TrimSpace(f["shelf"])
	tutDir, ok := s.safeTutorialPath(slug)
	if !ok || store.ValidateSlug(slug) != nil {
		http.NotFound(w, r)
		return
	}
	if info, err := os.Stat(tutDir); err != nil || !info.IsDir() {
		http.NotFound(w, r)
		return
	}
	shelves, ok := s.updateShelves(w, "move tutorial", func(sh *store.Shelves) error { return sh.Assign(slug, id) })
	if !ok {
		return
	}
	on := shelves.ShelfOf(slug)
	if isJSON {
		writeJSON(w, map[string]string{"slug": slug, "shelf": on.ID, "shelfName": on.Name})
		return
	}
	http.Redirect(w, r, backTo(r, "/", "moved", slug, "to", on.ID), http.StatusSeeOther)
}

func (s *Server) handleShelfRename(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	f, isJSON, ok := readFields(w, r, "name")
	if !ok {
		return
	}
	id := r.PathValue("id")
	shelves, ok := s.updateShelves(w, "rename shelf", func(sh *store.Shelves) error { return sh.Rename(id, f["name"]) })
	if !ok {
		return
	}
	s.answerShelf(w, r, isJSON, shelves, id)
}

func (s *Server) handleShelfDescribe(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	f, isJSON, ok := readFields(w, r, "description")
	if !ok {
		return
	}
	id := r.PathValue("id")
	shelves, ok := s.updateShelves(w, "describe shelf", func(sh *store.Shelves) error { return sh.Describe(id, f["description"]) })
	if !ok {
		return
	}
	s.answerShelf(w, r, isJSON, shelves, id)
}

// answerShelf answers a shelf edit: the shelf as JSON, or a redirect to its
// page.
func (s *Server) answerShelf(w http.ResponseWriter, r *http.Request, isJSON bool, shelves *store.Shelves, id string) {
	sh, _ := shelves.Get(id)
	if isJSON {
		writeJSON(w, map[string]string{"id": sh.ID, "name": sh.Name, "description": sh.Description, "url": viewHref("shelf", sh.ID)})
		return
	}
	http.Redirect(w, r, viewHref("shelf", sh.ID), http.StatusSeeOther)
}

// handleShelfDelete removes a shelf. Its tutorials aren't touched: they move
// to Unsorted, where the redirect goes.
func (s *Server) handleShelfDelete(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	// Count the stored tutorials it held (a placement can outlive its
	// tutorial while that is in the Trash).
	stored := map[string]bool{}
	if tutorials, err := s.readTutorials(); err == nil {
		for _, t := range tutorials {
			stored[t.Slug] = true
		}
	}
	moved := 0
	if _, ok := s.updateShelves(w, "delete shelf", func(sh *store.Shelves) error {
		for slug, on := range sh.Placements {
			if on == id && stored[slug] {
				moved++
			}
		}
		return sh.Delete(id)
	}); !ok {
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, map[string]any{"moved": moved, "url": viewHref("shelf", store.UnsortedShelfID)})
		return
	}
	http.Redirect(w, r, viewHref("shelf", store.UnsortedShelfID, "shelfdeleted", strconv.Itoa(moved)), http.StatusSeeOther)
}
