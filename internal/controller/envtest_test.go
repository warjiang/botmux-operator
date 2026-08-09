//go:build envtest

package controller

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	botmuxv1alpha1 "github.com/warjiang/botmux-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func TestEnvtestReconcileAndCELValidation(t *testing.T) {
	testEnvironment := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	config, err := testEnvironment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := testEnvironment.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := botmuxv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	manager, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
	})
	if err != nil {
		t.Fatal(err)
	}
	reconciler := &BotmuxUserReconciler{
		Client: manager.GetClient(), Scheme: manager.GetScheme(), OperatorImage: "operator:test",
	}
	if err := reconciler.SetupWithManager(manager); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		if err := manager.Start(ctx); err != nil {
			t.Errorf("manager stopped: %v", err)
		}
	}()

	k8sClient := manager.GetClient()
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "botmux-envtest"}}
	if err := k8sClient.Create(ctx, namespace); err != nil {
		t.Fatal(err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "lark", Namespace: namespace.Name},
		Data:       map[string][]byte{"appSecret": []byte("secret")},
	}
	if err := k8sClient.Create(ctx, secret); err != nil {
		t.Fatal(err)
	}
	user := &botmuxv1alpha1.BotmuxUser{
		ObjectMeta: metav1.ObjectMeta{Name: "alice", Namespace: namespace.Name},
		Spec: botmuxv1alpha1.BotmuxUserSpec{
			Lark: botmuxv1alpha1.LarkSpec{
				AppID: "cli_envtest", CredentialsSecretRef: botmuxv1alpha1.SecretReference{Name: secret.Name},
			},
			Runtime: botmuxv1alpha1.RuntimeSpec{CLIID: "custom", Image: "runtime:test"},
			Workspace: botmuxv1alpha1.WorkspaceSpec{
				Size: resource.MustParse("1Gi"), WorkingDir: "/workspace", ReclaimPolicy: "Retain",
			},
		},
	}
	if err := k8sClient.Create(ctx, user); err != nil {
		t.Fatal(err)
	}

	key := client.ObjectKey{Namespace: namespace.Name, Name: resourceBaseName(user)}
	eventually(t, 10*time.Second, func() bool {
		return k8sClient.Get(ctx, key, &appsv1.StatefulSet{}) == nil
	})

	stored := &botmuxv1alpha1.BotmuxUser{}
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(user), stored); err != nil {
		t.Fatal(err)
	}
	if stored.Spec.Lark.Brand != botmuxv1alpha1.BrandFeishu {
		t.Fatalf("default brand = %q, want %q", stored.Spec.Lark.Brand, botmuxv1alpha1.BrandFeishu)
	}
	if stored.Spec.Runtime.Backend != botmuxv1alpha1.BackendTmux {
		t.Fatalf("default backend = %q, want %q", stored.Spec.Runtime.Backend, botmuxv1alpha1.BackendTmux)
	}
	stored.Spec.Lark.AppID = "cli_changed"
	if err := k8sClient.Update(ctx, stored); err == nil {
		t.Fatal("expected immutable appId update to be rejected by CEL validation")
	}

	invalidIngress := user.DeepCopy()
	invalidIngress.ResourceVersion = ""
	invalidIngress.UID = ""
	invalidIngress.Name = "invalid-ingress"
	invalidIngress.Spec.Ingress.Enabled = true
	invalidIngress.Spec.Ingress.Host = ""
	if err := k8sClient.Create(ctx, invalidIngress); err == nil {
		t.Fatal("expected ingress without a host to be rejected")
	}

	invalidStorage := user.DeepCopy()
	invalidStorage.ResourceVersion = ""
	invalidStorage.UID = ""
	invalidStorage.Name = "invalid-storage"
	invalidStorage.Spec.Workspace.Size = resource.MustParse("0")
	if err := k8sClient.Create(ctx, invalidStorage); err == nil {
		t.Fatal("expected zero storage size to be rejected")
	}

	invalidEnum := user.DeepCopy()
	invalidEnum.ResourceVersion = ""
	invalidEnum.UID = ""
	invalidEnum.Name = "invalid-enum"
	invalidEnum.Spec.Lark.Brand = "unknown"
	if err := k8sClient.Create(ctx, invalidEnum); err == nil {
		t.Fatal("expected an invalid brand to be rejected")
	}

	invalidWorkingDir := user.DeepCopy()
	invalidWorkingDir.ResourceVersion = ""
	invalidWorkingDir.UID = ""
	invalidWorkingDir.Name = "invalid-working-dir"
	invalidWorkingDir.Spec.Workspace.WorkingDir = "/opt/botmux"
	if err := k8sClient.Create(ctx, invalidWorkingDir); err == nil {
		t.Fatal("expected a reserved working directory to be rejected")
	}
}

func eventually(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}
