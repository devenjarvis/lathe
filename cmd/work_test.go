package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/config"
)

// pointWorkerAt writes a serve.json under a temp HOME so the work commands
// discover the given base URL, and returns nothing — the env is set for the test.
func pointWorkerAt(t *testing.T, url string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	if err := config.WriteServeRuntime(&config.ServeRuntime{URL: url}); err != nil {
		t.Fatalf("WriteServeRuntime: %v", err)
	}
}

func TestWorkNextNoServerIsCleanError(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no serve.json written
	err := workNextCmd.RunE(workNextCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "no lathe server") {
		t.Fatalf("err = %v, want a clean 'no lathe server' error", err)
	}
}

func TestWorkNextPrintsJob(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/-/work" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"7","type":"verify","slug":"demo","state":"claimed"}`)
	}))
	defer srv.Close()
	pointWorkerAt(t, srv.URL)

	var out bytes.Buffer
	workNextCmd.SetOut(&out)
	if err := workNextCmd.RunE(workNextCmd, nil); err != nil {
		t.Fatalf("work next: %v", err)
	}
	var job map[string]any
	if err := json.Unmarshal(out.Bytes(), &job); err != nil {
		t.Fatalf("output is not JSON: %v (%q)", err, out.String())
	}
	if job["id"] != "7" || job["type"] != "verify" {
		t.Errorf("job = %v, want the verify job for demo", job)
	}
}

func TestWorkNextPrintsNoTaskOn204(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	pointWorkerAt(t, srv.URL)

	var out bytes.Buffer
	workNextCmd.SetOut(&out)
	if err := workNextCmd.RunE(workNextCmd, nil); err != nil {
		t.Fatalf("work next: %v", err)
	}
	if strings.TrimSpace(out.String()) != "no task" {
		t.Errorf("output = %q, want \"no task\"", out.String())
	}
}

func TestWorkAnswerPostsAnswerFromStdin(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	pointWorkerAt(t, srv.URL)

	t.Cleanup(func() { workAnswerFile = "" })
	workAnswerFile = "-"
	workAnswerCmd.SetIn(strings.NewReader("the answer\n"))
	if err := workAnswerCmd.RunE(workAnswerCmd, []string{"42"}); err != nil {
		t.Fatalf("work answer: %v", err)
	}
	if gotPath != "/-/work/42/answer" {
		t.Errorf("path = %q, want /-/work/42/answer", gotPath)
	}
	var payload struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("body not JSON: %v (%q)", err, gotBody)
	}
	if payload.Answer != "the answer" {
		t.Errorf("answer = %q, want the trimmed stdin answer", payload.Answer)
	}
}

func TestWorkAnswerRequiresFlag(t *testing.T) {
	t.Cleanup(func() { workAnswerFile = "" })
	workAnswerFile = ""
	err := workAnswerCmd.RunE(workAnswerCmd, []string{"42"})
	if err == nil || !strings.Contains(err.Error(), "--answer is required") {
		t.Fatalf("err = %v, want a required-flag error", err)
	}
}

func TestWorkDonePosts(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	pointWorkerAt(t, srv.URL)

	if err := workDoneCmd.RunE(workDoneCmd, []string{"99"}); err != nil {
		t.Fatalf("work done: %v", err)
	}
	if gotPath != "/-/work/99/done" {
		t.Errorf("path = %q, want /-/work/99/done", gotPath)
	}
}
