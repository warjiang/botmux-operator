package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	botmuxv1alpha1 "github.com/warjiang/botmux-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	dataVolumeName          = "data"
	credentialsVolume       = "lark-credentials"
	tempVolumeName          = "tmp"
	homeDir                 = "/data/home/botmux"
	sessionDataDir          = "/data/home/botmux/.botmux/data"
	physicalWorkspace       = "/data/workspace"
	botmuxPackageRoot       = "/opt/botmux"
	dashboardPort     int32 = 7891
	daemonIPCURL            = "http://127.0.0.1:7950/healthz"
)

var defaultImageCatalog = map[string]string{
	"codex":       "ghcr.io/warjiang/botmux-runtime-codex:v0.1.0",
	"claude-code": "ghcr.io/warjiang/botmux-runtime-claude:v0.1.0",
	"gemini":      "ghcr.io/warjiang/botmux-runtime-gemini:v0.1.0",
}

func resolveRuntimeImage(user *botmuxv1alpha1.BotmuxUser) (string, bool) {
	if image := strings.TrimSpace(user.Spec.Runtime.Image); image != "" {
		return image, true
	}
	image, found := defaultImageCatalog[user.Spec.Runtime.CLIID]
	return image, found
}

func resourceBaseName(user *botmuxv1alpha1.BotmuxUser) string {
	raw := "botmux-" + user.Name
	if len(raw) <= 63 {
		return strings.TrimRight(raw, "-")
	}
	sum := sha256.Sum256([]byte(raw))
	return strings.TrimRight(raw[:54], "-") + "-" + hex.EncodeToString(sum[:])[:8]
}

func labelsFor(user *botmuxv1alpha1.BotmuxUser) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "botmux",
		"app.kubernetes.io/instance":   resourceBaseName(user),
		"app.kubernetes.io/managed-by": "botmux-operator",
		"botmux.io/user":               boundedLabelValue(user.Name),
	}
}

func boundedLabelValue(value string) string {
	if len(value) <= 63 {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return value[:54] + "-" + hex.EncodeToString(sum[:])[:8]
}

func effectiveBrand(user *botmuxv1alpha1.BotmuxUser) string {
	if user.Spec.Lark.Brand == "" {
		return botmuxv1alpha1.BrandFeishu
	}
	return user.Spec.Lark.Brand
}

func effectiveBackend(user *botmuxv1alpha1.BotmuxUser) string {
	if user.Spec.Runtime.Backend == "" {
		return botmuxv1alpha1.BackendTmux
	}
	return user.Spec.Runtime.Backend
}

func effectiveWorkingDir(user *botmuxv1alpha1.BotmuxUser) string {
	if user.Spec.Workspace.WorkingDir == "" {
		return "/workspace"
	}
	return user.Spec.Workspace.WorkingDir
}

func effectiveReclaimPolicy(user *botmuxv1alpha1.BotmuxUser) string {
	if user.Spec.Workspace.ReclaimPolicy == "" {
		return botmuxv1alpha1.ReclaimPolicyRetain
	}
	return user.Spec.Workspace.ReclaimPolicy
}

func effectiveStorageSize(user *botmuxv1alpha1.BotmuxUser) resource.Quantity {
	if user.Spec.Workspace.Size.IsZero() {
		return resource.MustParse("20Gi")
	}
	return user.Spec.Workspace.Size.DeepCopy()
}

func desiredPVC(user *botmuxv1alpha1.BotmuxUser) *corev1.PersistentVolumeClaim {
	name := resourceBaseName(user)
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: user.Namespace,
			Labels:    labelsFor(user),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: effectiveStorageSize(user)},
			},
			StorageClassName: user.Spec.Workspace.StorageClassName,
		},
	}
}

func desiredService(user *botmuxv1alpha1.BotmuxUser) *corev1.Service {
	name := resourceBaseName(user)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: user.Namespace, Labels: labelsFor(user)},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: labelsFor(user),
			Ports: []corev1.ServicePort{{
				Name: "dashboard", Port: 80, TargetPort: intstr.FromInt32(dashboardPort), Protocol: corev1.ProtocolTCP,
			}},
		},
	}
}

func desiredIngress(user *botmuxv1alpha1.BotmuxUser) *networkingv1.Ingress {
	name := resourceBaseName(user)
	pathType := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   user.Namespace,
			Labels:      labelsFor(user),
			Annotations: copyStringMap(user.Spec.Ingress.Annotations),
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: user.Spec.Ingress.ClassName,
			Rules: []networkingv1.IngressRule{{
				Host: user.Spec.Ingress.Host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{
						Path:     "/",
						PathType: &pathType,
						Backend: networkingv1.IngressBackend{
							Service: &networkingv1.IngressServiceBackend{
								Name: name,
								Port: networkingv1.ServiceBackendPort{Number: 80},
							},
						},
					}}},
				},
			}},
		},
	}
	if user.Spec.Ingress.TLSSecretName != "" {
		ingress.Spec.TLS = []networkingv1.IngressTLS{{
			Hosts: []string{user.Spec.Ingress.Host}, SecretName: user.Spec.Ingress.TLSSecretName,
		}}
	}
	return ingress
}

