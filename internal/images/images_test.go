package images_test

import (
	"context"
	"fmt"
	"testing"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/images"
	"git.home.yarotsky.me/vlad/application-controller/internal/testutil"
	. "github.com/onsi/gomega"
)

func TestImageFinder(t *testing.T) {
	ctx := context.Background()
	g := NewWithT(t)
	s := testutil.NewTestRegistry(t)
	t.Cleanup(s.Close)

	oldFoo := s.MustUpsertTag("foo", "latest")

	finder, err := images.NewImageFinder()
	g.Expect(err).NotTo(HaveOccurred())

	versionSpec := yarotskymev1alpha1.ImageSpec{
		Repository:      fmt.Sprintf("%s/foo", s.URL.Host),
		VersionStrategy: "Digest",
		Digest: yarotskymev1alpha1.VersionStrategyDigestSpec{
			Tag: "latest",
		},
	}
	ref, err := finder.FindImage(ctx, versionSpec)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ref).To(Equal(oldFoo.String()))

	newFoo := s.MustUpsertTag("foo", "latest")

	ref, err = finder.FindImage(ctx, versionSpec)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ref).To(Equal(newFoo.String()))
}
