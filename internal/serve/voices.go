package serve

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/devenjarvis/lathe/internal/voice"
)

// Settings › Voices (issue #140): list, compare, set the default, duplicate a
// built-in, and delete a custom voice with Undo. The CLI stays the owner of
// the voice files: these handlers call the same internal/voice and
// internal/config functions `lathe voice …` does.
//
//	POST /-/voices/default    {"name"}          → {"default"}
//	POST /-/voices/duplicate  {"from","name"}   → {"name","path","command"}
//	POST /-/voices/delete     {"name"}          → {"name","wasDefault","default"}
//	POST /-/voices/restore    {"name"}          → {"name","default"}  (Undo, within voiceUndoWindow)
//
// Show file reuses POST /-/native/reveal {"what":"voices"} (the voices
// folder). Every POST is sameOrigin-checked.

// voiceUndoWindow is how long a deleted voice can be restored. The toast's
// Undo button shows for a few seconds; the server keeps the text a little
// longer so a slow click still lands.
const voiceUndoWindow = 2 * time.Minute

// deletedVoice is a voice kept in memory after Delete, for Undo.
type deletedVoice struct {
	raw        []byte
	wasDefault bool
	at         time.Time
}

// voiceTrash holds recently deleted voices for Undo. It lives in memory: a
// restart forgets it, which only shortens the undo window.
type voiceTrash struct {
	mu      sync.Mutex
	entries map[string]deletedVoice
}

func (t *voiceTrash) put(name string, d deletedVoice) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.entries == nil {
		t.entries = map[string]deletedVoice{}
	}
	for k, e := range t.entries {
		if time.Since(e.at) > voiceUndoWindow {
			delete(t.entries, k)
		}
	}
	t.entries[name] = d
}

func (t *voiceTrash) take(name string) (deletedVoice, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d, ok := t.entries[name]
	delete(t.entries, name)
	if ok && time.Since(d.at) > voiceUndoWindow {
		return deletedVoice{}, false
	}
	return d, ok
}

// settingsVoice is one voice on the Voices tab.
type settingsVoice struct {
	Name, Description string
	Builtin, Default  bool
	// Selected is the voice the detail pane opens on: ?voice=, else the
	// default.
	Selected bool
	// Sample is the spec's "## Sample" paragraphs, as plain text.
	Sample []string
	// UsedBy counts the stored tutorials that recorded this voice.
	UsedBy int
	// Path and PathShort are a custom voice's file.
	Path, PathShort string
}

// settingsVoices builds the Voices tab: built-ins first, then the reader's
// own, each group by name. selected (else the default) is marked Selected
// when pick is set; pick is false while another pane (New voice, Compare)
// is open.
func (s *Server) settingsVoices(selected string, pick bool) (builtin, yours []settingsVoice) {
	list, err := voice.List()
	if err != nil {
		return nil, nil
	}
	def := store.NormalizeVoice(config.DefaultVoice())
	selected = store.NormalizeVoice(selected)
	found := false
	for _, v := range list {
		if v.Name == selected {
			found = true
		}
	}
	if !found {
		selected = def
	}
	used := map[string]int{}
	if tuts, err := s.readTutorials(); err == nil {
		for _, t := range tuts {
			if n := store.NormalizeVoice(t.Voice); n != "" {
				used[n]++
			}
		}
	}
	for _, v := range list {
		sv := settingsVoice{
			Name: v.Name, Description: v.Description, Builtin: v.Builtin,
			Default: v.Name == def, Selected: pick && v.Name == selected,
			Sample: paragraphs(v.Sample()), UsedBy: used[v.Name],
		}
		if v.Builtin {
			builtin = append(builtin, sv)
			continue
		}
		if p, err := voice.Path(v.Name); err == nil {
			sv.Path, sv.PathShort = p, displayPath(p)
		}
		yours = append(yours, sv)
	}
	return builtin, yours
}

// voiceCommand is the /lathe-voice invocation that refines a voice.
func voiceCommand(name string) string { return "/lathe-voice " + name }

