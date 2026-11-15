package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"

	"chainguard.dev/apko/pkg/apk/apk"
)

// arches are the architectures packages are built for. A pinned build
// dependency is only updated to a version available for all of them.
var arches = []string{"x86_64", "aarch64"}

// resolvers returns a package resolver per architecture over the build
// environment's repositories, whose indexes are verified against its
// keyring.
func resolvers(ctx context.Context, env buildEnvironment) ([]*apk.PkgResolver, error) {
	keys, err := fetchKeyring(ctx, env.Keyring)
	if err != nil {
		return nil, err
	}
	var rs []*apk.PkgResolver
	for _, arch := range arches {
		indexes, err := apk.GetRepositoryIndexes(ctx, env.Repositories, keys, arch, apk.WithHTTPClient(http.DefaultClient))
		if err != nil {
			return nil, err
		}
		rs = append(rs, apk.NewPkgResolver(ctx, indexes))
	}
	return rs, nil
}

// fetchKeyring downloads each key, named by its file name as index
// signatures refer to it.
func fetchKeyring(ctx context.Context, urls []string) (map[string][]byte, error) {
	keys := map[string][]byte{}
	for _, u := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		key, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s returned %s", u, resp.Status)
		}
		keys[path.Base(u)] = key
	}
	return keys, nil
}

// latestAPK returns the package an unpinned build would install for name,
// limited to versions that every architecture has. A name may be a
// provides, such as rust for rust-1.100.
func latestAPK(rs []*apk.PkgResolver, name string) (*apk.RepositoryPackage, error) {
	var candidates [][]*apk.RepositoryPackage
	for i, r := range rs {
		pkgs, err := r.ResolvePackage(name, nil)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", arches[i], err)
		}
		candidates = append(candidates, pkgs)
	}
	if p := latestCommon(candidates); p != nil {
		return p, nil
	}
	return nil, fmt.Errorf("no version of %s is available for %v", name, arches)
}

// latestCommon returns the first of the first architecture's candidates,
// which are sorted best first, whose version every architecture has.
func latestCommon(candidates [][]*apk.RepositoryPackage) *apk.RepositoryPackage {
	for _, p := range candidates[0] {
		inAll := true
		for _, others := range candidates[1:] {
			inAll = inAll && slices.ContainsFunc(others, func(o *apk.RepositoryPackage) bool {
				return o.Version == p.Version
			})
		}
		if inAll {
			return p
		}
	}
	return nil
}

// isNewer reports whether apk version a is newer than b.
func isNewer(a, b string) (bool, error) {
	va, err := apk.ParseVersion(a)
	if err != nil {
		return false, err
	}
	vb, err := apk.ParseVersion(b)
	if err != nil {
		return false, err
	}
	return apk.CompareVersions(va, vb) > 0, nil
}
