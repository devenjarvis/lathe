package serve

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/update"
)

// fakeUpdates is a canned update checker.
type fakeUpdates struct {
	mu         sync.Mutex
	notice     *update.Notice
	dismissed  []string
	restarts   int
	restartErr error
}

func (f *fakeUpdates) Notice() *update.Notice {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.notice
}

func (f *fakeUpdates) Dismiss(v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := update.Parse(v); !ok {
		return errors.New("not a release version")
	}
	f.dismissed = append(f.dismissed, v)
	f.notice = nil
	return nil
}

func (f *fakeUpdates) Restart() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts++
	return f.restartErr
}

func TestUpdateNoticeRenders(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })

	for _, p := range []string{"/", "/series/part-01.md"} {
		if strings.Contains(get(t, ts, p), `id="updateNotice"`) {
			t.Errorf("%s shows an update notice without a checker", p)
		}
	}

	srv.SetNative(newFakeNative())
	fu := &fakeUpdates{}
	srv.SetUpdates(fu)
	for _, p := range []string{"/", "/series/part-01.md"} {
		if strings.Contains(get(t, ts, p), `id="updateNotice"`) {
			t.Errorf("%s shows an update notice when there is nothing to say", p)
		}
	}

	cases := []struct {
		n    update.Notice
		want []string
		not  []string
	}{
		{
			update.Notice{Kind: update.KindDownload, Version: "0.9.0", Label: "0.9", URL: "https://github.com/devenjarvis/lathe/releases/tag/v0.9.0"},
			[]string{`Lathe 0.9<span class="update-notice-state"> is available`, `href="https://github.com/devenjarvis/lathe/releases/tag/v0.9.0"`, ">Download</a>", `data-version="0.9.0"`, "data-update-dismiss"},
			[]string{">Restart</button>", "Run <code>"},
		},
		{
			update.Notice{Kind: update.KindHomebrew, Version: "0.9.0", Label: "0.9", URL: update.ReleasesPage, Command: update.BrewCommand},
			[]string{`Lathe 0.9<span class="update-notice-state"> is available`, "Run <code>brew upgrade --cask lathe</code>", `data-update-copy="brew upgrade --cask lathe"`},
			[]string{">Download</a>", ">Restart</button>"},
		},
		{
			update.Notice{Kind: update.KindRestart, Version: "0.9.0", Label: "0.9"},
			[]string{`Lathe 0.9<span class="update-notice-state"> is ready`, ">Restart</button>"},
			[]string{">Download</a>", "Run <code>"},
		},
	}
	for _, c := range cases {
		n := c.n
		fu.mu.Lock()
		fu.notice = &n
		fu.mu.Unlock()
		for _, p := range []string{"/", "/series/part-01.md"} {
			page := get(t, ts, p)
			for _, w := range c.want {
				if !strings.Contains(page, w) {
					t.Errorf("%s notice on %s is missing %q", n.Kind, p, w)
				}
			}
			for _, w := range c.not {
				if strings.Contains(page, w) {
					t.Errorf("%s notice on %s has %q", n.Kind, p, w)
				}
			}
		}
	}
}

func TestNativeUpdateEndpoint(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	const body = `{"action":"dismiss","version":"0.9.0"}`

	if w := nativePost(t, srv, "/-/native/update", body, ""); w.Code != http.StatusNotFound {
		t.Errorf("without a shell = %d, want 404", w.Code)
	}
	srv.SetNative(newFakeNative())
	if w := nativePost(t, srv, "/-/native/update", body, ""); w.Code != http.StatusNotFound {
		t.Errorf("without a checker = %d, want 404", w.Code)
	}

	fu := &fakeUpdates{notice: &update.Notice{Kind: update.KindDownload, Version: "0.9.0", Label: "0.9"}}
	srv.SetUpdates(fu)
	if w := nativePost(t, srv, "/-/native/update", body, "https://evil.example"); w.Code != http.StatusForbidden {
		t.Errorf("cross-site = %d, want 403", w.Code)
	}
	// A download notice can't be turned into a quit.
	if w := nativePost(t, srv, "/-/native/update", `{"action":"restart"}`, ""); w.Code != http.StatusConflict || fu.restarts != 0 {
		t.Errorf("restart without an upgraded app = %d (%d restarts), want 409", w.Code, fu.restarts)
	}
	if w := nativePost(t, srv, "/-/native/update", `{"action":"explode"}`, ""); w.Code != http.StatusBadRequest {
		t.Errorf("unknown action = %d, want 400", w.Code)
	}
	if w := nativePost(t, srv, "/-/native/update", `{"action":"dismiss","version":"dev"}`, ""); w.Code != http.StatusBadRequest {
		t.Errorf("dismiss a non-version = %d, want 400", w.Code)
	}
	if w := nativePost(t, srv, "/-/native/update", body, ""); w.Code != http.StatusNoContent || len(fu.dismissed) != 1 || fu.dismissed[0] != "0.9.0" {
		t.Errorf("dismiss = %d, dismissed %v", w.Code, fu.dismissed)
	}

	fu.notice = &update.Notice{Kind: update.KindRestart, Version: "0.9.0", Label: "0.9"}
	if w := nativePost(t, srv, "/-/native/update", `{"action":"restart"}`, ""); w.Code != http.StatusAccepted || fu.restarts != 1 {
		t.Errorf("restart = %d (%d restarts), want 202", w.Code, fu.restarts)
	}
	fu.restartErr = errors.New("no bundle")
	if w := nativePost(t, srv, "/-/native/update", `{"action":"restart"}`, ""); w.Code != http.StatusInternalServerError {
		t.Errorf("failed restart = %d, want 500", w.Code)
	}
}

func TestSettingsUpdateCheckToggle(t *testing.T) {
	_, ts, _ := settingsServer(t)
	page := get(t, ts, "/-/settings")
	if !strings.Contains(page, `data-setting="update_check" checked`) {
		t.Error("Settings › Library has no update_check toggle, on by default")
	}
	if code, body := postSettings(t, ts, "/-/settings", `{"update_check":false}`); code != http.StatusOK {
		t.Fatalf("POST update_check = %d %s", code, body)
	}
	if config.UpdateCheck() {
		t.Error("update_check:false did not save")
	}
	if c, _ := config.ReadConfig(); c.UpdateCheck == nil || *c.UpdateCheck {
		t.Errorf("config.json update_check = %v", c.UpdateCheck)
	}
	if code, _ := postSettings(t, ts, "/-/settings", `{"update_check":true}`); code != http.StatusOK || !config.UpdateCheck() {
		t.Error("update_check:true did not save")
	}
	if c, _ := config.ReadConfig(); c.UpdateCheck != nil {
		t.Error("the default (on) should leave the key out of config.json")
	}
}
