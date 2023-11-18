package controller

import (
	"context"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	rbacv1 "k8s.io/api/rbac/v1"
)

type roleBindingMutator struct {
	namer Namer
}

func (f *roleBindingMutator) Mutate(ctx context.Context, app *yarotskymev1alpha1.Application, rb *rbacv1.RoleBinding, roleRef yarotskymev1alpha1.ScopedRoleRef) func() error {
	return func() error {
		serviceAccountName := f.namer.ServiceAccountName()

		rb.Subjects = []rbacv1.Subject{
			{
				APIGroup:  "",
				Kind:      "ServiceAccount",
				Name:      serviceAccountName.Name,
				Namespace: serviceAccountName.Namespace,
			},
		}
		rb.RoleRef = roleRef.RoleRef
		return nil
	}
}
