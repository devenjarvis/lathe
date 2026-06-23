package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// ServeRuntime is the small runtime descriptor `lathe serve` writes to
// ~/.lathe/serve.json while it is running, and removes on shutdown. The worker
// CLI (`lathe work ...`) reads it to find the running server's URL (the listen
// port is configurable), and its presence doubles as "is a server running" so
// the worker can fail cleanly when there is none.
type ServeRuntime struct {
	URL     string    `json:"url"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

// ServeRuntimePath returns ~/.lathe/serve.json, creating ~/.lathe if needed.
func ServeRuntimePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "serve.json"), nil
}

// WriteServeRuntime persists rt to ~/.lathe/serve.json.
func WriteServeRuntime(rt *ServeRuntime) error {
	path, err := ServeRuntimePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(rt, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// ReadServeRuntime loads ~/.lathe/serve.json. A missing file returns os.IsNotExist
// so callers can distinguish "no server running" from a real read error.
func ReadServeRuntime() (*ServeRuntime, error) {
	path, err := ServeRuntimePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rt ServeRuntime
	if err := json.Unmarshal(data, &rt); err != nil {
		return nil, err
	}
	return &rt, nil
}

// RemoveServeRuntime deletes ~/.lathe/serve.json. A missing file is not an error.
func RemoveServeRuntime() error {
	path, err := ServeRuntimePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
