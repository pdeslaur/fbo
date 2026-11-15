package main

import (
	"context"
	"testing"

	"chainguard.dev/apko/pkg/apk/apk"
)

func packages(name string, versions ...string) []*apk.RepositoryPackage {
	var pkgs []*apk.RepositoryPackage
	for _, v := range versions {
		pkgs = append(pkgs, &apk.RepositoryPackage{Package: &apk.Package{Name: name, Version: v}})
	}
	return pkgs
}

func TestLatestCommon(t *testing.T) {
	for _, tc := range []struct {
		desc       string
		candidates [][]*apk.RepositoryPackage
		want       string
	}{{
		desc: "same versions",
		candidates: [][]*apk.RepositoryPackage{
			packages("go-1.26", "1.26.11-r0", "1.26.10-r0"),
			packages("go-1.26", "1.26.11-r0", "1.26.10-r0"),
		},
		want: "1.26.11-r0",
	}, {
		desc: "latest only built for the first architecture",
		candidates: [][]*apk.RepositoryPackage{
			packages("go-1.26", "1.26.12-r0", "1.26.11-r0", "1.26.10-r0"),
			packages("go-1.26", "1.26.11-r0", "1.26.10-r0"),
		},
		want: "1.26.11-r0",
	}, {
		desc: "latest only built for the second architecture",
		candidates: [][]*apk.RepositoryPackage{
			packages("go-1.26", "1.26.11-r0", "1.26.10-r0"),
			packages("go-1.26", "1.26.12-r0", "1.26.11-r0", "1.26.10-r0"),
		},
		want: "1.26.11-r0",
	}, {
		desc: "no common version",
		candidates: [][]*apk.RepositoryPackage{
			packages("bun", "1.4.4-r1"),
			packages("bun", "1.4.4-r0"),
		},
	}} {
		t.Run(tc.desc, func(t *testing.T) {
			got := latestCommon(tc.candidates)
			switch {
			case got == nil && tc.want != "":
				t.Errorf("latestCommon() = nil, want %s", tc.want)
			case got != nil && got.Version != tc.want:
				t.Errorf("latestCommon() = %s, want %q", got.Version, tc.want)
			}
		})
	}
}

func TestIsNewer(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"1.26.11-r0", "1.26.11-r0", false},
		{"1.26.11-r1", "1.26.11-r0", true},
		{"1.26.9-r3", "1.26.11-r0", false},
		{"1.100.0-r0", "1.99.1-r3", true},
		{"1-r10", "1-r9", true},
		{"1.27.0_rc2-r0", "1.27.0-r0", false},
	} {
		got, err := isNewer(tc.a, tc.b)
		if err != nil {
			t.Fatalf("isNewer(%q, %q) = %v", tc.a, tc.b, err)
		}
		if got != tc.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}

	if _, err := isNewer("not a version", "1.0-r0"); err == nil {
		t.Error("isNewer() of an invalid version succeeded, want error")
	}
}

func TestUpdatePinsWithoutPins(t *testing.T) {
	in := "package:\n    name: foo\nenvironment:\n    contents:\n        repositories:\n            - http://127.0.0.1:1/os\n        packages:\n            - busybox\n"
	got, updates, err := updatePins(context.Background(), []byte(in))
	if err != nil || len(updates) != 0 || string(got) != in {
		t.Errorf("updatePins() = %q, %v, %v; want no fetch and no change", got, updates, err)
	}
}
