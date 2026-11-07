package cmd

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/desktop"
)

// fakeLauncher records whether the app was opened.
func fakeLauncher(available, display bool, opened *bool) appLauncher {
	return appLauncher{
		available: func() bool { return available },
		display: func() (bool, string) {
			if display {
				return true, ""
			}
			return false, "no display found (DISPLAY and WAYLAND_DISPLAY are unset)"
		},
		run: func(context.Context, desktop.Options) error {
			*opened = true
			return nil
		},
	}
}

// runRoot runs execute and resets the root command's output afterwards.
func runRoot(t *testing.T, args []string, l appLauncher) (stdout, stderr string, err error) {
	t.Helper()
	var out, errb bytes.Buffer
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	err = execute(context.Background(), args, l, &out, &errb)
	return out.String(), errb.String(), err
}

func rootHelp(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	rootCmd.SetOut(&b)
	t.Cleanup(func() { rootCmd.SetOut(nil) })
	if err := rootCmd.Help(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestBareLatheWithoutDesktopBuildPrintsHelp(t *testing.T) {
	var opened bool
	stdout, stderr, err := runRoot(t, nil, fakeLauncher(false, true, &opened))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if opened {
		t.Fatal("a build without the desktop app must not try to open it")
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing (help as today)", stderr)
	}
	if want := rootHelp(t); stdout != want {
		t.Errorf("bare lathe printed\n%s\nwant the root help\n%s", stdout, want)
	}
	// rootCmd stays non-runnable, so the usage line is unchanged.
	if strings.Contains(stdout, "lathe [flags]") || !strings.Contains(stdout, "lathe [command]") {
		t.Errorf("usage line changed:\n%s", stdout)
	}
}

func TestBareLatheInRealNonDesktopBuildPrintsHelp(t *testing.T) {
	// The real launcher, in this (untagged) test build.
	if desktop.Available() {
		t.Skip("built with -tags desktop")
	}
	stdout, stderr, err := runRoot(t, nil, launcher)
	if err != nil || stderr != "" || !strings.Contains(stdout, "Available Commands:") {
		t.Fatalf("err=%v stderr=%q stdout=%q", err, stderr, stdout)
	}
}

func TestBareLatheOpensAppWithDisplay(t *testing.T) {
	var opened bool
	stdout, stderr, err := runRoot(t, nil, fakeLauncher(true, true, &opened))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !opened {
		t.Fatal("bare lathe in a desktop build with a display should open the app")
	}
	if stdout != "" || stderr != "" {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout, stderr)
	}
}

func TestBareLatheWithoutDisplayPrintsHelpAndWhy(t *testing.T) {
	var opened bool
	stdout, stderr, err := runRoot(t, nil, fakeLauncher(true, false, &opened))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if opened {
		t.Fatal("no display: the app must not open")
	}
	if want := rootHelp(t); stdout != want {
		t.Errorf("stdout should be the root help, got\n%s", stdout)
	}
	if strings.Count(stderr, "\n") != 1 || !strings.Contains(stderr, "no display") {
		t.Errorf("stderr = %q, want one line saying why", stderr)
	}
}

func TestSubcommandsDoNotOpenApp(t *testing.T) {
	var opened bool
	stdout, _, err := runRoot(t, []string{"--version"}, fakeLauncher(true, true, &opened))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if opened || !strings.HasPrefix(stdout, "lathe ") {
		t.Errorf("--version: opened=%v stdout=%q", opened, stdout)
	}
	stdout, _, err = runRoot(t, []string{"--help"}, fakeLauncher(true, true, &opened))
	if err != nil || opened || stdout != rootHelp(t) {
		t.Errorf("--help: err=%v opened=%v", err, opened)
	}
}

func TestAppCommandWithoutDesktopBuild(t *testing.T) {
	var opened bool
	err := fakeLauncher(false, true, &opened).open(context.Background(), appOptions(""))
	if !errors.Is(err, desktop.ErrNotBuilt) || opened {
		t.Fatalf("err = %v, opened = %v; want ErrNotBuilt", err, opened)
	}
	for _, want := range []string{"lathe serve", "releases"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should point to %q", err, want)
		}
	}
}

func TestAppCommandRealStub(t *testing.T) {
	if desktop.Available() {
		t.Skip("built with -tags desktop")
	}
	_, stderr, err := runRoot(t, []string{"app"}, launcher)
	if !errors.Is(err, desktop.ErrNotBuilt) {
		t.Fatalf("lathe app: err = %v, want ErrNotBuilt", err)
	}
	if !strings.Contains(stderr, "lathe serve") {
		t.Errorf("stderr = %q, want the error pointing to lathe serve", stderr)
	}
}

func TestAppCommandWithoutDisplay(t *testing.T) {
	var opened bool
	err := fakeLauncher(true, false, &opened).open(context.Background(), appOptions(""))
	if err == nil || opened || !strings.Contains(err.Error(), "no display") {
		t.Fatalf("err = %v, opened = %v", err, opened)
	}
}

func TestAppCommandOpensWithAgent(t *testing.T) {
	var got desktop.Options
	l := appLauncher{
		available: func() bool { return true },
		display:   func() (bool, string) { return true, "" },
		run: func(_ context.Context, o desktop.Options) error {
			got = o
			return nil
		},
	}
	if err := l.open(context.Background(), appOptions("my-acp")); err != nil {
		t.Fatal(err)
	}
	if got.Agent != "my-acp" {
		t.Errorf("Agent = %q", got.Agent)
	}
}

func TestLaunchArgsDropsMacProcessSerial(t *testing.T) {
	in := []string{"-psn_0_12345"}
	if got := launchArgs("darwin", in); len(got) != 0 {
		t.Errorf("darwin: %v, want none", got)
	}
	if got := launchArgs("linux", in); !reflect.DeepEqual(got, in) {
		t.Errorf("linux: %v, want unchanged", got)
	}
	if got := launchArgs("darwin", []string{"list", "-psn_0_1"}); !reflect.DeepEqual(got, []string{"list"}) {
		t.Errorf("darwin with a command: %v", got)
	}
}