func desiredStatefulSet(
	user *botmuxv1alpha1.BotmuxUser,
	runtimeImage, operatorImage, credentialsRevision string,
) *appsv1.StatefulSet {
	name := resourceBaseName(user)
	replicas := int32(1)
	if user.Spec.Suspend {
		replicas = 0
	}
	runAsUser := int64(1000)
	runAsGroup := int64(1000)
	nonRoot := true
	allowPrivilegeEscalation := false
	readOnlyRoot := true
	terminationGrace := int64(90)
	mode := int32(0o400)
	fsGroupChangePolicy := corev1.FSGroupChangeOnRootMismatch
	labels := labelsFor(user)

	commonEnv := []corev1.EnvVar{
		{Name: "HOME", Value: homeDir},
		{Name: "SESSION_DATA_DIR", Value: sessionDataDir},
		{Name: "BOTMUX_BOT_INDEX", Value: "0"},
		{Name: "BOTMUX_DAEMON_IPC_BASE_PORT", Value: "7950"},
		{Name: "BOTMUX_WEB_PROXY_BASE_PORT", Value: "8800"},
		{Name: "BOTMUX_DASHBOARD_PORT", Value: "7891"},
		{Name: "WEB_HOST", Value: "0.0.0.0"},
		{Name: "BOTMUX_DASHBOARD_HOST", Value: "0.0.0.0"},
		{Name: "SHELL", Value: "/bin/bash"},
	}
	if user.Spec.Ingress.Enabled {
		commonEnv = append(commonEnv, corev1.EnvVar{Name: "BOTMUX_PUBLIC_URL", Value: dashboardURL(user)})
	}
	envFrom := make([]corev1.EnvFromSource, 0, len(user.Spec.Runtime.EnvFromSecretRefs))
	for _, ref := range user.Spec.Runtime.EnvFromSecretRefs {
		envFrom = append(envFrom, corev1.EnvFromSource{
			SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: ref.Name}},
		})
	}

	appSecurityContext := &corev1.SecurityContext{
		AllowPrivilegeEscalation: &allowPrivilegeEscalation,
		ReadOnlyRootFilesystem:   &readOnlyRoot,
		RunAsNonRoot:             &nonRoot,
		RunAsUser:                &runAsUser,
		RunAsGroup:               &runAsGroup,
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
	volumeMounts := []corev1.VolumeMount{
		{Name: dataVolumeName, MountPath: "/data"},
		{Name: dataVolumeName, MountPath: effectiveWorkingDir(user), SubPath: "workspace"},
		{Name: tempVolumeName, MountPath: "/tmp"},
	}
	daemonResources := user.Spec.Resources
	if len(daemonResources.Requests) == 0 && len(daemonResources.Limits) == 0 {
		daemonResources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi"),
			},
		}
	}
	dashboardResources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
	}
	execProbe := &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{
			"node", "-e", fmt.Sprintf("fetch(%q).then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))", daemonIPCURL),
		}}},
		InitialDelaySeconds: 10, PeriodSeconds: 10, TimeoutSeconds: 3, FailureThreshold: 6,
	}
	dashboardProbe := &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
			Path: "/__health", Port: intstr.FromInt32(dashboardPort), Scheme: corev1.URISchemeHTTP,
		}},
		InitialDelaySeconds: 3, PeriodSeconds: 10, TimeoutSeconds: 3, FailureThreshold: 6,
	}

	initContainers := []corev1.Container{{
		Name:            "bootstrap",
		Image:           operatorImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"/manager", "bootstrap"},
		Env: []corev1.EnvVar{
			{Name: "BOTMUX_BOOTSTRAP_HOME", Value: homeDir},
			{Name: "BOTMUX_BOOTSTRAP_WORKING_DIR", Value: effectiveWorkingDir(user)},
			{Name: "BOTMUX_BOOTSTRAP_WORKSPACE_PATH", Value: physicalWorkspace},
			{Name: "BOTMUX_BOOTSTRAP_APP_ID", Value: user.Spec.Lark.AppID},
			{Name: "BOTMUX_BOOTSTRAP_APP_SECRET_FILE", Value: "/var/run/botmux/credentials/appSecret"},
			{Name: "BOTMUX_BOOTSTRAP_BRAND", Value: effectiveBrand(user)},
			{Name: "BOTMUX_BOOTSTRAP_CLI_ID", Value: user.Spec.Runtime.CLIID},
			{Name: "BOTMUX_BOOTSTRAP_MODEL", Value: user.Spec.Runtime.Model},
			{Name: "BOTMUX_BOOTSTRAP_BACKEND", Value: effectiveBackend(user)},
			{Name: "BOTMUX_BOOTSTRAP_SANDBOX", Value: fmt.Sprintf("%t", user.Spec.Runtime.Sandbox)},
		},
		SecurityContext: appSecurityContext,
		VolumeMounts: []corev1.VolumeMount{
			{Name: dataVolumeName, MountPath: "/data"},
			{Name: credentialsVolume, MountPath: "/var/run/botmux/credentials", ReadOnly: true},
			{Name: tempVolumeName, MountPath: "/tmp"},
		},
	}}
	if user.Spec.Runtime.Sandbox {
		initContainers = append(initContainers, corev1.Container{
			Name:            "sandbox-probe",
			Image:           runtimeImage,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command: []string{"/bin/sh", "-ec",
				"command -v bwrap >/dev/null && bwrap --ro-bind / / --proc /proc --dev /dev /bin/true",
			},
			SecurityContext: appSecurityContext,
			VolumeMounts:    []corev1.VolumeMount{{Name: tempVolumeName, MountPath: "/tmp"}},
		})
	}

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: user.Namespace, Labels: labels},
		Spec: appsv1.StatefulSetSpec{
			Replicas:             &replicas,
			ServiceName:          name,
			PodManagementPolicy:  appsv1.OrderedReadyPodManagement,
			UpdateStrategy:       appsv1.StatefulSetUpdateStrategy{Type: appsv1.RollingUpdateStatefulSetStrategyType},
			RevisionHistoryLimit: int32Ptr(2),
			Selector:             &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
					Annotations: map[string]string{
						"botmux.io/credentials-revision": credentialsRevision,
					},
				},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: &terminationGrace,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:        &nonRoot,
						RunAsUser:           &runAsUser,
						RunAsGroup:          &runAsGroup,
						FSGroup:             &runAsGroup,
						FSGroupChangePolicy: &fsGroupChangePolicy,
						SeccompProfile:      &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					NodeSelector:   copyStringMap(user.Spec.Scheduling.NodeSelector),
					Tolerations:    append([]corev1.Toleration(nil), user.Spec.Scheduling.Tolerations...),
					Affinity:       user.Spec.Scheduling.Affinity.DeepCopy(),
					InitContainers: initContainers,
					Containers: []corev1.Container{
						{
							Name:            "daemon",
							Image:           runtimeImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{"node", botmuxPackageRoot + "/dist/index-daemon.js"},
							Env:             append([]corev1.EnvVar(nil), commonEnv...),
							EnvFrom:         envFrom,
							Resources:       daemonResources,
							SecurityContext: appSecurityContext,
							VolumeMounts:    volumeMounts,
							ReadinessProbe:  execProbe,
							LivenessProbe:   copyProbeWithDelay(execProbe, 60),
						},
						{
							Name:            "dashboard",
							Image:           runtimeImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{"node", botmuxPackageRoot + "/dist/dashboard.js"},
							Env:             append([]corev1.EnvVar(nil), commonEnv...),
							Ports:           []corev1.ContainerPort{{Name: "dashboard", ContainerPort: dashboardPort}},
							Resources:       dashboardResources,
							SecurityContext: appSecurityContext,
							VolumeMounts: []corev1.VolumeMount{
								{Name: dataVolumeName, MountPath: "/data"},
								{Name: tempVolumeName, MountPath: "/tmp"},
							},
							ReadinessProbe: dashboardProbe,
							LivenessProbe:  copyProbeWithDelay(dashboardProbe, 30),
						},
					},
					Volumes: []corev1.Volume{
						{Name: dataVolumeName, VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: name},
						}},
						{Name: credentialsVolume, VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName: user.Spec.Lark.CredentialsSecretRef.Name,
								Items:      []corev1.KeyToPath{{Key: "appSecret", Path: "appSecret", Mode: &mode}},
							},
						}},
						{Name: tempVolumeName, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}
}

func dashboardURL(user *botmuxv1alpha1.BotmuxUser) string {
	scheme := "http"
	if user.Spec.Ingress.TLSSecretName != "" {
		scheme = "https"
	}
	return scheme + "://" + user.Spec.Ingress.Host
}

func credentialsRevision(resourceVersions map[string]string) string {
	keys := make([]string, 0, len(resourceVersions))
	for key := range resourceVersions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		_, _ = fmt.Fprintf(hash, "%s=%s\n", key, resourceVersions[key])
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func copyProbeWithDelay(in *corev1.Probe, delay int32) *corev1.Probe {
	out := in.DeepCopy()
	out.InitialDelaySeconds = delay
	return out
}

func int32Ptr(value int32) *int32 {
	return &value
}
