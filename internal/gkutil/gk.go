package gkutil

import (
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func FromRoleRef(r rbacv1.RoleRef) schema.GroupKind {
	return schema.GroupKind{
		Group: r.APIGroup,
		Kind:  r.Kind,
	}
}

func FromOwnerReference(r *metav1.OwnerReference) schema.GroupKind {
	if r == nil {
		return schema.EmptyObjectKind.GroupVersionKind().GroupKind()
	}
	gv, _ := schema.ParseGroupVersion(r.APIVersion)
	return schema.GroupKind{
		Group: gv.Group,
		Kind:  r.Kind,
	}
}

func IsClusterRole(gk schema.GroupKind) bool {
	return gk == schema.GroupKind{
		Group: rbacv1.SchemeGroupVersion.Group,
		Kind:  "ClusterRole",
	}
}

func IsRole(gk schema.GroupKind) bool {
	return gk == schema.GroupKind{
		Group: rbacv1.SchemeGroupVersion.Group,
		Kind:  "Role",
	}
}

func IsApplication(gk schema.GroupKind) bool {
	return gk == schema.GroupKind{
		Group: "yarotsky.me",
		Kind:  "Application",
	}
}
