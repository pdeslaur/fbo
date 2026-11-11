// Command melange-update bumps a melange package and the Go modules it pins to
// their latest releases.
//
// The upstream repository and tag format are read from the config's
// git-checkout step. When a newer GitHub release exists, the package version
// and expected-commit are updated. Packages that download upstream artifacts
// with fetch steps instead describe how to find the latest release with
// update-* vars, which are jq queries over a JSON document; a newer release
// updates the vars they resolve and each fetch step's expected-sha256. Go
// modules pinned in pipeline steps with
// "go get MODULE@VERSION" are bumped to the latest release within their major
// version, as reported by the Go module proxy.
//
// When the package version changes the epoch is reset to 0; when only Go
// modules change it is incremented. Results are written as GitHub Actions step
// outputs: package, updated, and when updated, title and body.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/sethvargo/go-envconfig"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

type config struct {
	GitHubToken   string `env:"GH_TOKEN,required"`
	MelangeConfig string `env:"MELANGE_CONFIG,default=melange.yaml"`
	GitHubOutput  string `env:"GITHUB_OUTPUT"`
}

func main() {
	ctx := context.Background()
	cfg := envconfig.MustProcess(ctx, &config{})

	data, err := os.ReadFile(cfg.MelangeConfig)
	if err != nil {
		clog.FatalContextf(ctx, "reading melange config: %v", err)
	}

	src, err := parseSource(data)
	if err != nil {
		clog.FatalContextf(ctx, "%s: %v", cfg.MelangeConfig, err)
	}
	ctx = clog.WithValues(ctx, "package", src.Name)
	setOutput(ctx, cfg.GitHubOutput, "package", src.Name)

	var updates []update

	gh := githubClient{token: cfg.GitHubToken}
	var pkgUpdate *update
	var pkgVersionChanged bool
	switch {
	case src.Update != nil:
		data, pkgUpdate, err = updatePackageFetch(ctx, data, src)
		pkgVersionChanged = pkgUpdate != nil
	case src.Branch != "":
		data, pkgUpdate, pkgVersionChanged, err = updatePackageBranch(ctx, gh, data, src)
	default:
		data, pkgUpdate, err = updatePackage(ctx, gh, data, src)
		pkgVersionChanged = pkgUpdate != nil
	}
	if err != nil {
		clog.FatalContextf(ctx, "updating package: %v", err)
	}
	if pkgUpdate != nil {
		updates = append(updates, *pkgUpdate)
	}

	data, modUpdates, err := updateGoModules(ctx, data)
	if err != nil {
		clog.FatalContextf(ctx, "updating Go modules: %v", err)
	}
	updates = append(updates, modUpdates...)

	if len(updates) == 0 {
		clog.InfoContext(ctx, "already up to date")
		setOutput(ctx, cfg.GitHubOutput, "updated", "false")
		return
	}

	epoch := src.Epoch + 1
	if pkgVersionChanged {
		epoch = 0
	}
	if data, err = setEpoch(data, epoch); err != nil {
		clog.FatalContextf(ctx, "%s: %v", cfg.MelangeConfig, err)
	}

	if err := os.WriteFile(cfg.MelangeConfig, data, 0o644); err != nil {
		clog.FatalContextf(ctx, "writing melange config: %v", err)
	}

	clog.InfoContext(ctx, "updated melange config", "updates", len(updates), "epoch", epoch)
	setOutput(ctx, cfg.GitHubOutput, "updated", "true")
	setOutput(ctx, cfg.GitHubOutput, "title", title(updates))
	setOutput(ctx, cfg.GitHubOutput, "body", body(src.Name, updates))
}

