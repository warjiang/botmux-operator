package controller

import (
	"strings"
	"testing"

	botmuxv1alpha1 "github.com/warjiang/botmux-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDesiredStatefulSetEnforcesSingleInstanceContract(t *testing.T) {
	user := testUser()
	user.Spec.Ingress.Enabled = true
	user.Spec.Ingress.Host = "alice.example.com"
	user.Spec.Ingress.TLSSecretName = "alice-tls"
	sts := desiredStatefulSet(user, "runtime:test", "operator:test", "revision")

	if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 1 {
		t.Fatalf("replicas = %v, want 1", sts.Spec.Replicas)
	}
	if sts.Spec.PodManagementPolicy != appsv1.OrderedReadyPodManagement {
		t.Fatalf("podManagementPolicy = %s", sts.Spec.PodManagementPolicy)
	}
	if len(sts.Spec.Template.Spec.Containers) != 2 {
		t.Fatalf("containers = %d, want daemon and dashboard", len(sts.Spec.Template.Spec.Containers))
	}
	daemon := sts.Spec.Template.Spec.Containers[0]
	if daemon.Name != "daemon" || daemon.Image != "runtime:test" {
		t.Fatalf("unexpected daemon: %#v", daemon)
	}
	if value := envValue(daemon.Env, "BOTMUX_PUBLIC_URL"); value != "https://alice.example.com" {
		t.Fatalf("BOTMUX_PUBLIC_URL = %q", value)
	}
	if daemon.SecurityContext == nil || daemon.SecurityContext.AllowPrivilegeEscalation == nil || *daemon.SecurityContext.AllowPrivilegeEscalation {
		t.Fatal("daemon must disallow privilege escalation")
	}
	if got := sts.Spec.Template.Annotations["botmux.io/credentials-revision"]; got != "revision" {
		t.Fatalf("credentials revision = %q", got)
	}
}

func TestDesiredStatefulSetSuspendedAndSandboxProbe(t *testing.T) {
	user := testUser()
	user.Spec.Suspend = true
	user.Spec.Runtime.Sandbox = true
	sts := desiredStatefulSet(user, "runtime:test", "operator:test", "revision")
	if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 0 {
		t.Fatalf("replicas = %v, want 0", sts.Spec.Replicas)
	}
	if len(sts.Spec.Template.Spec.InitContainers) != 2 || sts.Spec.Template.Spec.InitContainers[1].Name != "sandbox-probe" {
		t.Fatalf("sandbox probe missing: %#v", sts.Spec.Template.Spec.InitContainers)
	}
}

func TestCredentialsRevisionIsOrderIndependent(t *testing.T) {
	a := credentialsRevision(map[string]string{"a": "1", "b": "2"})
	b := credentialsRevision(map[string]string{"b": "2", "a": "1"})
	if a != b {
		t.Fatalf("revision depends on map order: %s != %s", a, b)
	}
	if a == credentialsRevision(map[string]string{"a": "2", "b": "2"}) {
		t.Fatal("revision did not change with a Secret resourceVersion")
	}
}

func TestLabelsForLongUserName(t *testing.T) {
	user := testUser()
	user.Name = "a" + strings.Repeat("b", 100)
	value := labelsFor(user)["botmux.io/user"]
	if len(value) > 63 {
		t.Fatalf("user label length = %d, want at most 63", len(value))
	}
	if value != labelsFor(user)["botmux.io/user"] {
		t.Fatal("user label is not stable")
	}
}

func testUser() *botmuxv1alpha1.BotmuxUser {
	return &botmuxv1alpha1.BotmuxUser{
		ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: "default", Generation: 1},
		Spec: botmuxv1alpha1.BotmuxUserSpec{
			Lark: botmuxv1alpha1.LarkSpec{
				AppID: "cli_alice", Brand: "feishu",
				CredentialsSecretRef: botmuxv1alpha1.SecretReference{Name: "alice-lark"},
			},
			Runtime: botmuxv1alpha1.RuntimeSpec{
				CLIID: "codex", Backend: "tmux",
				EnvFromSecretRefs: []botmuxv1alpha1.SecretReference{{Name: "alice-provider"}},
			},
			Workspace: botmuxv1alpha1.WorkspaceSpec{
				Size: resource.MustParse("20Gi"), WorkingDir: "/workspace", ReclaimPolicy: "Retain",
			},
		},
	}
}

func envValue(env []corev1.EnvVar, name string) string {
	for _, item := range env {
		if item.Name == name {
			return item.Value
		}
	}
	return ""
}