func (s *Server) handleVoiceDefault(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var p struct {
		Name string `json:"name"`
	}
	if !readJSONBody(w, r, 4<<10, &p) {
		return
	}
	v, err := voice.Resolve(p.Name)
	if err != nil {
		http.Error(w, "There's no voice called "+strings.TrimSpace(p.Name)+".", http.StatusNotFound)
		return
	}
	if err := setDefaultVoice(v.Name); err != nil {
		http.Error(w, "could not save: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"default": v.Name})
}

// setDefaultVoice records name as the default through the locked
// read-modify-write the Settings window uses, so it can't drop a setting
// another window saved at the same moment.
func setDefaultVoice(name string) error {
	return config.UpdateConfig(func(c *config.Config) error {
		c.DefaultVoice = name
		return nil
	})
}

func (s *Server) handleVoiceDuplicate(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var p struct {
		From string `json:"from"`
		Name string `json:"name"`
	}
	if !readJSONBody(w, r, 4<<10, &p) {
		return
	}
	if _, err := voice.Resolve(p.From); err != nil {
		http.Error(w, "There's no voice called "+strings.TrimSpace(p.From)+".", http.StatusNotFound)
		return
	}
	v, err := voice.Duplicate(p.From, p.Name)
	switch {
	case errors.Is(err, voice.ErrExists):
		http.Error(w, "There's already a voice called "+strings.ToLower(strings.TrimSpace(p.Name))+". Pick another name.", http.StatusConflict)
		return
	case err != nil:
		http.Error(w, capitalize(err.Error())+".", http.StatusBadRequest)
		return
	}
	path, _ := voice.Path(v.Name)
	writeJSON(w, map[string]string{"name": v.Name, "path": path, "command": voiceCommand(v.Name)})
}

func (s *Server) handleVoiceDelete(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var p struct {
		Name string `json:"name"`
	}
	if !readJSONBody(w, r, 4<<10, &p) {
		return
	}
	v, err := voice.Resolve(p.Name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if v.Builtin {
		http.Error(w, "Built-in voices can't be deleted.", http.StatusBadRequest)
		return
	}
	// The same rule as `lathe voice rm`: deleting the default resets it to
	// the built-in default, so new tutorials never name a missing voice.
	// Tutorials that recorded the voice keep the name; their byline shows it
	// without the spec (server.go degrades when Resolve fails).
	wasDefault := strings.EqualFold(config.DefaultVoice(), v.Name)
	if err := voice.Remove(v.Name); err != nil {
		http.Error(w, "could not delete: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.voiceTrash.put(v.Name, deletedVoice{raw: v.Raw, wasDefault: wasDefault, at: time.Now()})
	if wasDefault {
		if err := setDefaultVoice(""); err != nil {
			http.Error(w, "deleted, but could not reset the default: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, map[string]any{"name": v.Name, "wasDefault": wasDefault, "default": config.DefaultVoice()})
}

func (s *Server) handleVoiceRestore(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var p struct {
		Name string `json:"name"`
	}
	if !readJSONBody(w, r, 4<<10, &p) {
		return
	}
	name := store.NormalizeVoice(p.Name)
	d, ok := s.voiceTrash.take(name)
	if !ok {
		http.Error(w, "It's too late to undo that.", http.StatusGone)
		return
	}
	if _, err := voice.Resolve(name); err == nil {
		http.Error(w, "A voice called "+name+" exists again, so it wasn't restored.", http.StatusConflict)
		return
	}
	if err := voice.Add(name, d.raw); err != nil {
		http.Error(w, "could not restore: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if d.wasDefault {
		if err := setDefaultVoice(name); err != nil {
			http.Error(w, "restored, but could not make it the default again: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, map[string]string{"name": name, "default": config.DefaultVoice()})
}

// voicesDirForReveal is the folder Show file opens.
func voicesDirForReveal() (string, error) {
	dir, err := config.VoicesDir()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// paragraphs splits text on blank lines and joins each paragraph's lines,
// for showing a sample as plain prose.
func paragraphs(text string) []string {
	var out []string
	for _, p := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		if p = strings.Join(strings.Fields(p), " "); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// voicesDirDisplay is the voices folder in its ~ form, for the Voices tab.
func voicesDirDisplay() string {
	dir, err := config.VoicesDir()
	if err != nil {
		return "~/.lathe/voices"
	}
	return displayPath(dir)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
