package testutil

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"git.home.yarotsky.me/vlad/application-controller/internal/images"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/uuid"
)

type TestRegistry struct {
	t *testing.T
	*httptest.Server
	URL *url.URL
}

func NewTestRegistry(t *testing.T) *TestRegistry {
	s := httptest.NewServer(registry.New())
	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatalf("failed to parse test registry url: %v", err)
	}
	return &TestRegistry{t, s, u}
}

// MustUpsertTag inserts/updates a given tag with random manifest contents,
// ensuring a unique digest after each call.
func (r *TestRegistry) MustUpsertTag(repoWithoutHost, tag string) *images.ImageRef {
	url := *r.URL
	url.Path = fmt.Sprintf("/v2/%s/manifests/%s", repoWithoutHost, tag)

	// This ensures a different digest on subsequent tag "uploads"
	randomContents := uuid.New().String()
	req := &http.Request{
		Method: "PUT",
		URL:    &url,
		Body:   io.NopCloser(strings.NewReader(randomContents)),
	}
	resp, err := r.Client().Do(req)
	if err != nil {
		r.t.Fatalf("Error uploading manifest: %v", err)
	}

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		r.t.Fatalf("Error uploading manifest got status: %d %s", resp.StatusCode, body)
	}
	digest := resp.Header.Get("Docker-Content-Digest")

	return &images.ImageRef{
		Repository: fmt.Sprintf("%s/%s", url.Host, repoWithoutHost),
		Tag:        tag,
		Digest:     digest,
	}
}
