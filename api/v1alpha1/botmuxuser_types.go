package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ReclaimPolicyRetain = "Retain"
	ReclaimPolicyDelete = "Delete"

	BrandFeishu = "feishu"
	BrandLark   = "lark"

	BackendTmux = "tmux"
)

type SecretReference struct {
	// Name is the name of a Secret in the BotmuxUser namespace.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

type LarkSpec struct {
	// AppID is the Feishu/Lark application ID. It is immutable after creation.
	// +kubebuilder:validation:MinLength=1
	AppID string `json:"appId"`

	// +kubebuilder:validation:Enum=feishu;lark
	// +kubebuilder:default=feishu
	Brand string `json:"brand,omitempty"`

	CredentialsSecretRef SecretReference `json:"credentialsSecretRef"`
}

type RuntimeSpec struct {
	// CLIID selects the botmux CLI adapter.
	// +kubebuilder:validation:MinLength=1
	CLIID string `json:"cliId"`

	// Image overrides the image catalog entry for CLIID.
	// +optional
	Image string `json:"image,omitempty"`

	// +optional
	Model string `json:"model,omitempty"`

	// +kubebuilder:validation:Enum=tmux
	// +kubebuilder:default=tmux
	Backend string `json:"backend,omitempty"`

	// EnvFromSecretRefs are exposed only to the daemon container and its CLI children.
	// +optional
	EnvFromSecretRefs []SecretReference `json:"envFromSecretRefs,omitempty"`

	// +kubebuilder:default=false
	Sandbox bool `json:"sandbox,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="!has(self.size) || quantity(self.size).isGreaterThan(quantity('0'))",message="spec.workspace.size must be greater than zero"
type WorkspaceSpec struct {
	// +kubebuilder:default="20Gi"
	Size resource.Quantity `json:"size,omitempty"`

	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// +kubebuilder:default="/workspace"
	// +kubebuilder:validation:Pattern=`^/workspace(/.*)?$`
	WorkingDir string `json:"workingDir,omitempty"`

	// +kubebuilder:validation:Enum=Retain;Delete
	// +kubebuilder:default=Retain
	ReclaimPolicy string `json:"reclaimPolicy,omitempty"`
}

type IngressSpec struct {
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// +optional
	Host string `json:"host,omitempty"`

	// +optional
	ClassName *string `json:"className,omitempty"`

	// +optional
	TLSSecretName string `json:"tlsSecretName,omitempty"`

	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

type SchedulingSpec struct {
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`
}

// BotmuxUserSpec defines the desired state of BotmuxUser.
// +kubebuilder:validation:XValidation:rule="self.lark.appId == oldSelf.lark.appId",message="spec.lark.appId is immutable"
// +kubebuilder:validation:XValidation:rule="!self.ingress.enabled || size(self.ingress.host) > 0",message="spec.ingress.host is required when ingress is enabled"
type BotmuxUserSpec struct {
	Lark       LarkSpec                    `json:"lark"`
	Runtime    RuntimeSpec                 `json:"runtime"`
	Workspace  WorkspaceSpec               `json:"workspace,omitempty"`
	Resources  corev1.ResourceRequirements `json:"resources,omitempty"`
	Ingress    IngressSpec                 `json:"ingress,omitempty"`
	Scheduling SchedulingSpec              `json:"scheduling,omitempty"`

	// Suspend scales the user's StatefulSet to zero without deleting storage.
	// +kubebuilder:default=false
	Suspend bool `json:"suspend,omitempty"`
}

// BotmuxUserStatus defines the observed state of BotmuxUser.
type BotmuxUserStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Phase              string             `json:"phase,omitempty"`
	PodName            string             `json:"podName,omitempty"`
	ServiceName        string             `json:"serviceName,omitempty"`
	DashboardURL       string             `json:"dashboardURL,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=bmu
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.status.dashboardURL`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type BotmuxUser struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BotmuxUserSpec   `json:"spec,omitempty"`
	Status BotmuxUserStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type BotmuxUserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BotmuxUser `json:"items"`
}
