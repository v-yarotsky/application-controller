/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type ImageSpec struct {
	// OCI Image repository.
	Repository string `json:"repository"`

	// Version selection strategy, e.g. `digest`.
	// Available strategies:
	// - `digest` - looks for the most recently uploaded digest of a mutable tag
	VersionStrategy string                `json:"versionStrategy"`
	Digest          VersionStrategyDigest `json:"digest,omitempty"`
}

type VersionStrategyDigest struct {
	// Mutable tag to watch for new digests.
	Tag string `json:"tag"`
}

// ApplicationSpec defines the desired state of Application
type ApplicationSpec struct {
	// Important: Run "make" to regenerate code after modifying this file

	// Image defines the image for the primary container.
	Image ImageSpec `json:"image"`

	// Entrypoint array. Not executed within a shell.
	// The container image's ENTRYPOINT is used if this is not provided.
	// Variable references $(VAR_NAME) are expanded using the container's environment. If a variable
	// cannot be resolved, the reference in the input string will be unchanged. Double $$ are reduced
	// to a single $, which allows for escaping the $(VAR_NAME) syntax: i.e. "$$(VAR_NAME)" will
	// produce the string literal "$(VAR_NAME)". Escaped references will never be expanded, regardless
	// of whether the variable exists or not. Cannot be updated.
	// More info: https://kubernetes.io/docs/tasks/inject-data-application/define-command-argument-container/#running-a-command-in-a-shell
	// +optional
	Command []string `json:"command,omitempty"`

	// Arguments to the entrypoint.
	// The container image's CMD is used if this is not provided.
	// Variable references $(VAR_NAME) are expanded using the container's environment. If a variable
	// cannot be resolved, the reference in the input string will be unchanged. Double $$ are reduced
	// to a single $, which allows for escaping the $(VAR_NAME) syntax: i.e. "$$(VAR_NAME)" will
	// produce the string literal "$(VAR_NAME)". Escaped references will never be expanded, regardless
	// of whether the variable exists or not. Cannot be updated.
	// More info: https://kubernetes.io/docs/tasks/inject-data-application/define-command-argument-container/#running-a-command-in-a-shell
	// +optional
	Args []string `json:"args,omitempty"`

	// List of ports to expose from the container. Not specifying a port here
	// DOES NOT prevent that port from being exposed. Any port which is
	// listening on the default "0.0.0.0" address inside a container will be
	// accessible from the network.
	// Modifying this array with strategic merge patch may corrupt the data.
	// For more information See https://github.com/kubernetes/kubernetes/issues/108255.
	// Cannot be updated.
	// +optional
	// +patchMergeKey=containerPort
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=containerPort
	// +listMapKey=protocol
	Ports []corev1.ContainerPort `json:"ports,omitempty" patchStrategy:"merge" patchMergeKey:"containerPort"`

	// List of environment variables to set in the container.
	// Cannot be updated.
	// +optional
	// +patchMergeKey=name
	// +patchStrategy=merge
	Env []corev1.EnvVar `json:"env,omitempty" patchStrategy:"merge" patchMergeKey:"name"`

	// Compute Resources required by this container.
	// Cannot be updated.
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Periodic probe of container liveness.
	// Container will be restarted if the probe fails.
	// Cannot be updated.
	// More info: https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle#container-probes
	// +optional
	LivenessProbe *corev1.Probe `json:"livenessProbe,omitempty"`

	// Periodic probe of container service readiness.
	// Container will be removed from service endpoints if the probe fails.
	// Cannot be updated.
	// More info: https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle#container-probes
	// +optional
	ReadinessProbe *corev1.Probe `json:"readinessProbe,omitempty"`

	// StartupProbe indicates that the Pod has successfully initialized.
	// If specified, no other probes are executed until this completes successfully.
	// If this probe fails, the Pod will be restarted, just as if the livenessProbe failed.
	// This can be used to provide different probe parameters at the beginning of a Pod's lifecycle,
	// when it might take a long time to load data or warm a cache, than during steady-state operation.
	// This cannot be updated.
	// More info: https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle#container-probes
	// +optional
	StartupProbe *corev1.Probe `json:"startupProbe,omitempty"`

	// SecurityContext defines the security options the container should be run with.
	// If set, the fields of SecurityContext override the equivalent fields of PodSecurityContext.
	// More info: https://kubernetes.io/docs/tasks/configure-pod-container/security-context/
	// +optional
	SecurityContext *corev1.SecurityContext `json:"securityContext,omitempty"`

	// List of volumes to mount into the primary container.
	// More info: https://kubernetes.io/docs/concepts/storage/volumes
	// +optional
	// +patchMergeKey=name
	// +patchStrategy=merge,retainKeys
	Volumes []Volume `json:"volumes,omitempty"`

	Ingress *Ingress `json:"ingress,omitempty"`

	Metrics Metrics `json:"metrics,omitempty"`

	Roles []rbacv1.RoleRef `json:"roles,omitempty"`
}

