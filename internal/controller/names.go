package controller

import (
	"fmt"
	"strings"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
)

type Namer interface {
	ApplicationName() types.NamespacedName
	DeploymentName() types.NamespacedName
	ServiceAccountName() types.NamespacedName
	ServiceName() types.NamespacedName
	LBServiceName() types.NamespacedName
	PodMonitorName() types.NamespacedName
	IngressRouteName() types.NamespacedName
	RoleBindingName(roleRef rbacv1.RoleRef) types.NamespacedName
	ClusterRoleBindingName(roleRef rbacv1.RoleRef) types.NamespacedName
	CronJobName(jobName string) types.NamespacedName

	SelectorLabels() map[string]string
	CronJobSelectorLabels(jobName string) map[string]string
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

func (a *simpleNamer) LBServiceName() types.NamespacedName {
	return types.NamespacedName{
		Name:      fmt.Sprintf("%s-loadbalancer", a.Name),
		Namespace: a.Namespace,
	}
}

func (a *simpleNamer) PodMonitorName() types.NamespacedName {
	return types.NamespacedName{
		Name:      a.Name,
		Namespace: a.Namespace,
	}
}

func (a *simpleNamer) IngressRouteName() types.NamespacedName {
	return types.NamespacedName{
		Name:      a.Name,
		Namespace: a.Namespace,
	}
}

func (a *simpleNamer) RoleBindingName(roleRef rbacv1.RoleRef) types.NamespacedName {
	return types.NamespacedName{
		Name:      fmt.Sprintf("%s-%s-%s", a.Name, strings.ToLower(roleRef.Kind), roleRef.Name),
		Namespace: a.Namespace,
	}
}

func (a *simpleNamer) ClusterRoleBindingName(roleRef rbacv1.RoleRef) types.NamespacedName {
	return types.NamespacedName{
		Name: fmt.Sprintf("%s-%s-%s-%s", a.Namespace, a.Name, strings.ToLower(roleRef.Kind), roleRef.Name),
	}
}

func (a *simpleNamer) CronJobName(jobName string) types.NamespacedName {
	return types.NamespacedName{
		Name:      fmt.Sprintf("%s-%s", a.Name, jobName),
		Namespace: a.Namespace,
	}
}

func (a *simpleNamer) SelectorLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       a.Name,
		"app.kubernetes.io/managed-by": Name,
		"app.kubernetes.io/instance":   "default",
	}
}

func (a *simpleNamer) CronJobSelectorLabels(jobName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       jobName,
		"app.kubernetes.io/managed-by": Name,
		"app.kubernetes.io/part-of":    a.Name,
		"app.kubernetes.io/instance":   "default",
	}
}
