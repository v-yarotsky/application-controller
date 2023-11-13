package images_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/images"
	"git.home.yarotsky.me/vlad/application-controller/internal/testutil"
)

func TestImageFinderWithDigestStrategy(t *testing.T) {
	s := testutil.NewTestRegistry(t)
	t.Cleanup(s.Close)

	finder, err := images.NewImageFinder()
	if err != nil {
		t.Error(err)
	}

	versionSpec := yarotskymev1alpha1.ImageSpec{
		Repository:      fmt.Sprintf("%s/foo", s.URL.Host),
		VersionStrategy: "Digest",
		Digest: &yarotskymev1alpha1.VersionStrategyDigestSpec{
			Tag: "latest",
		},
	}

	_, err = finder.FindImage(context.Background(), versionSpec)
	if !strings.Contains(err.Error(), "failed to retrieve digest") {
		t.Errorf("the finder should not have found an image, but got error: %s", err)
	}

	oldFoo := s.MustUpsertTag("foo", "latest")
	mustFindImage(t, finder, versionSpec, oldFoo)

	newFoo := s.MustUpsertTag("foo", "latest")
	mustFindImage(t, finder, versionSpec, newFoo)
}

func TestImageFinderWithSemVerStrategy(t *testing.T) {
	s := testutil.NewTestRegistry(t)
	t.Cleanup(s.Close)

	finder, err := images.NewImageFinder()
	if err != nil {
		t.Error(err)
	}

	versionSpec := yarotskymev1alpha1.ImageSpec{
		Repository:      fmt.Sprintf("%s/foo", s.URL.Host),
		VersionStrategy: "SemVer",
		SemVer: &yarotskymev1alpha1.VersionStrategySemVerSpec{
			Constraint: "^1.1.0",
		},
	}

	_, err = finder.FindImage(context.Background(), versionSpec)
	if !strings.Contains(err.Error(), "failed to fetch list of tags") {
		t.Errorf("the finder should not have found an image, but got error: %s", err)
	}

	_ = s.MustUpsertTag("foo", "latest")
	_, err = finder.FindImage(context.Background(), versionSpec)
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("the finder should not have found an image, but got error: %s", err)
	}

	_ = s.MustUpsertTag("foo", "1.1.0")
	want := s.MustUpsertTag("foo", "v1.2.0") // also checks that the `v` prefix is ignored.
	_ = s.MustUpsertTag("foo", "2.0.0")
	mustFindImage(t, finder, versionSpec, want)

}

func mustFindImage(t *testing.T, finder images.ImageFinder, spec yarotskymev1alpha1.ImageSpec, want *images.ImageRef) {
	t.Helper()

	got, err := finder.FindImage(context.Background(), spec)
	if err != nil {
		t.Error(err)
	}

	if *got != *want {
		t.Errorf("want: %s, got: %s", want, got)
	}
}