type Volume struct {
	corev1.Volume `json:",inline"`

	// Mounted read-only if true, read-write otherwise (false or unspecified).
	// Defaults to false.
	// +optional
	ReadOnly bool `json:"readOnly,omitempty"`
	// Path within the container at which the volume should be mounted.  Must
	// not contain ':'.
	MountPath string `json:"mountPath"`
}

type Ingress struct {
	// ingressClassName is the name of an IngressClass cluster resource.
	IngressClassName *string `json:"ingressClassName,omitempty"`

	// host is the fully qualified domain name of a network host, as defined by RFC 3986.
	// Note the following deviations from the "host" part of the
	// URI as defined in RFC 3986:
	// 1. IPs are not allowed. Currently an IngressRuleValue can only apply to
	//    the IP in the Spec of the parent Ingress.
	// 2. The `:` delimiter is not respected because ports are not allowed.
	//	  Currently the port of an Ingress is implicitly :80 for http and
	//	  :443 for https.
	// Both these may change in the future.
	// Incoming requests are matched against the host before the
	// IngressRuleValue. If the host is unspecified, the Ingress routes all
	// traffic based on the specified IngressRuleValue.
	//
	// host can be "precise" which is a domain name without the terminating dot of
	// a network host (e.g. "foo.bar.com") or "wildcard", which is a domain name
	// prefixed with a single wildcard label (e.g. "*.foo.com").
	// The wildcard character '*' must appear by itself as the first DNS label and
	// matches only a single label. You cannot have a wildcard label by itself (e.g. Host == "*").
	// Requests will be matched against the Host field in the following way:
	// 1. If host is precise, the request matches this rule if the http host header is equal to Host.
	// 2. If host is a wildcard, then the request matches this rule if the http host header
	// is to equal to the suffix (removing the first label) of the wildcard rule.
	// +optional
	Host string `json:"host,omitempty"`

	// Guesses `web` or `http` by default.
	PortName string `json:"portName,omitempty"`
}

type Metrics struct {
	// Guesses `/metrics` by default
	Path string `json:"path,omitempty"`

	// Guesses `metrics` or `prometheus` by default.
	PortName string `json:"portName,omitempty"`
}

// ApplicationStatus defines the observed state of Application
type ApplicationStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Application is the Schema for the applications API
type Application struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ApplicationSpec   `json:"spec,omitempty"`
	Status ApplicationStatus `json:"status,omitempty"`
}

func (a *Application) DeploymentName() types.NamespacedName {
	return types.NamespacedName{
		Name:      a.Name,
		Namespace: a.Namespace,
	}
}

func (a *Application) SelectorLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       a.Name,
		"app.kubernetes.io/managed-by": "application-controller",
		"app.kubernetes.io/instance":   "default",
		"app.kubernetes.io/version":    "0.1.0",
	}
}

func (a *Application) ServiceAccountName() types.NamespacedName {
	return types.NamespacedName{
		Name:      a.Name,
		Namespace: a.Namespace,
	}
}

func (a *Application) ServiceName() types.NamespacedName {
	return types.NamespacedName{
		Name:      a.Name,
		Namespace: a.Namespace,
	}
}

func (a *Application) IngressName() types.NamespacedName {
	return types.NamespacedName{
		Name:      a.Name,
		Namespace: a.Namespace,
	}
}

func (a *Application) RoleBindingNameForRoleRef(roleRef rbacv1.RoleRef) (types.NamespacedName, error) {
	if roleRef.APIGroup == "rbac.authorization.k8s.io" && roleRef.Kind == "ClusterRole" {
		return types.NamespacedName{
			Name:      fmt.Sprintf("%s-%s-%s", a.Name, "clusterrole", roleRef.Name),
			Namespace: a.Namespace,
		}, nil
	} else if roleRef.APIGroup == "rbac.authorization.k8s.io" && roleRef.Kind == "Role" {
		return types.NamespacedName{
			Name:      fmt.Sprintf("%s-%s-%s", a.Name, "role", roleRef.Name),
			Namespace: a.Namespace,
		}, nil
	} else {
		return types.NamespacedName{}, fmt.Errorf("Cannot create role binding name for %s/%s", roleRef.APIGroup, roleRef.Kind)
	}
}

//+kubebuilder:object:root=true

// ApplicationList contains a list of Application
type ApplicationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Application `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Application{}, &ApplicationList{})
}
