package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"chainguard.dev/apko/pkg/apk/apk"
	"github.com/chainguard-dev/clog"
	"github.com/pdeslaur/fbo/tools/internal/apksign"
	"github.com/pdeslaur/fbo/tools/internal/r2"
)

// arches are the architectures the repository has an index for.
var arches = []string{"x86_64", "aarch64"}

const (
	// A package's version-epoch never changes, so caches may keep it.
	packageCacheControl = "public, max-age=31536000, immutable"
	// The index changes with every new package; Cloudflare's cache, in
	// front of the bucket's domain, must not serve an old one.
	indexCacheControl = "no-cache"
)

// store is the bucket the repository is in.
type store interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, data []byte, contentType, cacheControl string) error
	PutIfAbsent(ctx context.Context, key string, data []byte, contentType, cacheControl string) (bool, error)
	List(ctx context.Context, prefix string) ([]r2.Object, error)
}

// packageKey is where the repository keeps a package built for arch, as
// apk fetches it: <repository>/<arch>/<name>-<version>.apk.
func packageKey(arch, file string) string {
	return path.Join("os", arch, file)
}

func indexKey(arch string) string {
	return path.Join("os", arch, "APKINDEX.tar.gz")
}

// upload adds packages to the repository, each from a file in a directory
// named after its arch, as melange writes them: packages/<arch>/<file>.apk.
// A package the repository already has is kept, not replaced: a
// version-epoch is immutable.
func upload(ctx context.Context, st store, files []string) error {
	for _, f := range files {
		arch := filepath.Base(filepath.Dir(f))
		if !slices.Contains(arches, arch) {
			return fmt.Errorf("%s: not in a directory named after one of %v", f, arches)
		}
		if !strings.HasSuffix(f, ".apk") {
			return fmt.Errorf("%s: not an .apk", f)
		}
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		key := packageKey(arch, filepath.Base(f))
		created, err := st.PutIfAbsent(ctx, key, data, "application/octet-stream", packageCacheControl)
		if err != nil {
			return err
		}
		if created {
			clog.InfoContext(ctx, "added package", "key", key)
		} else {
			clog.InfoContext(ctx, "repository already has this version-epoch; keeping it", "key", key)
		}
	}
	return nil
}

// index adds the packages the repository has for arch but its index doesn't
// list to the index, and signs and writes it. The index is verified with
// publicKey before it's changed.
func index(ctx context.Context, st store, s apksign.Signer, keyName string, publicKey []byte, arch string) error {
	ctx = clog.WithValues(ctx, "arch", arch)

	idx := &apk.APKIndex{}
	existing, err := st.Get(ctx, indexKey(arch))
	switch {
	case errors.Is(err, r2.ErrNotFound):
		clog.InfoContext(ctx, "no index yet")
	case err != nil:
		return err
	default:
		if err := apksign.Verify(existing, publicKey); err != nil {
			return fmt.Errorf("%s: %w", indexKey(arch), err)
		}
		if idx, err = apk.IndexFromArchive(io.NopCloser(bytes.NewReader(existing))); err != nil {
			return fmt.Errorf("reading %s: %w", indexKey(arch), err)
		}
	}

	listed := map[string]bool{}
	for _, p := range idx.Packages {
		listed[p.Filename()] = true
	}
	objects, err := st.List(ctx, packageKey(arch, "")+"/")
	if err != nil {
		return err
	}
	added := 0
	for _, o := range objects {
		file := path.Base(o.Key)
		if !strings.HasSuffix(file, ".apk") || listed[file] {
			continue
		}
		data, err := st.Get(ctx, o.Key)
		if err != nil {
			return err
		}
		pkg, err := apk.ParsePackage(ctx, bytes.NewReader(data), uint64(len(data)))
		if err != nil {
			return fmt.Errorf("reading %s: %w", o.Key, err)
		}
		if pkg.Filename() != file || pkg.Arch != arch {
			return fmt.Errorf("%s holds %s for %s", o.Key, pkg.Filename(), pkg.Arch)
		}
		idx.Packages = append(idx.Packages, pkg)
		added++
		clog.InfoContext(ctx, "indexing package", "key", o.Key)
	}
	if added == 0 && existing != nil {
		clog.InfoContext(ctx, "index is up to date")
		return nil
	}

	slices.SortFunc(idx.Packages, func(a, b *apk.Package) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.Version, b.Version))
	})
	archive, err := apk.ArchiveFromIndex(idx)
	if err != nil {
		return err
	}
	unsigned, err := io.ReadAll(archive)
	if err != nil {
		return err
	}
	signed, err := apksign.Resign(ctx, unsigned, s, keyName, publicKey)
	if err != nil {
		return fmt.Errorf("signing %s: %w", indexKey(arch), err)
	}
	if err := st.Put(ctx, indexKey(arch), signed, "application/gzip", indexCacheControl); err != nil {
		return err
	}
	clog.InfoContext(ctx, "wrote index", "added", added, "packages", len(idx.Packages))
	return nil
}
