# Desktop app

The desktop app is the reader and the hosted agent in their own window. It is the same `lathe` binary: `lathe` with no arguments (or `lathe app`) opens the app, and every subcommand works as before. This page records why it is built the way it is, how it fits together, and what to check by hand on each OS.

## Decisions

**Wails v3 (v3.1.0), not v2.** When #100 was written, v3 was in beta; v3.0.0 and v3.1.0 have since been released. v3 gives us first-class multi-window support (the agent panel's pop-out becomes a real window), a notifications service, a dialogs manager, and single-instance support out of the box. Both v3.1 and v2.17 require Go 1.26, so the Go bump wasn't a reason to stay on v2. Electron would be heavy, and Tauri would put a Rust shell around a Go core; Wails keeps everything in Go.

**One binary, behind a `desktop` build tag.** A plain `go build` / `go install` stays pure Go (`CGO_ENABLED=0`), so CI, `install.sh` and `go install` users are unaffected. `go build -tags desktop,production` adds the app to the same binary. All Wails imports live in `//go:build desktop` files, and an untagged stub returns `desktop.ErrNotBuilt`, so `cmd` needs no tags. `production` turns off Wails' dev mode and must be on for every release build.

**No frontend rewrite.** The window serves the existing `internal/serve` handler through Wails' asset server (`application.AssetOptions{Handler: …}`), so templates, styles, fonts and the SSE agent stream carry over unchanged.

**No JavaScript bridge.** The page reaches the shell only through same-origin POST endpoints (`/-/native/…`) that 404 without a shell. The templates only learn whether a shell is present (`.Native`, `data-native` on `<html>`), so the reader keeps one code path and `lathe serve` renders exactly what it did before.

**Platforms.** macOS ships a signed, notarized, universal `Lathe.app` (built on a macOS runner with cgo). Windows ships the desktop build as a console-subsystem `lathe.exe` (pure Go, cross-compiled), which hides its own console when double-clicked. Linux release binaries stay CLI-only; the Linux app is build-from-source (`libgtk-4-dev libwebkitgtk-6.0-dev`). Wails' experimental cgo-free Linux backend (`-tags purego`, which loads GTK4/WebKitGTK 6.0 at runtime) could later let one Linux release binary be both. That's a follow-up, not part of this change.

## How it fits together

- `internal/desktop.Run` mirrors `cmd/serve.go`: tutorials dir, `serve.NewServer`, agent from `--agent` or `config.Agent()`, `SetAgent`, `SetNative`, `Close` on quit.
- **Pages** come from `serve.Server.Handler()` through the asset server. The origin is `wails://localhost` on macOS and Linux and `http://wails.localhost` on Windows; `sameOrigin` accepts both. WebKitGTK doesn't follow 3xx responses from a custom-scheme handler, so `redirect.go` turns same-origin redirects into `location.replace` pages.
- **CLI bridge.** The app also listens on `127.0.0.1:0` with the same Server and writes `~/.lathe/serve.json`, so `lathe work`, `lathe start` and `lathe verify-cancel` find it just like `lathe serve`. On quit it removes the file only if the PID is still its own.
- **Native affordances** (`serve.Native`, implemented in `internal/desktop/shell.go`):
  - `PickFolder`: the workspace's **Choose folder…** button, in the agent panel's setup and its footer.
  - `Notify`: a system notification when Verify, Extend or Check my work finishes, whether the run was on the hosted agent or a `/lathe-work` worker. Runs you cancel and Ask answers don't notify. Notifications are queued and never block a job; where the OS can't post one (an unbundled binary on macOS, no D-Bus on Linux) it becomes a log line.
  - `OpenWindow`: the agent panel's pop-out (`/{slug}/{part}?agent=popout` only).
  - `OpenURL`: links to other sites open in the system browser (http, https and mailto only), so the window never navigates away from Lathe.
