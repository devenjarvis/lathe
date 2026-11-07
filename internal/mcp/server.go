// Package mcp is Lathe's Model Context Protocol server: the skills' write
// commands (`lathe store`, `verify-result`, `extend-start`/`extend-commit`,
// `check-result`, `tag`, `voice add`) served as typed tools over stdio.
//
// `lathe serve --agent` passes it to the hosted agent in ACP session/new (as
// `lathe mcp --slug <slug>`), so an agent working for Lathe records results
// with tool calls instead of finding the binary and getting shell flags right.
// The tools are thin wrappers over the same internal/store and internal/voice
// functions the CLI commands call; the binary stays the only writer of durable
// state, and the CLI keeps working as the fallback.
//
// It speaks MCP's stdio transport: newline-delimited JSON-RPC 2.0, reusing
// internal/acp's connection. It handles initialize, ping, tools/list and
// tools/call, and never calls a model.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/devenjarvis/lathe/internal/acp"
	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
	"github.com/devenjarvis/lathe/internal/voice"
)

// ProtocolVersion is the MCP revision this server implements. initialize
// echoes the client's version when it is one we know, else offers this one.
const ProtocolVersion = "2025-06-18"

var knownVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

// Server serves Lathe's write tools.
type Server struct {
	// Slug, when set, scopes the server to one tutorial: the per-tutorial tools
	// default to it and refuse any other slug, and the tools that create state
	// outside it (lathe_store, lathe_voice_add) are not offered.
	Slug string
	// New, with Slug, is a hosted new-tutorial run's server: Slug is the
	// reservation the server made for it (status "writing"), and lathe_store
	// is offered too, storing only into that slug whatever the source dir is
	// called. Without Slug it has no effect.
	New bool
	// VoiceDraft, when set, is a hosted New voice session's server (Settings
	// › Voices › New voice): the name Lathe reserved for the voice
	// (voice.ReserveDraft). It offers only the voice tools: lathe_voice_draft
	// records a draft for the reader's preview, lathe_voice_show reads a
	// voice's spec, and lathe_voice_add saves the draft the reader approved,
	// under that name only. Exclusive with Slug.
	VoiceDraft string
	// Propose, when set, is a hosted shelf-suggestion run's server:
	// ProposeSort ("sort": a shelf for every tutorial) or ProposeSplit
	// ("split": a two-way split of Shelf). It offers only that proposal tool
	// and lathe_shelf_list, so the run can record a proposal but move
	// nothing; the reader applies it.
	Propose string
	// Shelf is the shelf a ProposeSplit server may propose a split for.
	Shelf string
	// Version is reported as serverInfo.version.
	Version string
}

// Propose modes.
const (
	ProposeSort  = "sort"
	ProposeSplit = "split"
)

// Serve answers requests from r on w until r ends.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	conn := acp.NewConn(r, w, handler{s})
	// Finish in-flight tool calls: an agent may close stdin right after its
	// last request.
	conn.Wait()
	// A clean end of input closes the connection with "...: EOF".
	if err := conn.Err(); err != nil && !strings.HasSuffix(err.Error(), io.EOF.Error()) {
		return err
	}
	return nil
}

type handler struct{ s *Server }

func (handler) HandleNotification(string, json.RawMessage) {}

func (h handler) HandleRequest(_ context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &p)
		v := ProtocolVersion
		if knownVersions[p.ProtocolVersion] {
			v = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "lathe", "title": "Lathe", "version": h.s.Version},
			"instructions":    h.s.instructions(),
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": h.s.Tools()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &acp.RPCError{Code: -32602, Message: "invalid params"}
		}
		t, ok := h.s.tool(p.Name)
		if !ok {
			return nil, &acp.RPCError{Code: -32602, Message: "unknown tool: " + p.Name}
		}
		text, err := h.s.call(t, p.Arguments)
		if err != nil {
			// Tool failures go back as results, so the agent sees them and can
			// fix its call.
			return toolResult(err.Error(), true), nil
		}
		return toolResult(text, false), nil
	}
	return nil, &acp.RPCError{Code: -32601, Message: "method not found: " + method}
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]string{{"type": "text", "text": text}},
		"isError": isError,
	}
}

