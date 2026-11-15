package main

import (
	"archive/tar"
	"bufio"
	"cmp"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// arches are the architectures packages are built for. A pinned build
// dependency is only updated to a version available for all of them.
var arches = []string{"x86_64", "aarch64"}

// apkPackage is a package version listed in an APKINDEX.
type apkPackage struct {
	Name    string
	Version string
	// Origin is the name of the melange config the package was built from.
	Origin string
	// Provides are the names this package also satisfies at its version,
	// e.g. "rust" for rust-1.100.
	Provides []string
}

// apkIndex maps a name to every package that is or provides it.
type apkIndex map[string][]apkPackage

// fetchAPKIndex reads the APKINDEX of a repository for one architecture.
// The index is only consulted for available versions; melange verifies the
// packages it installs against the repository's keyring.
func fetchAPKIndex(ctx context.Context, repo, arch string) (apkIndex, error) {
	u := strings.TrimSuffix(repo, "/") + "/" + arch + "/APKINDEX.tar.gz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", u, resp.Status)
	}
	idx, err := parseAPKIndexArchive(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", u, err)
	}
	return idx, nil
}

// parseAPKIndexArchive reads the APKINDEX file from an APKINDEX.tar.gz. The
// archive is a signature archive followed by the index archive, each gzipped
// separately; gzip reads them as one stream, and tar as one archive since the
// signature archive has no end-of-archive marker.
func parseAPKIndexArchive(r io.Reader) (apkIndex, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no APKINDEX file in archive")
		}
		if err != nil {
			return nil, err
		}
		if hdr.Name == "APKINDEX" {
			return parseAPKIndex(tr)
		}
	}
}

// parseAPKIndex parses an APKINDEX: blank-line separated records of
// "K:value" lines, of which P is the name, V the version, o the origin and p
// a space-separated list of "name" or "name=version" provides.
func parseAPKIndex(r io.Reader) (apkIndex, error) {
	idx := apkIndex{}
	add := func(p apkPackage) {
		if p.Name == "" {
			return
		}
		idx[p.Name] = append(idx[p.Name], p)
		for _, name := range p.Provides {
			idx[name] = append(idx[name], p)
		}
	}

	var cur apkPackage
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			add(cur)
			cur = apkPackage{}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch key {
		case "P":
			cur.Name = value
		case "V":
			cur.Version = value
		case "o":
			cur.Origin = value
		case "p":
			for _, p := range strings.Fields(value) {
				// Only versioned provides can satisfy a pinned version.
				if name, _, ok := strings.Cut(p, "="); ok {
					cur.Provides = append(cur.Provides, name)
				}
			}
		}
	}
	add(cur)
	return idx, sc.Err()
}

// latestAPK returns the latest version of name that every index has, and the
// package it comes from in the first index.
func latestAPK(indexes []apkIndex, name string) (apkPackage, bool, error) {
	var latest apkPackage
	found := false
	for _, p := range indexes[0][name] {
		if !inAll(indexes[1:], name, p.Version) {
			continue
		}
		if found {
			cmp, err := compareAPKVersions(p.Version, latest.Version)
			if err != nil {
				return apkPackage{}, false, err
			}
			if cmp <= 0 {
				continue
			}
		}
		latest, found = p, true
	}
	return latest, found, nil
}

func inAll(indexes []apkIndex, name, version string) bool {
	for _, idx := range indexes {
		found := false
		for _, p := range idx[name] {
			if p.Version == version {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// apkVersionRe matches the apk versions Wolfi uses: dotted numbers, an
// optional letter, optional suffixes such as _rc1 or _p2, and a release.
var apkVersionRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)*)([a-z]?)((?:_(?:alpha|beta|pre|rc|cvs|svn|git|hg|p)[0-9]*)*)-r([0-9]+)$`)

var apkSuffixRe = regexp.MustCompile(`_([a-z]+)([0-9]*)`)

// apkSuffixOrder ranks version suffixes as apk does: pre-release suffixes
// sort before no suffix, which is 0, and post-release suffixes after it.
var apkSuffixOrder = map[string]int{
	"alpha": -4, "beta": -3, "pre": -2, "rc": -1,
	"cvs": 1, "svn": 2, "git": 3, "hg": 4, "p": 5,
}

// compareAPKVersions compares two apk versions such as "1.26.11-r0" or
// "1.27.0_rc2-r1", returning -1, 0 or 1.
func compareAPKVersions(a, b string) (int, error) {
	pa, err := parseAPKVersion(a)
	if err != nil {
		return 0, err
	}
	pb, err := parseAPKVersion(b)
	if err != nil {
		return 0, err
	}

	for i := range max(len(pa.numbers), len(pb.numbers)) {
		if c := cmp.Compare(component(pa.numbers, i), component(pb.numbers, i)); c != 0 {
			return c, nil
		}
	}
	if c := strings.Compare(pa.letter, pb.letter); c != 0 {
		return c, nil
	}
	// Each suffix is compared by rank, then number. A version that runs out
	// of suffixes compares as if it had a suffix of rank 0.
	for i := range max(len(pa.suffixes), len(pb.suffixes)) {
		if c := cmp.Compare(component(pa.suffixes, i), component(pb.suffixes, i)); c != 0 {
			return c, nil
		}
	}
	return cmp.Compare(pa.release, pb.release), nil
}

type apkVersion struct {
	numbers []int
	letter  string
	// suffixes holds a rank and a number for each suffix.
	suffixes []int
	release  int
}

func parseAPKVersion(v string) (apkVersion, error) {
	m := apkVersionRe.FindStringSubmatch(v)
	if m == nil {
		return apkVersion{}, fmt.Errorf("unsupported apk version %q", v)
	}
	var p apkVersion
	for _, s := range strings.Split(m[1], ".") {
		n, err := strconv.Atoi(s)
		if err != nil {
			return apkVersion{}, fmt.Errorf("apk version %q: %w", v, err)
		}
		p.numbers = append(p.numbers, n)
	}
	p.letter = m[2]
	for _, s := range apkSuffixRe.FindAllStringSubmatch(m[3], -1) {
		n := 0
		if s[2] != "" {
			n, _ = strconv.Atoi(s[2])
		}
		p.suffixes = append(p.suffixes, apkSuffixOrder[s[1]], n)
	}
	p.release, _ = strconv.Atoi(m[4])
	return p, nil
}

func component(s []int, i int) int {
	if i >= len(s) {
		return 0
	}
	return s[i]
}