// updatePackage bumps the package to the latest upstream GitHub release. It
// returns a nil update if the package is already up to date.
func updatePackage(ctx context.Context, gh githubClient, data []byte, src source) ([]byte, *update, error) {
	ctx = clog.WithValues(ctx, "upstream", src.Owner+"/"+src.Repo)

	latestTag, err := gh.latestReleaseTag(ctx, src.Owner, src.Repo)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching latest release: %w", err)
	}
	latest, ok := src.VersionFromTag(latestTag)
	if !ok {
		return nil, nil, fmt.Errorf("latest release tag %q does not match tag template %q", latestTag, src.TagTemplate)
	}

	cmp, err := compareVersions(src.Version, latest)
	if err != nil {
		return nil, nil, fmt.Errorf("comparing versions: %w", err)
	}
	clog.InfoContext(ctx, "package version check", "current", src.Version, "latest", latest)
	if cmp >= 0 {
		return data, nil, nil
	}

	commit, err := gh.tagCommit(ctx, src.Owner, src.Repo, latestTag)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching commit for tag %s: %w", latestTag, err)
	}
	data, err = patchConfig(data, src, latest, commit)
	if err != nil {
		return nil, nil, err
	}
	return data, &update{
		Name: src.Name,
		From: src.Tag(src.Version),
		To:   latestTag,
		URL:  fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", src.Owner, src.Repo, url.PathEscape(latestTag)),
	}, nil
}

// updatePackageBranch bumps the package to the latest commit on the tracked
// branch. The version is derived from the commit date as YYYY.MM.DD. It
// returns a nil update if the branch head matches the pinned expected-commit.
// The versionChanged bool reports whether the YYYY.MM.DD version changed, so
// the caller can decide whether to reset the epoch.
func updatePackageBranch(ctx context.Context, gh githubClient, data []byte, src source) ([]byte, *update, bool, error) {
	ctx = clog.WithValues(ctx, "upstream", src.Owner+"/"+src.Repo, "branch", src.Branch)

	commit, date, err := gh.branchHeadCommit(ctx, src.Owner, src.Repo, src.Branch)
	if err != nil {
		return nil, nil, false, fmt.Errorf("fetching branch head: %w", err)
	}
	clog.InfoContext(ctx, "branch commit check", "current", src.ExpectedCommit[:8], "latest", commit[:8])
	if commit == src.ExpectedCommit {
		return data, nil, false, nil
	}

	latest := date.UTC().Format("2006.01.02")
	versionChanged := latest != src.Version
	data, err = patchConfig(data, src, latest, commit)
	if err != nil {
		return nil, nil, false, err
	}
	return data, &update{
		Name: src.Name,
		From: src.ExpectedCommit[:8],
		To:   commit[:8],
		URL:  fmt.Sprintf("https://github.com/%s/%s/commit/%s", src.Owner, src.Repo, commit),
	}, versionChanged, nil
}

// updatePackageFetch bumps a package of upstream artifacts to the latest
// release found by its update rules, updating the vars they resolve and each
// fetch step's expected SHA-256. It returns a nil update if the package is
// already up to date.
func updatePackageFetch(ctx context.Context, data []byte, src source) ([]byte, *update, error) {
	ctx = clog.WithValues(ctx, "upstream", src.Update.URL)

	rel, err := latestRelease(ctx, src.Update)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching latest release: %w", err)
	}

	cmp, err := compareVersions(src.Version, rel.Version)
	if err != nil {
		return nil, nil, fmt.Errorf("comparing versions: %w", err)
	}
	clog.InfoContext(ctx, "package version check", "current", src.Version, "latest", rel.Version)
	if cmp >= 0 {
		return data, nil, nil
	}

	for _, name := range slices.Sorted(maps.Keys(rel.Vars)) {
		if data, err = patchVar(data, name, src.Vars[name], rel.Vars[name]); err != nil {
			return nil, nil, err
		}
	}
	vars := maps.Clone(src.Vars)
	maps.Copy(vars, rel.Vars)
	for _, f := range src.Fetches {
		u, err := expand(f.URI, rel.Version, vars)
		if err != nil {
			return nil, nil, err
		}
		sum, err := sha256OfURL(ctx, u)
		if err != nil {
			return nil, nil, fmt.Errorf("computing checksum: %w", err)
		}
		clog.InfoContext(ctx, "fetched artifact", "uri", u, "sha256", sum)
		if data, err = patchSHA256(data, f, sum); err != nil {
			return nil, nil, err
		}
	}
	if data, err = patchVersion(data, src, rel.Version); err != nil {
		return nil, nil, err
	}

	// Without release notes, link to the document the release was found in.
	notes := rel.ReleaseNotes
	if notes == "" {
		notes = src.Update.URL
	}
	return data, &update{
		Name: src.Name,
		From: src.Version,
		To:   rel.Version,
		URL:  notes,
	}, nil
}

