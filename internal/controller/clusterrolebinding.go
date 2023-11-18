package controller

import (
	"context"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	rbacv1 "k8s.io/api/rbac/v1"
)

type clusterRoleBindingMutator struct {
	namer Namer
}

func (f *clusterRoleBindingMutator) Mutate(ctx context.Context, app *yarotskymev1alpha1.Application, crb *rbacv1.ClusterRoleBinding, roleRef yarotskymev1alpha1.ScopedRoleRef) func() error {
	return func() error {
		serviceAccountName := f.namer.ServiceAccountName()

		crb.Subjects = []rbacv1.Subject{
			{
				APIGroup:  "",
				Kind:      "ServiceAccount",
				Name:      serviceAccountName.Name,
				Namespace: serviceAccountName.Namespace,
			},
		}
		crb.RoleRef = roleRef.RoleRef
		return nil
	}
}
