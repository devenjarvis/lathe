package serve

import "testing"

type nopNative struct{}

func (nopNative) PickFolder(string, string) (string, error) { return "", nil }
func (nopNative) Notify(string, string)                      {}
func (nopNative) OpenWindow(string, string) error            { return nil }

// A plain browser server has no native shell until one is installed, and
// SetNative(nil) removes it again.
func TestSetNative(t *testing.T) {
	s := NewServer(t.TempDir())
	if s.nativeShell() != nil {
		t.Fatal("new server has a native shell")
	}
	s.SetNative(nopNative{})
	if s.nativeShell() == nil {
		t.Fatal("SetNative did not install the shell")
	}
	s.SetNative(nil)
	if s.nativeShell() != nil {
		t.Fatal("SetNative(nil) did not remove the shell")
	}
}
