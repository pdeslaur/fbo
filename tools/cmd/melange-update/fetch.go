package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/itchyny/gojq"
)

// Vars that describe how to find the latest release of a fetch-based source.
// melange rejects unknown config fields, so these live alongside the vars
// that the fetch URIs reference.
const (
	// updateURLVar is the URL of a JSON document describing upstream releases.
	updateURLVar = "update-url"
	// updateVersionVar is a jq query over that document for the latest
	// version.
	updateVersionVar = "update-version"
	// updateVarPrefix prefixes a jq query that resolves the var of the same
	// name, minus the prefix, for the latest version.
	updateVarPrefix = "update-var-"
	// updateReleaseNotesVar is an optional jq query for the latest version's
	// release notes URL. melange substitutes ${{...}} in var values, so it
	// can't be a template.
	updateReleaseNotesVar = "update-release-notes"
)

// updateRules describe how to find the latest release of a fetch-based
// source, as read from its update-* vars.
type updateRules struct {
	URL     string
	Version string
	// Vars maps var names to the jq queries that resolve them.
	Vars         map[string]string
	ReleaseNotes string
}

// parseFetchSource reads the update rules from a source's vars and validates
// its fetch steps.
func parseFetchSource(s source) (source, error) {
	r := &updateRules{
		URL:          s.Vars[updateURLVar],
		Version:      s.Vars[updateVersionVar],
		Vars:         map[string]string{},
		ReleaseNotes: s.Vars[updateReleaseNotesVar],
	}
	if r.URL == "" || r.Version == "" {
		return source{}, fmt.Errorf("%s and %s are required", updateURLVar, updateVersionVar)
	}
	for k, q := range s.Vars {
		name, ok := strings.CutPrefix(k, updateVarPrefix)
		if !ok {
			continue
		}
		if _, ok := s.Vars[name]; !ok {
			return source{}, fmt.Errorf("%s resolves var %q, which isn't set", k, name)
		}
		r.Vars[name] = q
	}
	for _, f := range s.Fetches {
		if f.ExpectedSHA256 == "" {
			return source{}, fmt.Errorf("fetch of %q has no expected-sha256", f.URI)
		}
	}
	s.Update = r
	return s, nil
}

// release is an upstream release of a fetch-based source.
type release struct {
	Version string
	// Vars are the resolved values of the vars with update-var-* queries.
	Vars map[string]string
	// ReleaseNotes is empty unless there's an update-release-notes query.
	ReleaseNotes string
}

// latestRelease evaluates the update rules against the upstream release
// document. The update-var-* and update-release-notes queries see the latest
// version as $version and may call fetch_json to follow a URL to another JSON
// document.
func latestRelease(ctx context.Context, r *updateRules) (release, error) {
	var doc any
	if err := getJSON(ctx, r.URL, nil, &doc); err != nil {
		return release{}, err
	}

	version, err := evalQuery(ctx, r.Version, doc, "")
	if err != nil {
		return release{}, fmt.Errorf("%s: %w", updateVersionVar, err)
	}
	rel := release{Version: version, Vars: map[string]string{}}
	for name, q := range r.Vars {
		if rel.Vars[name], err = evalQuery(ctx, q, doc, version); err != nil {
			return release{}, fmt.Errorf("%s%s: %w", updateVarPrefix, name, err)
		}
	}
	if r.ReleaseNotes != "" {
		if rel.ReleaseNotes, err = evalQuery(ctx, r.ReleaseNotes, doc, version); err != nil {
			return release{}, fmt.Errorf("%s: %w", updateReleaseNotesVar, err)
		}
	}
	return rel, nil
}

// evalQuery runs a jq query against doc, binding version to $version. The
// query must produce exactly one non-empty string.
func evalQuery(ctx context.Context, query string, doc any, version string) (string, error) {
	parsed, err := gojq.Parse(query)
	if err != nil {
		return "", fmt.Errorf("parsing %q: %w", query, err)
	}
	code, err := gojq.Compile(parsed,
		gojq.WithVariables([]string{"$version"}),
		gojq.WithFunction("fetch_json", 0, 0, func(v any, _ []any) any {
			u, ok := v.(string)
			if !ok {
				return fmt.Errorf("fetch_json: input %v is not a string", v)
			}
			var out any
			if err := getJSON(ctx, u, nil, &out); err != nil {
				return fmt.Errorf("fetch_json: %w", err)
			}
			return out
		}),
	)
	if err != nil {
		return "", fmt.Errorf("compiling %q: %w", query, err)
	}

	var results []any
	iter := code.RunWithContext(ctx, doc, version)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := v.(error); ok {
			return "", fmt.Errorf("evaluating %q: %w", query, err)
		}
		results = append(results, v)
	}
	if len(results) != 1 {
		return "", fmt.Errorf("%q produced %d results, want 1", query, len(results))
	}
	s, ok := results[0].(string)
	if !ok || s == "" {
		return "", fmt.Errorf("%q produced %v, want a non-empty string", query, results[0])
	}
	return s, nil
}

var varRefRe = regexp.MustCompile(`\$\{\{vars\.([^}]+)\}\}`)

// expand substitutes version and vars into a fetch URI or release notes URL.
// It fails if any other substitution remains, such as ${{build.arch}}, since
// its value isn't known outside of a build.
func expand(s, version string, vars map[string]string) (string, error) {
	var missing []string
	out := varRefRe.ReplaceAllStringFunc(s, func(ref string) string {
		name := varRefRe.FindStringSubmatch(ref)[1]
		v, ok := vars[name]
		if !ok {
			missing = append(missing, name)
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%q references unset vars %v", s, missing)
	}
	out = expandVersion(out, version)
	if strings.Contains(out, "${{") {
		return "", fmt.Errorf("%q has substitutions other than package.version and vars", s)
	}
	return out, nil
}

// sha256OfURL downloads u and returns the hex-encoded SHA-256 of its contents.
func sha256OfURL(ctx context.Context, u string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned %s", u, resp.Status)
	}
	h := sha256.New()
	if _, err := io.Copy(h, resp.Body); err != nil {
		return "", fmt.Errorf("downloading %s: %w", u, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
