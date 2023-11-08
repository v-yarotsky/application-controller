package controller

import (
	"fmt"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/gkutil"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
)

type Namer interface {
	ApplicationName() types.NamespacedName
	DeploymentName() types.NamespacedName
	ServiceAccountName() types.NamespacedName
	ServiceName() types.NamespacedName
	IngressName() types.NamespacedName
	RoleBindingName(roleRef rbacv1.RoleRef) (types.NamespacedName, error)
	ClusterRoleBindingName(roleRef rbacv1.RoleRef) (types.NamespacedName, error)

	SelectorLabels() map[string]string
}

type simpleNamer struct {
	*yarotskymev1alpha1.Application
}

var _ Namer = &simpleNamer{nil}

func (a *simpleNamer) ApplicationName() types.NamespacedName {
	return types.NamespacedName{
		Name:      a.Name,
		Namespace: a.Namespace,
	}
}

func (a *simpleNamer) DeploymentName() types.NamespacedName {
	return types.NamespacedName{
		Name:      a.Name,
		Namespace: a.Namespace,
	}
}

func (a *simpleNamer) ServiceAccountName() types.NamespacedName {
	return types.NamespacedName{
		Name:      a.Name,
		Namespace: a.Namespace,
	}
}

func (a *simpleNamer) ServiceName() types.NamespacedName {
	return types.NamespacedName{
		Name:      a.Name,
		Namespace: a.Namespace,
	}
}

func (a *simpleNamer) IngressName() types.NamespacedName {
	return types.NamespacedName{
		Name:      a.Name,
		Namespace: a.Namespace,
	}
}

func (a *simpleNamer) RoleBindingName(roleRef rbacv1.RoleRef) (types.NamespacedName, error) {
	gk := gkutil.FromRoleRef(roleRef)
	if gkutil.IsClusterRole(gk) {
		return types.NamespacedName{
			Name:      fmt.Sprintf("%s-%s-%s", a.Name, "clusterrole", roleRef.Name),
			Namespace: a.Namespace,
		}, nil
	} else if gkutil.IsRole(gk) {
		return types.NamespacedName{
			Name:      fmt.Sprintf("%s-%s-%s", a.Name, "role", roleRef.Name),
			Namespace: a.Namespace,
		}, nil
	} else {
		return types.NamespacedName{}, fmt.Errorf("Cannot create role binding name for %s", gk.String())
	}
}

func (a *simpleNamer) ClusterRoleBindingName(roleRef rbacv1.RoleRef) (types.NamespacedName, error) {
	gk := gkutil.FromRoleRef(roleRef)
	if !gkutil.IsClusterRole(gk) {
		return types.NamespacedName{}, fmt.Errorf("Cannot create cluster role binding name for %s", gk.String())
	}
	return types.NamespacedName{
		Name: fmt.Sprintf("%s-%s-%s-%s", a.Namespace, a.Name, "clusterrole", roleRef.Name),
	}, nil
}

func (a *simpleNamer) SelectorLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       a.Name,
		"app.kubernetes.io/managed-by": "application-controller",
		"app.kubernetes.io/instance":   "default",
		"app.kubernetes.io/version":    "0.1.0",
	}
}
