package update

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Interval is how often automatic checks ask GitHub: at most once a day.
const Interval = 24 * time.Hour

// minManualInterval keeps a burst of Check for updates… clicks from spending
// GitHub's unauthenticated rate limit: a click this soon after a lookup
// reuses its answer.
const minManualInterval = time.Minute

// Notice kinds.
const (
	// KindDownload: a newer release is out; Download opens its page.
	KindDownload = "download"
	// KindHomebrew: a newer release is out and this is a cask install; the
	// notice shows BrewCommand.
	KindHomebrew = "homebrew"
	// KindRestart: the bundle on disk is already newer than the running
	// app (`brew upgrade` replaced it); a restart picks it up.
	KindRestart = "restart"
)

// Notice is the title-bar notice: "Lathe 0.9 is out · Download",
// "Lathe 0.9 is out · Run brew upgrade --cask lathe", or
// "Lathe 0.9 is ready · Restart".
type Notice struct {
	Kind string
	// Version is the full version ("0.9.0"); Label is how the notice says
	// it ("0.9").
	Version, Label string
	// URL is the release page (KindDownload, KindHomebrew).
	URL string
	// Command is BrewCommand (KindHomebrew).
	Command string
}

// Result is one check's outcome, for Check for updates….
type Result struct {
	// Current is the running version as buildinfo reports it.
	Current string
	// Dev is a development build: nothing was looked up.
	Dev bool
	// Latest is the newest release (zero for a development build).
	Latest Release
	// Notice is the newer version, if any: what the title bar would show,
	// even if the reader hid it there (they asked). nil when up to date.
	Notice *Notice
}

// state is the cache file, ~/.lathe/update-check.json.
type state struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    Release   `json:"latest"`
	// Dismissed is the version whose notice the reader closed; a newer one
	// shows again.
	Dismissed string `json:"dismissed,omitempty"`
}

// Checker looks up releases and words the notice. Its fields are set before
// first use; the zero value of each optional field is a sensible default.
type Checker struct {
	// Current is the running version (buildinfo.Resolve()).
	Current string
	// Fetch looks up the newest release.
	Fetch Fetcher
	// CachePath is where the last answer and the dismissed version are
	// kept between launches; "" keeps them in memory only.
	CachePath string
	// Install is how this app was installed (Detect()).
	Install Install
	// Now is the clock (nil: time.Now).
	Now func() time.Time
	// ReadFile reads the bundle's Info.plist (nil: os.ReadFile).
	ReadFile func(string) ([]byte, error)

	fetchMu sync.Mutex // one lookup at a time
	mu      sync.Mutex
	loaded  bool
	st      state
}

// Dev reports a development build, which never checks.
func (c *Checker) Dev() bool {
	_, ok := Parse(c.Current)
	return !ok
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// load reads the cache once; c.mu is held.
func (c *Checker) load() {
	if c.loaded {
		return
	}
	c.loaded = true
	if c.CachePath == "" {
		return
	}
	b, err := os.ReadFile(c.CachePath)
	if err != nil {
		return
	}
	var st state
	if json.Unmarshal(b, &st) == nil {
		c.st = st
	}
}

// save writes the cache; c.mu is held. Best effort: a read-only home only
// costs an extra lookup next launch.
func (c *Checker) save() {
	if c.CachePath == "" {
		return
	}
	b, err := json.MarshalIndent(c.st, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(c.CachePath), 0o755)
	tmp := c.CachePath + ".tmp"
	if os.WriteFile(tmp, append(b, '\n'), 0o644) == nil {
		_ = os.Rename(tmp, c.CachePath)
	}
}

// Due reports whether an automatic check should ask GitHub now: a release
// build whose last lookup is a day old (or never happened, or is in the
// future because the clock moved).
func (c *Checker) Due() bool {
	if c.Dev() {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	age := c.now().Sub(c.st.CheckedAt)
	return c.st.CheckedAt.IsZero() || age >= Interval || age < 0
}

// ErrDev is returned by Check for a development build.
var ErrDev = errors.New("this is a development build, which doesn't check for updates")

// Check looks up the newest release, unless the cached answer is fresh
// enough: a day for an automatic check (force false), a minute for Check for
// updates… (force true). A development build returns ErrDev without a
// lookup. On a failed lookup the cache is left as it was.
func (c *Checker) Check(ctx context.Context, force bool) (Result, error) {
	res := Result{Current: c.Current, Dev: c.Dev()}
	if res.Dev {
		return res, ErrDev
	}
	if c.Fetch == nil {
		return res, errors.New("no release source")
	}
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()

	c.mu.Lock()
	c.load()
	fresh := Interval
	if force {
		fresh = minManualInterval
	}
	age := c.now().Sub(c.st.CheckedAt)
	cached := !c.st.CheckedAt.IsZero() && age >= 0 && age < fresh && c.st.Latest.Version != ""
	c.mu.Unlock()

	if !cached {
		rel, err := c.Fetch(ctx)
		if err != nil {
			return c.result(), err
		}
		c.mu.Lock()
		c.st.CheckedAt = c.now()
		c.st.Latest = rel
		c.save()
		c.mu.Unlock()
	}
	return c.result(), nil
}

// Dismiss hides the notice for version until a newer one is out (or, for a
// restart notice, until the bundle changes again).
func (c *Checker) Dismiss(version string) error {
	if _, ok := Parse(version); !ok {
		return errors.New("not a release version")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	c.st.Dismissed = version
	c.save()
	return nil
}

func (c *Checker) result() Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Result{Current: c.Current, Latest: c.st.Latest, Notice: c.noticeLocked(false)}
}

// Notice is the title-bar notice from what is already known (the cache and
// the bundle on disk); it never reaches the network. nil: nothing to say.
func (c *Checker) Notice() *Notice {
	if c.Dev() {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	return c.noticeLocked(true)
}

// noticeLocked words the notice; c.mu is held. dismissable: leave out a
// version the reader hid.
func (c *Checker) noticeLocked(dismissable bool) *Notice {
	hidden := func(v string) bool { return dismissable && c.st.Dismissed == v }
	// The app on disk was replaced by a newer one: restart into it.
	if on := BundleVersion(c.Install.Bundle, c.ReadFile); Newer(on, c.Current) {
		v, _ := Parse(on)
		if !hidden(v.String()) {
			return &Notice{Kind: KindRestart, Version: v.String(), Label: v.Short()}
		}
		return nil
	}
	latest := c.st.Latest
	if !Newer(latest.Version, c.Current) || hidden(latest.Version) {
		return nil
	}
	v, _ := Parse(latest.Version)
	n := &Notice{Kind: KindDownload, Version: v.String(), Label: v.Short(), URL: latest.URL}
	if n.URL == "" {
		n.URL = ReleasesPage
	}
	if c.Install.Homebrew {
		n.Kind, n.Command = KindHomebrew, BrewCommand
	}
	return n
}
