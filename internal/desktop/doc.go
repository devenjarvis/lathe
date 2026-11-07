// Package desktop is Lathe's desktop app: a native window around the same
// reader `lathe serve` renders, with the hosted agent embedded, built from the
// same `lathe` binary.
//
// # Decisions
//
//   - Wails v3 (github.com/wailsapp/wails/v3, v3.1.0). v3 is stable now, and it
//     gives us what the reader needs from a shell out of the box: real
//     multi-window support (the agent panel's pop-out is a real window), a
//     notifications service (pkg/services/notifications), a dialogs manager
//     with a directory chooser, and single-instance locking. It needs go >=
//     1.26, which is why go.mod says 1.26.0 (Wails v2.17.1 needs the same, so
//     that cost is not v3's alone).
//
//   - Build tag "desktop". Everything that imports Wails is in a
//     `//go:build desktop` file. Linux Wails needs cgo plus GTK4 and
//     WebKitGTK 6.0, so the default build (`go build`, `go install`, CI, the
//     CGO_ENABLED=0 release CLI) stays pure Go and gets the untagged stub
//     (stub.go), which reports ErrNotBuilt. `go build -tags desktop` (add
//     "production" for releases, which drops Wails' dev-mode logging and
//     inspector) produces the same single `lathe` binary that can also open
//     the app. Wails' Windows backend is pure Go, so the Windows desktop build
//     cross-compiles with CGO_ENABLED=0.
//
//   - No localhost port for the UI. The window loads the reader through Wails'
//     asset server, which serves serve.Server.Handler() directly
//     (AssetOptions.Handler); SSE streams through it, and the page origin is
//     wails://localhost (the serve package's same-origin check accepts it as
//     loopback). The CLI still needs a URL: `lathe work`, `lathe start` and
//     `lathe verify-cancel` find the server through ~/.lathe/serve.json, so
//     Run also listens on 127.0.0.1:0 with a second Handler() of the same
//     Server (same queue, same agent) and records that URL there while the app
//     runs. Loopback only, for the reason cmd/serve.go gives.
//
//   - Native affordances go through serve.Native (internal/serve/native.go):
//     the page calls same-origin endpoints and the server calls back into the
//     shell installed here, so the reader keeps one code path for browser and
//     app.
//
//   - Bundle id com.devenjarvis.lathe, app name "Lathe". The bundle id is
//     also the single-instance id and the Linux GTK application id.
//
// The environment seam: a dock or Start-menu launch does not inherit the
// user's shell PATH, so the agent CLI may not be found. Options.PrepareEnv
// runs before the agent command is resolved and started, so the caller can
// load the login shell's environment first.
package desktop
