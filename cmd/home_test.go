package cmd

import "testing"

// setHome points the user's home directory at dir for the test. os.UserHomeDir
// reads HOME on Unix and USERPROFILE on Windows, so set both: with HOME alone,
// a Windows run writes into the runner's real profile.
func setHome(t testing.TB, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}