// updateGoModules bumps each Go module pinned with "go get" to its latest
// release within the same major version.
func updateGoModules(ctx context.Context, data []byte) ([]byte, []update, error) {
	var updates []update
	for _, m := range parseGoModules(data) {
		ctx := clog.WithValues(ctx, "module", m.Path)
		if !semver.IsValid(m.Version) || semver.Prerelease(m.Version) != "" {
			clog.WarnContext(ctx, "skipping module not pinned to a release version", "version", m.Version)
			continue
		}

		latest, err := latestModuleVersion(ctx, m.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("fetching latest version of %s: %w", m.Path, err)
		}
		clog.InfoContext(ctx, "module version check", "current", m.Version, "latest", latest)
		// The proxy reports a pseudo-version, which is a prerelease, for
		// modules without any tagged release.
		if semver.Prerelease(latest) != "" || semver.Compare(latest, m.Version) <= 0 {
			continue
		}

		if data, err = patchGoModule(data, m, latest); err != nil {
			return nil, nil, err
		}
		updates = append(updates, update{
			Name: m.Path,
			From: m.Version,
			To:   latest,
			URL:  "https://pkg.go.dev/" + m.Path + "@" + latest,
		})
	}
	return data, updates, nil
}

// latestModuleVersion returns the latest version of a Go module path from the
// Go module proxy. Since major versions above v1 have distinct module paths,
// this never crosses a major version.
func latestModuleVersion(ctx context.Context, path string) (string, error) {
	escaped, err := module.EscapePath(path)
	if err != nil {
		return "", err
	}
	var r struct {
		Version string `json:"Version"`
	}
	if err := getJSON(ctx, "https://proxy.golang.org/"+escaped+"/@latest", nil, &r); err != nil {
		return "", err
	}
	return r.Version, nil
}

type githubClient struct {
	token string
}

func (c githubClient) latestReleaseTag(ctx context.Context, owner, repo string) (string, error) {
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := c.get(ctx, fmt.Sprintf("repos/%s/%s/releases/latest", owner, repo), &r); err != nil {
		return "", err
	}
	return r.TagName, nil
}

func (c githubClient) tagCommit(ctx context.Context, owner, repo, tag string) (string, error) {
	var r struct {
		SHA string `json:"sha"`
	}
	if err := c.get(ctx, fmt.Sprintf("repos/%s/%s/commits/%s", owner, repo, url.PathEscape(tag)), &r); err != nil {
		return "", err
	}
	return r.SHA, nil
}

func (c githubClient) branchHeadCommit(ctx context.Context, owner, repo, branch string) (sha string, date time.Time, err error) {
	var r struct {
		SHA    string `json:"sha"`
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := c.get(ctx, fmt.Sprintf("repos/%s/%s/commits/%s", owner, repo, url.PathEscape(branch)), &r); err != nil {
		return "", time.Time{}, err
	}
	return r.SHA, r.Commit.Committer.Date, nil
}

func (c githubClient) get(ctx context.Context, path string, out any) error {
	return getJSON(ctx, "https://api.github.com/"+path, http.Header{
		"Authorization": {"Bearer " + c.token},
		"Accept":        {"application/vnd.github+json"},
	}, out)
}

func getJSON(ctx context.Context, u string, header http.Header, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	for k, v := range header {
		req.Header[k] = v
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", u, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// setOutput writes a GitHub Actions step output, using the multiline
// delimiter syntax so that values may span several lines.
func setOutput(ctx context.Context, path, key, value string) {
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		clog.WarnContextf(ctx, "opening GITHUB_OUTPUT: %v", err)
		return
	}
	defer f.Close()
	delim := make([]byte, 16)
	rand.Read(delim)
	d := "EOF_" + hex.EncodeToString(delim)
	fmt.Fprintf(f, "%s<<%s\n%s\n%s\n", key, d, value, d)
}
