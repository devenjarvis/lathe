//go:build desktop && !darwin

package desktop

import "errors"

// relaunch: only a macOS app bundle can be upgraded under the running app
// (by `brew upgrade --cask lathe`), so only there does the update notice offer
// a restart.
func relaunch(string) error {
	return errors.New("quit Lathe and open it again to use the new version")
}
