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
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
	// of whether the variable exists or not.
	// More info: https://kubernetes.io/docs/tasks/inject-data-application/define-command-argument-container/#running-a-command-in-a-shell
	// +optional
	Command []string `json:"command,omitempty"`

	// Arguments to the entrypoint.
	// The container image's CMD is used if this is not provided.
	// Variable references $(VAR_NAME) are expanded using the container's environment. If a variable
	// cannot be resolved, the reference in the input string will be unchanged. Double $$ are reduced
	// to a single $, which allows for escaping the $(VAR_NAME) syntax: i.e. "$$(VAR_NAME)" will
	// produce the string literal "$(VAR_NAME)". Escaped references will never be expanded, regardless
	// of whether the variable exists or not.
	// More info: https://kubernetes.io/docs/tasks/inject-data-application/define-command-argument-container/#running-a-command-in-a-shell
	// +optional
	Args []string `json:"args,omitempty"`

	// List of ports to expose from the container. Not specifying a port here
	// DOES NOT prevent that port from being exposed. Any port which is
	// listening on the default "0.0.0.0" address inside a container will be
	// accessible from the network.
	// Modifying this array with strategic merge patch may corrupt the data.
	// For more information See https://github.com/kubernetes/kubernetes/issues/108255.
	// +optional
	// +patchMergeKey=containerPort
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=containerPort
	// +listMapKey=protocol
	Ports []corev1.ContainerPort `json:"ports,omitempty" patchStrategy:"merge" patchMergeKey:"containerPort"`

	// List of environment variables to set in the container.
	// +optional
	// +patchMergeKey=name
	// +patchStrategy=merge
	Env []corev1.EnvVar `json:"env,omitempty" patchStrategy:"merge" patchMergeKey:"name"`

	// List of sources to populate environment variables in the container.
	// The keys defined within a source must be a C_IDENTIFIER. All invalid keys
	// will be reported as an event when the container is starting. When a key exists in multiple
	// sources, the value associated with the last source will take precedence.
	// Values defined by an Env with a duplicate key will take precedence.
	// +optional
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`

	// Compute Resources required by this container.
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Periodic probe of container liveness/readiness.
	// Container won't be put in service unless the probe passes, and will be restarted if the probe fails.
	// More info: https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle#container-probes
	// +optional
	Probe *corev1.Probe `json:"probe,omitempty"`

	// SecurityContext holds pod-level security attributes and container settings.
	SecurityContext *SecurityContext `json:"securityContext,omitempty"`

	// List of volumes to mount into the primary container.
	// More info: https://kubernetes.io/docs/concepts/storage/volumes
	// +optional
	// +patchMergeKey=name
	// +patchStrategy=merge,retainKeys
	Volumes []Volume `json:"volumes,omitempty"`

	// +optional
	Ingress *Ingress `json:"ingress,omitempty"`

	// LoadBalancer creates a separate Service with type LoadBalancer,
	// and exposes specified ports. It supports creating a DNS record
	// via an external-dns annotation, the value of which is controlled
	// with the `host` field.
	// +optional
	LoadBalancer *LoadBalancer `json:"loadBalancer,omitempty"`

	// +optional
	Metrics *Metrics `json:"metrics,omitempty"`

	// List of Roles and ClusterRoles to bind to the ServiceAccount of
	// this application.
	// +optional
	// +patchMergeKey=name
	// +patchStrategy=merge,retainKeys
	Roles []ScopedRoleRef `json:"roles,omitempty"`

	// Use the host's network namespace.
	// If this option is set, the ports that will be used must be specified.
	// Default to false.
	// +k8s:conversion-gen=false
	// +optional
	HostNetwork bool `json:"hostNetwork,omitempty"`
}

type ImageSpec struct {
	// OCI Image repository.
	Repository string `json:"repository"`

	// Cron-like expression determining update check schedule.
	// Ref: https://en.wikipedia.org/wiki/Cron
	UpdateSchedule *string `json:"updateSchedule,omitempty"`

	// Version selection strategy, e.g. `Digest`.
	// +kubebuilder:validation:Enum=Digest;SemVer
	VersionStrategy VersionStrategy            `json:"versionStrategy"`
	Digest          *VersionStrategyDigestSpec `json:"digest,omitempty"`
	SemVer          *VersionStrategySemVerSpec `json:"semver,omitempty"`
}

// VersionStrategy determines how the controller checks for the new
// versions of Application images.
// +enum
type VersionStrategy string

const (
	VersionStrategyDigest = VersionStrategy("Digest")
	VersionStrategySemver = VersionStrategy("SemVer")
)

type VersionStrategyDigestSpec struct {
	// Mutable tag to watch for new digests.
	Tag string `json:"tag"`
}

type VersionStrategySemVerSpec struct {
	// SemVer constraint as defined by https://github.com/Masterminds/semver
	// Examples:
	// - `1.x` (equivalent to `>= 1.0.0 < 2.0.0`
	// - `~1.2` (equivalent to `>= 1.2.0 < 2.0.0`
	// - `^1.2.3` (equivalent to `>= 1.2.3 < 2.0.0`
	Constraint string `json:"constraint"`
}

// SecurityContext is amalgamation of corev1.SecurityContext and corev1.PodSecurityContext,
// which overlap, but aren't the same.
type SecurityContext struct {
	*corev1.SecurityContext `json:",inline"`

	// A list of groups applied to the first process run in each container, in addition
	// to the container's primary GID, the fsGroup (if specified), and group memberships
	// defined in the container image for the uid of the container process. If unspecified,
	// no additional groups are added to any container. Note that group memberships
	// defined in the container image for the uid of the container process are still effective,
	// even if they are not included in this list.
	// Note that this field cannot be set when spec.os.name is windows.
	// +optional
	SupplementalGroups []int64 `json:"supplementalGroups,omitempty"`

	// A special supplemental group that applies to all containers in a pod.
	// Some volume types allow the Kubelet to change the ownership of that volume
	// to be owned by the pod:
	//
	// 1. The owning GID will be the FSGroup
	// 2. The setgid bit is set (new files created in the volume will be owned by FSGroup)
	// 3. The permission bits are OR'd with rw-rw----
	//
	// If unset, the Kubelet will not modify the ownership and permissions of any volume.
	// Note that this field cannot be set when spec.os.name is windows.
	// +optional
	FSGroup *int64 `json:"fsGroup,omitempty"`

	// Sysctls hold a list of namespaced sysctls used for the pod. Pods with unsupported
	// sysctls (by the container runtime) might fail to launch.
	// Note that this field cannot be set when spec.os.name is windows.
	// +optional
	Sysctls []corev1.Sysctl `json:"sysctls,omitempty"`
	// fsGroupChangePolicy defines behavior of changing ownership and permission of the volume
	// before being exposed inside Pod. This field will only apply to
	// volume types which support fsGroup based ownership(and permissions).
	// It will have no effect on ephemeral volume types such as: secret, configmaps
	// and emptydir.
	// Valid values are "OnRootMismatch" and "Always". If not specified, "Always" is used.
	// Note that this field cannot be set when spec.os.name is windows.
	// +optional
	FSGroupChangePolicy *corev1.PodFSGroupChangePolicy `json:"fsGroupChangePolicy,omitempty"`
}

// RoleBindingScope determines whether a namespaced RoleBinding
// or a cluster-scoped ClusterRoleBinding is created when referring
// to a ClusterRole.
// +enum
type RoleBindingScope string

const (
	RoleBindingScopeNamespace = RoleBindingScope("Namespace")
	RoleBindingScopeCluster   = RoleBindingScope("Cluster")
)

type ScopedRoleRef struct {
	rbacv1.RoleRef `json:",inline"`

	// +kubebuilder:validation:Enum=Cluster;Namespace
	Scope *RoleBindingScope `json:"scope,omitempty"`
}

func (r *ScopedRoleRef) ScopeOrDefault() RoleBindingScope {
	scope := RoleBindingScopeNamespace
	if r.Scope != nil {
		scope = *r.Scope
	}
	return scope
}

func RoleBindingScopePointer(s RoleBindingScope) *RoleBindingScope {
	return &s
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

	Auth *IngressAuth `json:"auth,omitempty"`

	// Guesses `web` or `http` by default.
	PortName string `json:"portName,omitempty"`

	TraefikMiddlewares []TraefikMiddlewareRef `json:"traefikMiddlewares,omitempty"`
}

type TraefikMiddlewareRef struct {
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

type IngressAuth struct {
	Enabled bool `json:"enabled"`
}

type LoadBalancer struct {
	// host is the fully qualified domain name of a network host, as defined by RFC 3986.
	// Note the following deviations from the "host" part of the
	// URI as defined in RFC 3986:
	// 1. IPs are not allowed.
	// 2. Ports are not allowed.
	Host string `json:"host"`

	// +kubebuilder:validation:MinItems=1
	PortNames []string `json:"portNames,omitempty"`
}

type Metrics struct {
	Enabled bool `json:"enabled"`

	// Guesses `/metrics` by default
	Path string `json:"path,omitempty"`

	// Guesses `metrics` or `prometheus` by default.
	PortName string `json:"portName,omitempty"`
}

const (
	ConditionReady = "Ready"
)

// ApplicationStatus defines the observed state of Application
type ApplicationStatus struct {
	Image               string             `json:"image"`
	ImageLastUpdateTime metav1.Time        `json:"imageLastUpdateTime,omitempty"`
	Conditions          []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=app
//+kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.status.image`
//+kubebuilder:printcolumn:name="Last Update",type=string,format=date-time,JSONPath=`.status.imageLastUpdateTime`

// Application is the Schema for the applications API
type Application struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ApplicationSpec   `json:"spec,omitempty"`
	Status ApplicationStatus `json:"status,omitempty"`
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
