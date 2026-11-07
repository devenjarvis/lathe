//go:build desktop

package desktop

import (
	"bytes"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/devenjarvis/lathe/internal/serve"
)

func TestAvailableInDesktopBuild(t *testing.T) {
	if !Available() {
		t.Fatal("a -tags desktop build should report the app available")
	}
}

func TestAppIconIs1024Square(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(appIcon))
	if err != nil {
		t.Fatalf("appicon.png: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 1024 || b.Dy() != 1024 {
		t.Errorf("appicon.png is %v, want 1024x1024", b)
	}
}

func TestLateHandler(t *testing.T) {
	var h lateHandler
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("before set: %d, want 503", rec.Code)
	}
	h.set(serve.NewServer(t.TempDir()).Handler())
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("after set: %d, want 200", rec.Code)
	}
}

func TestShellIsNative(t *testing.T) {
	var _ serve.Native = (*shell)(nil)
}