func (s *Server) instructions() string {
	if s.VoiceDraft != "" {
		return fmt.Sprintf("Lathe's voice tools, scoped to the new voice %q that Lathe reserved for this session. Record each draft with lathe_voice_draft (the reader sees its ## Sample as the preview); lathe_voice_add saves the draft the reader approved, and only after they click Save voice.", s.VoiceDraft)
	}
	switch s.Propose {
	case ProposeSort:
		return "Lathe's shelf tools for sorting the library. lathe_shelf_propose records a proposed shelf for one tutorial; nothing moves until the reader reviews and applies the proposal."
	case ProposeSplit:
		return fmt.Sprintf("Lathe's shelf tools for splitting the shelf %q. lathe_shelf_split_propose records the proposed split; nothing moves until the reader previews and applies it.", s.Shelf)
	}
	if s.Slug != "" && s.New {
		return fmt.Sprintf("Lathe's state-writing commands as tools, scoped to the new tutorial %q that Lathe reserved for this session. lathe_store stores into %q whatever your source directory is called. Use them instead of running the `lathe` CLI.", s.Slug, s.Slug)
	}
	if s.Slug != "" {
		return fmt.Sprintf("Lathe's state-writing commands as tools, scoped to the tutorial %q. Use them instead of running the `lathe` CLI.", s.Slug)
	}
	return "Lathe's state-writing commands as tools. Use them instead of running the `lathe` CLI."
}

