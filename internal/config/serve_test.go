package config_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
)

func TestServeRuntimePathUnderConfigDir(t *testing.T) {
	withTempHome(t)
	path, err := config.ServeRuntimePath()
	if err != nil {
		t.Fatalf("ServeRuntimePath() error = %v", err)
	}
	if !strings.HasSuffix(path, ".lathe/serve.json") {
		t.Errorf("ServeRuntimePath() = %q, want path ending in .lathe/serve.json", path)
	}
}

func TestServeRuntimeRoundTrip(t *testing.T) {
	withTempHome(t)
	want := &config.ServeRuntime{
		URL:     "http://localhost:4242",
		PID:     4321,
		Started: time.Now().UTC().Truncate(time.Second),
	}
	if err := config.WriteServeRuntime(want); err != nil {
		t.Fatalf("WriteServeRuntime() error = %v", err)
	}
	got, err := config.ReadServeRuntime()
	if err != nil {
		t.Fatalf("ReadServeRuntime() error = %v", err)
	}
	if got.URL != want.URL || got.PID != want.PID || !got.Started.Equal(want.Started) {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

func TestReadServeRuntimeMissingIsNotExist(t *testing.T) {
	withTempHome(t)
	_, err := config.ReadServeRuntime()
	if !os.IsNotExist(err) {
		t.Errorf("ReadServeRuntime() with no file = %v, want an IsNotExist error", err)
	}
}

func TestRemoveServeRuntime(t *testing.T) {
	withTempHome(t)
	if err := config.WriteServeRuntime(&config.ServeRuntime{URL: "http://localhost:4242"}); err != nil {
		t.Fatalf("WriteServeRuntime() error = %v", err)
	}
	if err := config.RemoveServeRuntime(); err != nil {
		t.Fatalf("RemoveServeRuntime() error = %v", err)
	}
	if _, err := config.ReadServeRuntime(); !os.IsNotExist(err) {
		t.Errorf("after remove, ReadServeRuntime() = %v, want IsNotExist", err)
	}
	// Removing again (no file) must be a no-op, not an error.
	if err := config.RemoveServeRuntime(); err != nil {
		t.Errorf("RemoveServeRuntime() on missing file = %v, want nil", err)
	}
}
