package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	ok := map[string]string{
		"v0.9.0":      "0.9.0",
		"0.9.0":       "0.9.0",
		"1.2.3-rc.1":  "1.2.3-rc.1",
		"v1.2.3-beta": "1.2.3-beta",
		" 2.0.10 ":    "2.0.10",
	}
	for in, want := range ok {
		v, good := Parse(in)
		if !good || v.String() != want {
			t.Errorf("Parse(%q) = %v, %v; want %s", in, v, good, want)
		}
	}
	dev := []string{
		"dev", "", "(devel)", "0.0.0", "v0.0.0",
		"v0.3.0-5-gabc1234", "v0.3.0-5-gabc1234-dirty", "abc1234",
		"v0.0.0-20261107120000-abcdef123456", "0.4.1-snapshot-abc1234",
		"1.2", "01.2.3", "v1.2.3+meta",
	}
	for _, in := range dev {
		if v, good := Parse(in); good {
			t.Errorf("Parse(%q) = %v; want a development build", in, v)
		}
	}
}

func TestCompareAndNewer(t *testing.T) {
	order := []string{"0.8.9", "0.9.0-alpha", "0.9.0-beta.2", "0.9.0-rc.1", "0.9.0-rc.2", "0.9.0-rc.10", "0.9.0", "0.9.1", "0.10.0", "1.0.0"}
	for i := range order {
		for j := range order {
			a, _ := Parse(order[i])
			b, _ := Parse(order[j])
			want := sign(i - j)
			if got := Compare(a, b); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	cases := []struct {
		cand, cur string
		want      bool
	}{
		{"0.9.0", "v0.8.0", true},
		{"v0.9.0", "0.9.0", false},
		{"0.9.0", "0.9.0-rc.1", true},
		{"0.8.0", "0.9.0", false},
		{"0.9.0", "dev", false},
		{"0.9.0", "v0.8.0-3-gabc", false},
		{"nonsense", "0.8.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.cand, c.cur); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.cand, c.cur, got, c.want)
		}
	}
	if s := (Version{Major: 0, Minor: 9}).Short(); s != "0.9" {
		t.Errorf("Short = %q", s)
	}
	if s := (Version{Major: 0, Minor: 9, Patch: 1}).Short(); s != "0.9.1" {
		t.Errorf("Short = %q", s)
	}
}

func TestDetectInstall(t *testing.T) {
	none := func(string) bool { return false }
	caskAt := func(prefix string) func(string) bool {
		return func(p string) bool { return p == filepath.Join(prefix, "Caskroom", "lathe") }
	}
	cases := []struct {
		name   string
		exe    string
		exists func(string) bool
		extra  []string
		want   Install
	}{
		{"plain binary", "/usr/local/bin/lathe", caskAt("/usr/local"), nil, Install{}},
		{"go install", "/Users/me/go/bin/lathe", none, nil, Install{}},
		{"downloaded zip", "/Applications/Lathe.app/Contents/MacOS/lathe", none, nil, Install{Bundle: "/Applications/Lathe.app"}},
		{"cask, Apple silicon", "/Applications/Lathe.app/Contents/MacOS/lathe", caskAt("/opt/homebrew"), nil, Install{Bundle: "/Applications/Lathe.app", Homebrew: true}},
		{"cask, Intel", "/Applications/Lathe.app/Contents/MacOS/lathe", caskAt("/usr/local"), nil, Install{Bundle: "/Applications/Lathe.app", Homebrew: true}},
		{"cask, custom prefix", "/Users/me/Applications/Lathe.app/Contents/MacOS/lathe", caskAt("/Users/me/brew"), []string{"/Users/me/brew"}, Install{Bundle: "/Users/me/Applications/Lathe.app", Homebrew: true}},
		{"bundle inside the Caskroom", "/opt/homebrew/Caskroom/lathe/0.9.0/Lathe.app/Contents/MacOS/lathe", none, nil, Install{Bundle: "/opt/homebrew/Caskroom/lathe/0.9.0/Lathe.app", Homebrew: true}},
		{"not a bundle layout", "/Applications/Lathe.app/lathe", caskAt("/opt/homebrew"), nil, Install{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DetectInstall(filepath.FromSlash(c.exe), c.exists, c.extra...)
			c.want.Bundle = filepath.FromSlash(c.want.Bundle)
			if got != c.want {
				t.Errorf("DetectInstall(%s) = %+v, want %+v", c.exe, got, c.want)
			}
		})
	}
}

func plist(short, full string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
	<key>CFBundleShortVersionString</key>
	<string>` + short + `</string>
	<key>CFBundleGetInfoString</key>
	<string>Lathe ` + full + `</string>
</dict></plist>`)
}

func TestBundleVersion(t *testing.T) {
	read := func(b []byte) func(string) ([]byte, error) {
		return func(p string) ([]byte, error) {
			if filepath.Base(p) != "Info.plist" {
				t.Errorf("read %s", p)
			}
			return b, nil
		}
	}
	if v := BundleVersion("/A/Lathe.app", read(plist("1.0.0", "1.0.0-rc.1"))); v != "1.0.0-rc.1" {
		t.Errorf("full version = %q", v)
	}
	if v := BundleVersion("/A/Lathe.app", read([]byte(`<key>CFBundleShortVersionString</key><string>0.9.0</string>`))); v != "0.9.0" {
		t.Errorf("short fallback = %q", v)
	}
	if v := BundleVersion("", read(plist("1.0.0", "1.0.0"))); v != "" {
		t.Errorf("no bundle = %q", v)
	}
	if v := BundleVersion("/A/Lathe.app", func(string) ([]byte, error) { return nil, os.ErrNotExist }); v != "" {
		t.Errorf("unreadable = %q", v)
	}
}

func TestGitHubFetcher(t *testing.T) {
	var gotUA, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotAccept = r.Header.Get("User-Agent"), r.Header.Get("Accept")
		if r.Header.Get("Authorization") != "" {
			t.Error("sent an Authorization header")
		}
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte(`{"tag_name":"v0.9.0","html_url":"https://github.com/devenjarvis/lathe/releases/tag/v0.9.0","published_at":"2026-11-01T10:00:00Z"}`))
		case "/odd-url":
			_, _ = w.Write([]byte(`{"tag_name":"v0.9.0","html_url":"https://evil.example/x"}`))
		case "/bad-tag":
			_, _ = w.Write([]byte(`{"tag_name":"nightly"}`))
		case "/limited":
			w.WriteHeader(http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	rel, err := GitHub(srv.Client(), srv.URL+"/ok", "v0.8.0")(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "0.9.0" || !strings.HasSuffix(rel.URL, "/tag/v0.9.0") || rel.Published.IsZero() {
		t.Errorf("release = %+v", rel)
	}
	if gotUA != "Lathe/0.8.0" || gotAccept != "application/vnd.github+json" {
		t.Errorf("headers: UA %q Accept %q", gotUA, gotAccept)
	}
	if rel, err := GitHub(srv.Client(), srv.URL+"/odd-url", "0.8.0")(context.Background()); err != nil || rel.URL != ReleasesPage {
		t.Errorf("odd url: %+v %v", rel, err)
	}
	for _, p := range []string{"/bad-tag", "/limited", "/missing"} {
		if _, err := GitHub(srv.Client(), srv.URL+p, "0.8.0")(context.Background()); err == nil {
			t.Errorf("%s: want an error", p)
		}
	}
}

// fakeFetch counts lookups and returns rel (or err).
type fakeFetch struct {
	calls int
	rel   Release
	err   error
}

func (f *fakeFetch) fetch(context.Context) (Release, error) {
	f.calls++
	return f.rel, f.err
}

func TestCheckerCachesADay(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	f := &fakeFetch{rel: Release{Version: "0.9.0", URL: "https://github.com/devenjarvis/lathe/releases/tag/v0.9.0"}}
	cache := filepath.Join(t.TempDir(), "update-check.json")
	c := &Checker{Current: "0.8.0", Fetch: f.fetch, CachePath: cache, Now: func() time.Time { return now }}

	if !c.Due() {
		t.Fatal("a first check should be due")
	}
	res, err := c.Check(context.Background(), false)
	if err != nil || f.calls != 1 {
		t.Fatalf("first check: %v, %d calls", err, f.calls)
	}
	if res.Notice == nil || res.Notice.Kind != KindDownload || res.Notice.Label != "0.9" {
		t.Fatalf("notice = %+v", res.Notice)
	}
	if c.Due() {
		t.Error("due right after a check")
	}
	now = now.Add(23 * time.Hour)
	if _, err := c.Check(context.Background(), false); err != nil || f.calls != 1 {
		t.Errorf("an automatic check within a day looked up again (%d calls)", f.calls)
	}
	// A manual check a minute later does ask.
	if _, err := c.Check(context.Background(), true); err != nil || f.calls != 2 {
		t.Errorf("a manual check reused a 23h-old answer (%d calls)", f.calls)
	}
	// ...but not twice in a minute.
	if _, err := c.Check(context.Background(), true); err != nil || f.calls != 2 {
		t.Errorf("two manual checks in a minute both looked up (%d calls)", f.calls)
	}
	now = now.Add(Interval)
	if !c.Due() {
		t.Error("not due after a day")
	}

	// A new Checker on the same cache (the next launch) knows the answer
	// without a lookup.
	c2 := &Checker{Current: "0.8.0", Fetch: f.fetch, CachePath: cache, Now: func() time.Time { return now.Add(-time.Hour) }}
	if n := c2.Notice(); n == nil || n.Version != "0.9.0" {
		t.Errorf("cached notice = %+v", n)
	}
	if c2.Due() {
		t.Error("cache from an hour ago is due")
	}
}

func TestCheckerFailureKeepsCache(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	f := &fakeFetch{rel: Release{Version: "0.9.0"}}
	c := &Checker{Current: "0.8.0", Fetch: f.fetch, Now: func() time.Time { return now }}
	if _, err := c.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * Interval)
	f.err = errors.New("offline")
	res, err := c.Check(context.Background(), false)
	if err == nil {
		t.Fatal("want the fetch error")
	}
	if res.Notice == nil || res.Notice.URL != ReleasesPage {
		t.Errorf("the cached notice went away on a failed lookup: %+v", res.Notice)
	}
	if !c.Due() {
		t.Error("a failed lookup should leave the check due")
	}
}

func TestCheckerDevAndCurrent(t *testing.T) {
	f := &fakeFetch{rel: Release{Version: "0.9.0"}}
	dev := &Checker{Current: "v0.8.0-4-gabc1234-dirty", Fetch: f.fetch}
	if _, err := dev.Check(context.Background(), true); !errors.Is(err, ErrDev) || f.calls != 0 {
		t.Errorf("dev build: %v, %d calls", err, f.calls)
	}
	if dev.Due() || dev.Notice() != nil {
		t.Error("a dev build is never due and shows no notice")
	}
	cur := &Checker{Current: "v0.9.0", Fetch: f.fetch}
	res, err := cur.Check(context.Background(), true)
	if err != nil || res.Notice != nil || res.Latest.Version != "0.9.0" {
		t.Errorf("up to date: %+v %v", res, err)
	}
}

func TestCheckerHomebrewRestartAndDismiss(t *testing.T) {
	f := &fakeFetch{rel: Release{Version: "0.9.0", URL: "https://github.com/devenjarvis/lathe/releases/tag/v0.9.0"}}
	onDisk := plist("0.8.0", "0.8.0")
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	c := &Checker{
		Now:       func() time.Time { return now },
		Current:   "0.8.0",
		Fetch:     f.fetch,
		CachePath: filepath.Join(t.TempDir(), "u.json"),
		Install:   Install{Bundle: "/Applications/Lathe.app", Homebrew: true},
		ReadFile:  func(string) ([]byte, error) { return onDisk, nil },
	}
	res, err := c.Check(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if n := res.Notice; n == nil || n.Kind != KindHomebrew || n.Command != BrewCommand {
		t.Fatalf("homebrew notice = %+v", n)
	}

	// The reader ran brew upgrade: the bundle under the running app is new.
	onDisk = plist("0.9.0", "0.9.0")
	if n := c.Notice(); n == nil || n.Kind != KindRestart || n.Label != "0.9" {
		t.Fatalf("restart notice = %+v", n)
	}

	if err := c.Dismiss("0.9.0"); err != nil {
		t.Fatal(err)
	}
	if n := c.Notice(); n != nil {
		t.Errorf("dismissed notice still shows: %+v", n)
	}
	// Check for updates… still reports it: the reader asked.
	if res, err := c.Check(context.Background(), true); err != nil || res.Notice == nil || res.Notice.Kind != KindRestart {
		t.Errorf("manual check after a dismissal: %+v %v", res.Notice, err)
	}
	if err := c.Dismiss("dev"); err == nil {
		t.Error("dismissed a non-version")
	}

	// A later release shows again despite the old dismissal.
	onDisk = plist("0.8.0", "0.8.0")
	f.rel.Version = "0.9.1"
	now = now.Add(2 * time.Minute)
	if _, err := c.Check(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if n := c.Notice(); n == nil || n.Version != "0.9.1" {
		t.Errorf("newer release after a dismissal: %+v", n)
	}
}