// Tool is one tool as tools/list describes it.
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`

	scoped bool // acts on one tutorial, so a scoped server offers it
	// anywhere: read-only, offered by every server (scoped and propose too).
	anywhere bool
	// propose: offered only by a server in that Propose mode.
	propose string
	// reserved: a --new scoped server offers it too, and its slug argument
	// (optional, defaulting to the reservation) is added to the schema.
	reserved bool
	run      func(s *Server, args json.RawMessage) (string, error)
}

// Tools lists the tools this server offers.
func (s *Server) Tools() []Tool {
	if s.VoiceDraft != "" {
		return voiceDraftTools(s.VoiceDraft)
	}
	var out []Tool
	for _, t := range allTools() {
		if !s.offers(t) {
			continue
		}
		switch {
		case s.Propose != "" || s.Slug == "" || t.anywhere:
		case t.scoped:
			t.InputSchema = withOptionalSlug(t.InputSchema, s.Slug)
		case t.reserved:
			t.InputSchema = withReservedSlug(t.InputSchema, s.Slug)
		}
		out = append(out, t)
	}
	return out
}

// offers reports whether this server offers t.
func (s *Server) offers(t Tool) bool {
	switch {
	case s.Propose != "":
		return t.anywhere || t.propose == s.Propose
	case t.propose != "":
		return false
	case s.Slug == "" || t.anywhere:
		return true
	}
	return t.scoped || (s.New && t.reserved)
}

func (s *Server) tool(name string) (Tool, bool) {
	for _, t := range s.Tools() {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

func (s *Server) call(t Tool, args json.RawMessage) (string, error) {
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	return t.run(s, args)
}

// decode strictly unmarshals tool arguments, so a misspelled field is an
// error the agent sees rather than a silently dropped value.
func decode(args json.RawMessage, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(args)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// tutorialDir resolves the slug a tool was called with against the scope.
func (s *Server) tutorialDir(slug string) (string, string, error) {
	slug = strings.TrimSpace(slug)
	if s.Slug != "" {
		if slug == "" {
			slug = s.Slug
		} else if slug != s.Slug {
			return "", "", fmt.Errorf("this Lathe session is scoped to %q; it cannot change %q", s.Slug, slug)
		}
	}
	if slug == "" {
		return "", "", errors.New("slug is required")
	}
	dir, err := store.TutorialPath(slug)
	return dir, slug, err
}

// JSON schema helpers.

func obj(required []string, props map[string]any) map[string]any {
	m := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func strList(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

var slugProp = str("the tutorial's slug (its directory name under ~/.lathe/tutorials/)")

var sourcesProp = map[string]any{
	"type":        "array",
	"description": "sources consulted while researching, kept as the tutorial's research trail",
	"items": obj([]string{"url"}, map[string]any{
		"url":   str("the source's URL"),
		"title": str("link text for the source (optional)"),
	}),
}

// withOptionalSlug drops "slug" from a schema's required list and notes the
// default, for a server scoped to one tutorial.
func withOptionalSlug(schema map[string]any, slug string) map[string]any {
	out := map[string]any{}
	for k, v := range schema {
		out[k] = v
	}
	if req, ok := schema["required"].([]string); ok {
		var kept []string
		for _, r := range req {
			if r != "slug" {
				kept = append(kept, r)
			}
		}
		if len(kept) > 0 {
			out["required"] = kept
		} else {
			delete(out, "required")
		}
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		p := map[string]any{}
		for k, v := range props {
			p[k] = v
		}
		if _, has := p["slug"]; has {
			p["slug"] = str(fmt.Sprintf("the tutorial's slug; this session is scoped to %q, which is the default", slug))
		}
		out["properties"] = p
	}
	return out
}

// withReservedSlug adds an optional slug to a tool that, unscoped, takes
// none (lathe_store derives it from the directory name): a --new session may
// name its reservation, and nothing else.
func withReservedSlug(schema map[string]any, slug string) map[string]any {
	out := map[string]any{}
	for k, v := range schema {
		out[k] = v
	}
	p := map[string]any{}
	if props, ok := schema["properties"].(map[string]any); ok {
		for k, v := range props {
			p[k] = v
		}
	}
	p["slug"] = str(fmt.Sprintf("the reserved tutorial's slug; this session may store only into %q, which is the default", slug))
	out["properties"] = p
	return out
}

type sourceArg struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

func toSources(in []sourceArg) []store.Source {
	out := make([]store.Source, 0, len(in))
	for _, s := range in {
		out = append(out, store.Source{Title: strings.TrimSpace(s.Title), URL: strings.TrimSpace(s.URL)})
	}
	return out
}

func allTools() []Tool {
	return []Tool{
		{
			Name:        "lathe_store",
			Title:       "Store a tutorial",
			Description: "Save a tutorial directory into the Lathe library (same as `lathe store <path>`). The directory's name becomes the slug; its part-NN.md files become the parts. Re-storing a slug replaces it.",
			InputSchema: obj([]string{"path"}, map[string]any{
				"path":        str("absolute path of the directory holding the tutorial's markdown"),
				"title":       str("display title; defaults to the slug, title-cased"),
				"tags":        strList("search tags"),
				"sources":     sourcesProp,
				"repo":        str("git remote the tutorial was written for"),
				"repo_branch": str("branch the tutorial targets (only recorded with repo)"),
				"tools": map[string]any{
					"type":        "array",
					"description": "languages/tools and versions the tutorial targets",
					"items": obj([]string{"name"}, map[string]any{
						"name":    str("tool or language, e.g. zig"),
						"version": str("version, e.g. 0.13"),
					}),
				},
				"voice": str("writing voice the tutorial was generated in"),
				"model": str("display label of the LLM that wrote it, e.g. \"Claude Opus 4.8\""),
			}),
			reserved: true,
			run:      runStore,
		},
		{
			Name:        "lathe_verify_result",
			Title:       "Record a verification result",
			Description: "Record the status of verifying a tutorial (same as `lathe verify-result`). Call with status \"verifying\" when you start, then once with verified, failed or skipped.",
			InputSchema: obj([]string{"slug", "status"}, map[string]any{
				"slug": slugProp,
				"status": map[string]any{
					"type": "string", "enum": []string{"verifying", "verified", "failed", "skipped"},
					"description": "verifying (in flight), verified, failed, or skipped (a required tool is not installed)",
				},
				"part":        str("filename of the part that failed"),
				"failed_step": map[string]any{"type": "integer", "minimum": 0, "description": "1-indexed step that failed"},
				"error":       str("error message or output to record"),
				"checked_at":  str("RFC3339 timestamp; defaults to now"),
				"code":        str("absolute path of the scratch git repo (one part-NN tag per passing part) to keep as the code snapshot; verified or failed only"),
			}),
			scoped: true,
			run:    runVerifyResult,
		},
		{
			Name:        "lathe_extend_start",
			Title:       "Reserve the next part",
			Description: "Reserve the next part of a tutorial and mark it extending (same as `lathe extend-start`). Returns the filename to write, and its full path.",
			InputSchema: obj([]string{"slug"}, map[string]any{"slug": slugProp}),
			scoped:      true,
			run:         runExtendStart,
		},
		{
			Name:        "lathe_extend_commit",
			Title:       "Record a new part",
			Description: "Record a part written after lathe_extend_start (same as `lathe extend-commit`): appends it to the tutorial, folds in its sources, and resets the status to unverified.",
			InputSchema: obj([]string{"slug", "part"}, map[string]any{
				"slug":    slugProp,
				"part":    str("the part filename lathe_extend_start returned, e.g. part-03.md"),
				"sources": sourcesProp,
				"model":   str("display label of the LLM that wrote this part; refreshes the byline"),
			}),
			scoped: true,
			run:    runExtendCommit,
		},
		{
			Name:        "lathe_tag",
			Title:       "Edit a tutorial's tags",
			Description: "Set, add, or remove a tutorial's search tags (same as `lathe tag`). set replaces the whole list; add and remove then edit it. Pass at least one.",
			InputSchema: obj([]string{"slug"}, map[string]any{
				"slug":   slugProp,
				"set":    strList("replace all tags with these"),
				"add":    strList("tags to add"),
				"remove": strList("tags to remove"),
			}),
			scoped: true,
			run:    runTag,
		},
		{
			Name:        "lathe_check_result",
			Title:       "Record a Check my work result",
			Description: "Record the result of checking a learner's own code against a part (same as `lathe check-result`). Hints form a ladder: nudge (required), then pointer, where, and reveal; only reveal may contain the reference code, and the reader sees it only when they ask.",
			InputSchema: obj([]string{"slug", "part", "status"}, map[string]any{
				"slug": slugProp,
				"part": str("the part checked, e.g. part-02.md"),
				"status": map[string]any{
					"type": "string", "enum": []string{"pass", "fail", "skipped"},
					"description": "pass (the checkpoint behaves as the part says), fail, or skipped (the checkpoint could not be run here)",
				},
				"confidence": map[string]any{
					"type": "string", "enum": []string{"reference", "text"},
					"description": "reference when the code snapshot's diff for the part guided the check, text when only the part's text did; defaults from whether a snapshot exists",
				},
				"summary": str("one or two sentences for the top of the result"),
				"on_part": str("the part the learner's code matches best, when it is not part (they are ahead or behind)"),
				"findings": map[string]any{
					"type":        "array",
					"description": "what is wrong, one hint ladder per problem; required when status is fail",
					"items": obj([]string{"nudge"}, map[string]any{
						"file":    str("the learner's file, relative to their workspace"),
						"nudge":   str("rung 1: what to look at again, without saying what is wrong"),
						"pointer": str("rung 2: the step or side-note of the part that covers it"),
						"where":   str("rung 3: the file and roughly where in it"),
						"reveal":  str("rung 4: the reference code (markdown), shown only on request"),
					}),
				},
				"divergences": map[string]any{
					"type":        "array",
					"description": "where passing code differs from the reference: choices, not mistakes",
					"items": obj([]string{"note"}, map[string]any{
						"file":       str("the learner's file"),
						"note":       str("what differs, framed as a choice"),
						"later_part": str("a later part that relies on the reference shape, if any"),
					}),
				},
				"checked_at": str("RFC3339 timestamp; defaults to now"),
			}),
			scoped: true,
			run:    runCheckResult,
		},
		{
			Name:        "lathe_shelf_list",
			Title:       "List shelves",
			Description: "List the library's shelves in display order (Unsorted last) with each one's id, name, description and tutorial count (same as `lathe shelf list --json`). Read-only.",
			InputSchema: obj(nil, map[string]any{}),
			anywhere:    true,
			run:         runShelfList,
		},
		{
			Name:        "lathe_shelf_set",
			Title:       "Put a tutorial on a shelf",
			Description: "Put a tutorial on a shelf, by the shelf's id or name (same as `lathe shelf set`). Prefer an existing shelf; pass create: true to make a new shelf when none fits. \"unsorted\" takes it off its shelf.",
			InputSchema: obj([]string{"slug", "shelf"}, map[string]any{
				"slug":   slugProp,
				"shelf":  str("an existing shelf's id or name, or (with create) the new shelf's name"),
				"create": map[string]any{"type": "boolean", "description": "create the shelf when no shelf has that name"},
			}),
			scoped: true,
			run:    runShelfSet,
		},
		{
			Name:        "lathe_shelf_propose",
			Title:       "Propose a shelf for a tutorial",
			Description: "Record the shelf you propose for one tutorial (same as `lathe shelf propose`): an existing shelf's id or name, or a new shelf's name. Moves nothing; the reader reviews every proposal and applies them. Call once per tutorial; a second call for the same slug replaces the first.",
			InputSchema: obj([]string{"slug", "shelf"}, map[string]any{
				"slug":  slugProp,
				"shelf": str("an existing shelf's id or name, or the name of a new shelf"),
			}),
			propose: ProposeSort,
			run:     runShelfPropose,
		},
		{
			Name:        "lathe_shelf_split_propose",
			Title:       "Propose splitting a shelf",
			Description: "Record a proposed split of a shelf into exactly two groups (same as `lathe shelf propose-split`). Every tutorial on the shelf goes in exactly one group. The first group keeps the shelf (renamed to the group's name), so put the larger or more central group first. Moves nothing; the reader previews the split and applies it.",
			InputSchema: obj([]string{"groups"}, map[string]any{
				"shelf": str("the shelf's id; defaults to the shelf this session is for"),
				"groups": map[string]any{
					"type": "array", "minItems": 2, "maxItems": 2,
					"description": "the two groups, the one that keeps the shelf first",
					"items": obj([]string{"name", "slugs"}, map[string]any{
						"name":  str("the group's shelf name: short, a plural noun phrase like the reader's other shelves"),
						"slugs": strList("the slugs of the tutorials in this group"),
					}),
				},
			}),
			propose: ProposeSplit,
			run:     runShelfSplitPropose,
		},
		{
			Name:        "lathe_voice_add",
			Title:       "Add a custom voice",
			Description: "Save a custom writing voice (same as `lathe voice add <name> --file`). Built-in voice names are reserved.",
			InputSchema: obj([]string{"name", "spec"}, map[string]any{
				"name": str("voice name: lowercase letters, digits and hyphens"),
				"spec": str("the voice spec markdown, including its name:/description: frontmatter"),
			}),
			run: runVoiceAdd,
		},
	}
}

func runStore(s *Server, raw json.RawMessage) (string, error) {
	var a struct {
		Slug       string      `json:"slug"`
		Path       string      `json:"path"`
		Title      string      `json:"title"`
		Tags       []string    `json:"tags"`
		Sources    []sourceArg `json:"sources"`
		Repo       string      `json:"repo"`
		RepoBranch string      `json:"repo_branch"`
		Tools      []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"tools"`
		Voice string `json:"voice"`
		Model string `json:"model"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Path) == "" {
		return "", errors.New("path is required")
	}
	// Unscoped, the directory's name is the slug, as with `lathe store`.
	// Scoped to a reservation, the reserved slug is, and only it.
	var into string
	switch {
	case s.Slug == "" && strings.TrimSpace(a.Slug) != "":
		return "", errors.New("invalid arguments: slug is only for a session Lathe reserved a tutorial for; the directory's name is the slug")
	case s.Slug != "":
		dir, slug, err := s.tutorialDir(a.Slug)
		if err != nil {
			return "", err
		}
		if _, err := store.ReadMetadata(dir); err != nil {
			return "", fmt.Errorf("the tutorial %q Lathe reserved for this session is gone (the run was stopped), so there is nothing to store into", slug)
		}
		into = slug
	}
	tools := make([]store.Tool, 0, len(a.Tools))
	for _, t := range a.Tools {
		tools = append(tools, store.Tool{Name: t.Name, Version: t.Version})
	}
	tut, err := store.Store(a.Path, store.StoreOptions{
		Title:   a.Title,
		Tags:    store.SplitTags(a.Tags),
		Sources: toSources(a.Sources),
		Repo:    a.Repo,
		Branch:  a.RepoBranch,
		Tools:   tools,
		Voice:   a.Voice,
		Model:   a.Model,
		Slug:    into,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Stored %q as %s (%s).", tut.Title, tut.Slug, tut.Status), nil
}

func runVerifyResult(s *Server, raw json.RawMessage) (string, error) {
	var a struct {
		Slug       string `json:"slug"`
		Status     string `json:"status"`
		Part       string `json:"part"`
		FailedStep int    `json:"failed_step"`
		Error      string `json:"error"`
		CheckedAt  string `json:"checked_at"`
		Code       string `json:"code"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	dir, slug, err := s.tutorialDir(a.Slug)
	if err != nil {
		return "", err
	}
	status := store.Status(a.Status)
	if err := store.RecordVerify(dir, store.VerifyRecord{
		Status: status, Part: a.Part, FailedStep: a.FailedStep,
		Error: a.Error, CheckedAt: a.CheckedAt, Code: a.Code,
	}); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("Recorded %s for %q.", status, slug)
	if a.Code != "" && !config.KeepVerifiedCode() {
		msg += " Not keeping the code snapshot: \"Keep verified code\" is off in Settings."
	} else if a.Code != "" {
		if err := store.SaveCode(dir, a.Code); err != nil {
			return "", fmt.Errorf("recorded %s, but could not save the code snapshot: %w", status, err)
		}
		msg += fmt.Sprintf(" Saved code snapshot to %s.", filepath.Join(dir, store.CodeDirName))
	}
	return msg, nil
}

