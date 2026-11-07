package desktop

import (
	"errors"
	"strings"

	"github.com/devenjarvis/lathe/internal/update"
)

// What Lathe › Check for updates… says. Plain Go, so it is tested without the
// desktop tag; updates.go shows it with Wails.

// Update dialog actions: what its main button does.
const (
	updateActDownload = "download" // open the release page
	updateActCopy     = "copy"     // copy the brew command
	updateActRestart  = "restart"  // reopen the upgraded app
)

// updateDialog is one Check for updates… answer.
type updateDialog struct {
	Title, Message string
	// Warning: the check failed (a warning dialog, not an info one).
	Warning bool
	// Button is the main button's label ("" for OK alone), Action what it
	// does, and Arg its URL or command.
	Button, Action, Arg string
	// Dismiss is the other button's label.
	Dismiss string
}

// updateDialogFor words a check's outcome.
func updateDialogFor(res update.Result, err error) updateDialog {
	current := strings.TrimPrefix(res.Current, "v")
	switch {
	case errors.Is(err, update.ErrDev) || res.Dev:
		return updateDialog{
			Title:   "Development build",
			Message: "This is a development build of Lathe (" + res.Current + "), so it doesn't check for updates. Release builds look for new versions on GitHub.",
			Dismiss: "OK",
		}
	case err != nil:
		return updateDialog{
			Title:   "Couldn't check for updates",
			Message: "Lathe couldn't get the newest release from GitHub: " + err.Error() + ". Try again later, or look on the releases page.",
			Warning: true,
			Button:  "Open releases page", Action: updateActDownload, Arg: update.ReleasesPage,
			Dismiss: "OK",
		}
	}
	n := res.Notice
	if n == nil {
		return updateDialog{
			Title:   "You're up to date",
			Message: "Lathe " + current + " is the newest version.",
			Dismiss: "OK",
		}
	}
	switch n.Kind {
	case update.KindRestart:
		return updateDialog{
			Title:   "Lathe " + n.Label + " is ready",
			Message: "Lathe " + n.Version + " is installed. Restart Lathe to start using it.",
			Button:  "Restart", Action: updateActRestart,
			Dismiss: "Later",
		}
	case update.KindHomebrew:
		return updateDialog{
			Title:   "Lathe " + n.Label + " is available",
			Message: "You have Lathe " + current + ". You installed Lathe with Homebrew, so update it from a terminal:\n\n" + n.Command + "\n\nThen restart Lathe.",
			Button:  "Copy command", Action: updateActCopy, Arg: n.Command,
			Dismiss: "Later",
		}
	}
	return updateDialog{
		Title:   "Lathe " + n.Label + " is available",
		Message: "You have Lathe " + current + ". Download the new version from GitHub, then replace the app.",
		Button:  "Download", Action: updateActDownload, Arg: n.URL,
		Dismiss: "Later",
	}
}
