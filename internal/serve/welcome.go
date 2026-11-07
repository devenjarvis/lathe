package serve

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"

	"github.com/devenjarvis/lathe/internal/agents"
	"github.com/devenjarvis/lathe/internal/config"
)

// The first-launch setup (#148): three short steps at /-/welcome.
//
//  1. Pick your agent: Settings › Agent's list of registry agents, with
//     install help (the shared "agentChoices" partial, agents.html).
//  2. Pick where code folders go: config code_root, ~/code by default.
//  3. Make your first tutorial (the New tutorial sheet), or Skip to Home.
//
// Every step is a server-rendered page whose form posts back here, so the
// flow works with scripts off; the page script only adds the Copy buttons,
// the desktop app's folder picker and opening the sheet over Home.
//
// When it shows: in the desktop app, opening Home (GET / without a query)
// with an empty library and the setup not done redirects here, so a first
// launch lands on it. Plain `lathe serve` never redirects (a browser tab
// opened by `lathe open` or a script should get the page it asked for):
// Home's empty state links to it instead. Finishing or skipping records
// config "onboarded", which stops both; Settings › Agent's "Run setup
// again" and the Lathe menu's "Welcome to Lathe…" reopen it any time.
//
//	GET  /-/welcome?step=agent|folder|start   one step (agent by default)
//	POST /-/welcome  step=agent   [agent=<registry id>]  → start it, then ?step=folder
//	POST /-/welcome  step=folder  code_root=<path>       → save it, then ?step=start
//	POST /-/welcome  step=start   next=new|home [sheet=1] → done; /-/new (or /#new) or /

// welcomeSteps are the steps, in order. The ids are the ?step= values.
var welcomeSteps = []struct{ ID, Label string }{
	{"agent", "Your agent"},
	{"folder", "Code folder"},
	{"start", "First tutorial"},
}

type welcomeStep struct {
	N             int
	ID, Label     string
	Current, Done bool
}

// welcomeView is welcome.html's data.
type welcomeView struct {
	Step  string
	Steps []welcomeStep
	// Error is the last POST's problem, shown on the step it came from.
	Error string

	// The agent step.
	Agents []settingsAgent
	// AnyReady: some registry agent can be started from here.
	AnyReady bool
	// AnyMissing: some registry agent isn't installed (offer Check again).
	AnyMissing    bool
	CustomCommand string

	// The summary on the last step: who writes Part 1.
	AgentName, AgentStatus string
	WorkerConnected        bool

	// The folder step.
	CodeRoot        string
	CodeRootExample string
}

// showWelcome reports whether Home should send the reader to the setup:
// only in the desktop app, only for a first launch (no tutorials, setup
// not done).
func (s *Server) showWelcome(r *http.Request, tutorials int) bool {
	return s.nativeShell() != nil && r.URL.RawQuery == "" && tutorials == 0 && !config.Onboarded()
}

func welcomeStepID(raw string) string {
	for _, st := range welcomeSteps {
		if st.ID == raw {
			return raw
		}
	}
	return welcomeSteps[0].ID
}

func (s *Server) welcomeFor(step string) welcomeView {
	v := welcomeView{Step: welcomeStepID(step)}
	at := 0
	for i, st := range welcomeSteps {
		if st.ID == v.Step {
			at = i
		}
	}
	for i, st := range welcomeSteps {
		v.Steps = append(v.Steps, welcomeStep{N: i + 1, ID: st.ID, Label: st.Label, Current: i == at, Done: i < at})
	}

	cards, cur, custom := s.agentCards()
	v.CustomCommand = custom
	picked := false
	ready := -1
	for i, c := range cards {
		if c.Ready {
			v.AnyReady = true
			if ready < 0 {
				ready = i
			}
		} else {
			v.AnyMissing = true
		}
		picked = picked || c.Picked
	}
	// Nothing running yet: pre-pick the first agent that can start, so
	// Continue uses it.
	if !picked && custom == "" && ready >= 0 {
		cards[ready].Picked = true
	}
	v.Agents = cards
	if h := s.hostedAgent(); h != nil && cur != nil {
		v.AgentName = h.name
		if v.AgentName == "" {
			v.AgentName = agents.DisplayName(h.command)
		}
		v.AgentStatus = describeAgent(*cur)
	}
	v.WorkerConnected = s.queue.WorkerConnected()

	st := config.LoadSettings()
	v.CodeRoot = st.CodeRoot
	v.CodeRootExample = config.CodeRootFor(st.CodeRoot, "digital-synth-zig")
	return v
}

func (s *Server) handleWelcome(w http.ResponseWriter, r *http.Request) {
	s.renderWelcome(w, s.welcomeFor(r.URL.Query().Get("step")), http.StatusOK)
}

func (s *Server) renderWelcome(w http.ResponseWriter, v welcomeView, code int) {
	var buf bytes.Buffer
	if err := s.welcomeTmpl.Execute(&buf, map[string]any{
		"CSS":          s.designCSS,
		"HighlightCSS": s.highlightCSS,
		"Native":       s.nativeShell() != nil,
		"Welcome":      v,
	}); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = buf.WriteTo(w)
}

// handleWelcomePost saves one step and moves to the next. It answers a
// problem by showing the same step again with the message.
func (s *Server) handleWelcomePost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	step := r.PostForm.Get("step")
	fail := func(code int, msg string) {
		v := s.welcomeFor(step)
		v.Error = msg
		s.renderWelcome(w, v, code)
	}
	next := func(step string) {
		http.Redirect(w, r, "/-/welcome?step="+url.QueryEscape(step), http.StatusSeeOther)
	}

	switch step {
	case "agent":
		if id := strings.TrimSpace(r.PostForm.Get("agent")); id != "" {
			a, ok := agents.ByID(id)
			if !ok {
				fail(http.StatusBadRequest, "That agent isn't one Lathe knows.")
				return
			}
			if code, err := s.useAgent(a); err != nil {
				fail(code, err.Error())
				return
			}
		}
		next("folder")
	case "folder":
		root := r.PostForm.Get("code_root")
		var bad error
		err := config.UpdateConfig(func(c *config.Config) error {
			bad = (&settingsPatch{CodeRoot: &root}).apply(c)
			return bad
		})
		switch {
		case bad != nil:
			// Show what the reader typed, not the saved folder.
			v := s.welcomeFor(step)
			v.Error, v.CodeRoot = bad.Error(), strings.TrimSpace(root)
			s.renderWelcome(w, v, http.StatusBadRequest)
			return
		case err != nil:
			fail(http.StatusInternalServerError, "Could not save the code folder: "+err.Error())
			return
		}
		next("start")
	case "start":
		if err := config.SetOnboarded(true); err != nil {
			fail(http.StatusInternalServerError, "Could not save: "+err.Error())
			return
		}
		switch {
		case r.PostForm.Get("next") != "new":
			http.Redirect(w, r, "/", http.StatusSeeOther)
		case r.PostForm.Get("sheet") != "":
			// The page script is running: the sheet opens over Home.
			http.Redirect(w, r, "/#new", http.StatusSeeOther)
		default:
			http.Redirect(w, r, "/-/new", http.StatusSeeOther)
		}
	default:
		http.Error(w, "unknown step", http.StatusBadRequest)
	}
}