func runExtendStart(s *Server, raw json.RawMessage) (string, error) {
	var a struct {
		Slug string `json:"slug"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	dir, _, err := s.tutorialDir(a.Slug)
	if err != nil {
		return "", err
	}
	part, err := store.ExtendStart(dir)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\nWrite it to %s, then call lathe_extend_commit with part %q.", part, filepath.Join(dir, part), part), nil
}

func runExtendCommit(s *Server, raw json.RawMessage) (string, error) {
	var a struct {
		Slug    string      `json:"slug"`
		Part    string      `json:"part"`
		Sources []sourceArg `json:"sources"`
		Model   string      `json:"model"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	dir, slug, err := s.tutorialDir(a.Slug)
	if err != nil {
		return "", err
	}
	if err := store.ExtendCommit(dir, a.Part, toSources(a.Sources), a.Model); err != nil {
		return "", err
	}
	return fmt.Sprintf("Added %s to %q.", a.Part, slug), nil
}

func runTag(s *Server, raw json.RawMessage) (string, error) {
	var a struct {
		Slug   string   `json:"slug"`
		Set    []string `json:"set"`
		Add    []string `json:"add"`
		Remove []string `json:"remove"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	dir, slug, err := s.tutorialDir(a.Slug)
	if err != nil {
		return "", err
	}
	if len(a.Set) == 0 && len(a.Add) == 0 && len(a.Remove) == 0 {
		return "", errors.New("nothing to do: pass set, add, or remove")
	}
	tags, err := store.UpdateTags(dir, a.Set, a.Add, a.Remove)
	if err != nil {
		return "", err
	}
	if len(tags) == 0 {
		return fmt.Sprintf("Cleared tags for %q.", slug), nil
	}
	return fmt.Sprintf("Tags for %q: %s", slug, strings.Join(tags, ", ")), nil
}

func runCheckResult(s *Server, raw json.RawMessage) (string, error) {
	var a struct {
		Slug string `json:"slug"`
		store.CheckResult
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	dir, slug, err := s.tutorialDir(a.Slug)
	if err != nil {
		return "", err
	}
	res := a.CheckResult
	if err := store.RecordCheck(dir, &res); err != nil {
		return "", err
	}
	return fmt.Sprintf("Recorded check of %s for %q: %s.", res.Part, slug, res.Status), nil
}

func runVoiceAdd(_ *Server, raw json.RawMessage) (string, error) {
	var a struct {
		Name string `json:"name"`
		Spec string `json:"spec"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	if err := voice.Add(a.Name, []byte(a.Spec)); err != nil {
		return "", err
	}
	return fmt.Sprintf("Added custom voice %q.", a.Name), nil
}

func tutorialsDir() (string, error) { return config.TutorialsDir() }

func runShelfList(_ *Server, raw json.RawMessage) (string, error) {
	if err := decode(raw, &struct{}{}); err != nil {
		return "", err
	}
	dir, err := tutorialsDir()
	if err != nil {
		return "", err
	}
	list, err := store.ListShelves(dir)
	if err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(list, "", "  ")
	return string(b), err
}

func runShelfSet(s *Server, raw json.RawMessage) (string, error) {
	var a struct {
		Slug   string `json:"slug"`
		Shelf  string `json:"shelf"`
		Create bool   `json:"create"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	tutDir, slug, err := s.tutorialDir(a.Slug)
	if err != nil {
		return "", err
	}
	sh, created, err := store.SetShelf(filepath.Dir(tutDir), slug, a.Shelf, a.Create)
	if err != nil {
		return "", err
	}
	if created {
		return fmt.Sprintf("Created the shelf %q and put %q on it.", sh.Name, slug), nil
	}
	return fmt.Sprintf("%q is on %s.", slug, sh.Name), nil
}

func runShelfPropose(_ *Server, raw json.RawMessage) (string, error) {
	var a struct {
		Slug  string `json:"slug"`
		Shelf string `json:"shelf"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	dir, err := tutorialsDir()
	if err != nil {
		return "", err
	}
	return store.ProposeShelf(dir, strings.TrimSpace(a.Slug), a.Shelf)
}

func runShelfSplitPropose(s *Server, raw json.RawMessage) (string, error) {
	var a struct {
		Shelf  string             `json:"shelf"`
		Groups []store.SplitGroup `json:"groups"`
	}
	if err := decode(raw, &a); err != nil {
		return "", err
	}
	shelf := strings.TrimSpace(a.Shelf)
	switch {
	case shelf == "":
		shelf = s.Shelf
	case s.Shelf != "" && shelf != s.Shelf:
		return "", fmt.Errorf("this Lathe session is for the shelf %q; it cannot propose a split of %q", s.Shelf, shelf)
	}
	if shelf == "" {
		return "", errors.New("shelf is required")
	}
	dir, err := tutorialsDir()
	if err != nil {
		return "", err
	}
	return store.ProposeSplit(dir, shelf, a.Groups)
}
