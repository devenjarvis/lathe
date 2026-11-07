package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LatestURL is the GitHub API endpoint for the newest published release (not
// a draft, not a prerelease).
const LatestURL = "https://api.github.com/repos/devenjarvis/lathe/releases/latest"

// ReleasesPage is the human page a Download button opens when a release
// doesn't name its own.
const ReleasesPage = "https://github.com/devenjarvis/lathe/releases/latest"

// releasePagePrefix is the only place a release's html_url may point: the
// shell opens it in the browser, so a strange URL in the API's answer is
// replaced by ReleasesPage.
const releasePagePrefix = "https://github.com/devenjarvis/lathe/releases/"

// FetchTimeout bounds one release lookup.
const FetchTimeout = 10 * time.Second

// Release is the newest published release.
type Release struct {
	// Version is the tag without its "v" ("0.9.0").
	Version string `json:"version"`
	// URL is the release's page on GitHub.
	URL       string    `json:"url"`
	Published time.Time `json:"published,omitempty"`
}

// Fetcher looks up the newest release. GitHub is the real one; tests pass a
// fake, so they never reach the network.
type Fetcher func(ctx context.Context) (Release, error)

// GitHub returns a Fetcher that asks the GitHub API at url (LatestURL in the
// app) with client (nil: a client with FetchTimeout). It sends no token and
// no identifying data beyond a User-Agent naming the running version, which
// GitHub's API requires.
func GitHub(client *http.Client, url, current string) Fetcher {
	if client == nil {
		client = &http.Client{Timeout: FetchTimeout}
	}
	return func(ctx context.Context) (Release, error) {
		ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return Release{}, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "Lathe/"+strings.TrimPrefix(current, "v"))
		resp, err := client.Do(req)
		if err != nil {
			return Release{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		switch {
		case resp.StatusCode == http.StatusNotFound:
			return Release{}, errors.New("no published release yet")
		case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
			return Release{}, errors.New("GitHub is limiting requests from this network; try again later")
		case resp.StatusCode != http.StatusOK:
			return Release{}, fmt.Errorf("GitHub answered %s", resp.Status)
		}
		return decodeRelease(io.LimitReader(resp.Body, 1<<20))
	}
}

func decodeRelease(r io.Reader) (Release, error) {
	var body struct {
		TagName     string    `json:"tag_name"`
		HTMLURL     string    `json:"html_url"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
		PublishedAt time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("unreadable release: %w", err)
	}
	if body.Draft || body.Prerelease {
		return Release{}, errors.New("the latest release is not a stable release")
	}
	v, ok := Parse(body.TagName)
	if !ok {
		return Release{}, fmt.Errorf("the latest release has an unexpected tag %q", body.TagName)
	}
	url := body.HTMLURL
	if !strings.HasPrefix(url, releasePagePrefix) {
		url = ReleasesPage
	}
	return Release{Version: v.String(), URL: url, Published: body.PublishedAt}, nil
}