- **Menu bar and shortcuts** (`internal/desktop/menu.go`, laid out by `menu_plan.go` from the registry in `internal/shortcuts`). Menus: Lathe, File, Edit (macOS only: the webview needs its key equivalents for copy and paste), View, Tutorial, Agent, Window. Every window uses the application menu (`UseApplicationMenu`), so the keys work in pop-outs and Settings on Windows and Linux too. In the app the page binds none of the menu's keys; a click (or its accelerator) on a page command calls `serve.Server.RunCommand`, which sends it down the focused page's `GET /-/native/commands` stream, and the page runs it. Pages post `POST /-/native/context` on load and on focus with the commands they can run, and the shell enables or disables the items from that (no Tutorial items on Home, no Next part on the last part). Shell commands (About, Settings… → a `/-/settings` window) are handled in the shell. Commands that aren't built yet (New tutorial, Open in new window, Go to anything, Bigger/Smaller text, Check for updates, Ask about selection, Show workspace) are listed disabled and bind no accelerator, so their keys keep their system or browser meaning. Plain `lathe serve` has no menu: `shortcuts.js` binds the same combos in the page.
- **Single instance.** A second launch focuses the first window. On Linux this uses a D-Bus name, and is skipped with a log line when there is no session bus.
- **Agent discovery from a Dock launch** (`internal/loginenv`). The app starts with launchd's minimal PATH, so before resolving the agent it runs the login shell once (`$SHELL -i -l`, with a 5-second limit), merges its PATH, and puts the `agent_path` dirs from `~/.lathe/config.json` first. The README's Install section explains `agent_path` to users.

## Verified in CI and on Linux

- The CI `desktop` matrix vets, builds and tests `-tags desktop` on ubuntu-24.04 (under `xvfb-run`), macOS and Windows. On macOS it also builds an unsigned app with `build/darwin/package.sh`.
- On Linux (GTK4 + WebKitGTK 6.0 under Xvfb) these were checked by hand:
  - list and part pages render with the Recursive fonts, the green-bar listings and Chroma code blocks;
  - dark mode follows the GTK theme (`GTK_THEME=Adwaita:dark` makes WebKitGTK report `prefers-color-scheme: dark`);
  - **Choose folder…** opens the GTK picker and fills the field;
  - **Pop out** opens a separate window;
  - an external link goes to `xdg-open` and the window stays on Lathe;
  - the login-shell PATH and `agent_path` are applied at startup;
  - SIGINT and SIGTERM quit cleanly and remove `serve.json`;
  - a second launch exits and leaves the first window running.

## Manual checks

Run these on a real machine before a release that changes the app.

**macOS (signed `Lathe.app`)**

1. It launches from Finder and from the Dock, with no Gatekeeper prompt (`spctl --assess -vv /Applications/Lathe.app`).
2. On first launch only the window opens, with no notification prompt. The first finished Verify, Extend or Check asks for notification permission, then shows the notification.
3. **Choose folder…** opens the picker as a sheet, can create a folder, and Cancel leaves the workspace unchanged.
4. **Pop out** opens a 480×820 window; clicking it again focuses that window.
5. A second launch focuses the first window.
6. Cmd-Q and closing the main window both remove `~/.lathe/serve.json`.
7. Agent discovery from the Dock:
   - Install an agent where only your rc files put it on PATH (e.g. under nvm in `~/.zshrc`).
   - Quit Lathe, then open it from the Dock, not from a terminal.
   - **Bring your agent** should show it as found, and an Ask should stream.
   - Then add `[ -n "$LATHE_RESOLVING_ENVIRONMENT" ] && return` to the top of `~/.zshrc`, relaunch, and confirm the agent is *not* found.
   - Set `agent_path` to its directory, relaunch, and confirm it is found again.
   - Remove the guard afterwards.
