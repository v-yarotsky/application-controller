package images

import (
	"context"
	"fmt"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewSimpleApplicationLister(c client.Client) *simpleApplicationLister {
	return &simpleApplicationLister{c}
}

type simpleApplicationLister struct {
	client client.Client
}

func (lister *simpleApplicationLister) ListApplications(ctx context.Context) ([]yarotskymev1alpha1.Application, error) {
	list := &yarotskymev1alpha1.ApplicationList{}
	lookInAllNamespaces := client.InNamespace("")

	err := lister.client.List(ctx, list, lookInAllNamespaces)
	if err != nil {
		return nil, fmt.Errorf("failed to list Applications: %w", err)
	}
	return list.Items, nil
}

var _ ApplicationLister = &simpleApplicationLister{}