8. The delete `confirm()` dialog appears on the list page.
9. External links open in your default browser.
10. Dark mode follows the system appearance.
11. Accessibility (#84 still holds in the window):
    - keyboard-only, Tab reaches the panel, Choose folder…, Pop out and the sort select, with a visible focus ring;
    - VoiceOver announces "Workspace set to …" after a pick.

**Windows**

1. Double-clicking `lathe.exe` opens the app, and its console closes after a brief flash.
2. In cmd or PowerShell, `lathe app` keeps the console and prints logs; `lathe --help` and `lathe --version` print as before.
3. Toast notifications appear, with the app name.
4. Choose folder…, Pop out and external links work. A second launch focuses the first window.
5. Closing the window removes `serve.json`.
6. Fonts, green-bar listings and dark mode render in WebView2. Check the focus ring and the Narrator announcement for a workspace pick.
7. `confirm()` on delete works.

**Linux**

1. Build with `go build -tags desktop,production .`, then run `lathe` in a desktop session (GNOME or KDE).
2. Check the same items as macOS 3–10. Notifications go through `org.freedesktop.Notifications`.
3. Over SSH with no `DISPLAY`/`WAYLAND_DISPLAY`, `lathe` prints help, plus one stderr line saying why.

**Menu bar (every OS)**

1. On Home, the Tutorial and Agent items (except Agent settings…) are disabled; on a part they enable, and Next part is disabled on the last part.
2. Each wired item works from the menu and from its key: Next/Previous part (⌘] ⌘[ / Ctrl+] Ctrl+[), Verify (⇧⌘V, macOS only), Write next part (⇧⌘N / Ctrl+Shift+N), Show panel (⌘J / Ctrl+J), Check my work (⇧⌘K / Ctrl+Shift+K, on a part with a Checkpoint), Pop out (⌥⌘J / Ctrl+Shift+J), Home (⇧⌘H / Alt+Home), Settings… (⌘, / Ctrl+,), Light/Dark.
3. A key runs its command once (not twice), including while typing in the agent composer, and copy/paste still work in the composer (macOS Edit menu).
4. In a pop-out window the Agent keys act on the pop-out.

## Maintainer setup (signing and notarization)

Repo secrets used by `.github/workflows/release.yml`:

| Secret | What it is |
|---|---|
| `HOMEBREW_TAP_GITHUB_TOKEN` | fine-grained PAT with Contents read/write on `devenjarvis/homebrew-tap` (existing) |
| `MACOS_SIGN_P12` | base64 of the Developer ID Application certificate and private key (`.p12`) |
| `MACOS_SIGN_PASSWORD` | the `.p12` password |
| `MACOS_NOTARY_KEY` | base64 of the App Store Connect API key (`.p8`) |
| `MACOS_NOTARY_KEY_ID` | that key's Key ID |
| `MACOS_NOTARY_ISSUER_ID` | the Issuer ID shown above the key list |

1. **Developer ID Application certificate.** This needs Apple Developer Program membership, and only the Account Holder can create it.
   - In Keychain Access, go to Certificate Assistant → Request a Certificate from a Certificate Authority, and save the request to disk.
   - At developer.apple.com, go to Certificates → "+" → **Developer ID Application** (G2 Sub-CA). Upload the request, download the `.cer` and open it.
   - In Keychain Access → My Certificates, export "Developer ID Application: …" with its private key as a `.p12`.
   - `base64 -i cert.p12 | pbcopy` gives you `MACOS_SIGN_P12`.
2. **App Store Connect API key.**
   - Go to appstoreconnect.apple.com → Users and Access → Integrations → App Store Connect API → Team Keys → "+", with role **Developer**.
   - Download the `.p8` (you can download it only once).
   - `base64 -i AuthKey_XXXX.p8 | pbcopy` gives you `MACOS_NOTARY_KEY`.
3. **First dry run.** Before the first tag, run Actions → Release → Run workflow on `main`. It signs, notarizes and staples for real, then uploads the artifacts without publishing anything.

A tag pushed on `devenjarvis/lathe` fails if any signing secret is missing or signing fails. Forks and manual runs without secrets ad-hoc sign the app and log a warning.

## Known gaps / follow-ups

- **`lathe start`** with the app running opens the loopback URL in your browser, not an app window.
- **macOS window close:** the app quits when the main window closes; there is no "keep running in the Dock".
- **Windows exe resources:** no `.syso` yet (exe icon, DPI manifest).
- **Linux release:** a cgo-free (purego) Linux desktop binary is not built yet.
- **Notification clicks:** clicking a notification doesn't yet jump to the tutorial.
