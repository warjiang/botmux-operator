package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	botmuxv1alpha1 "github.com/warjiang/botmux-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileCreatesUserResources(t *testing.T) {
	ctx := context.Background()
	user := testUser()
	larkSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "alice-lark", Namespace: "default"},
		Data:       map[string][]byte{"appSecret": []byte("secret")},
	}
	providerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "alice-provider", Namespace: "default"},
		Data:       map[string][]byte{"OPENAI_API_KEY": []byte("key")},
	}
	r, c := testReconciler(t, user, larkSecret, providerSecret)
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(user)}

	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}

	name := resourceBaseName(user)
	for _, object := range []client.Object{
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}},
	} {
		if err := c.Get(ctx, client.ObjectKeyFromObject(object), object); err != nil {
			t.Fatalf("get %T: %v", object, err)
		}
	}
	stored := &botmuxv1alpha1.BotmuxUser{}
	if err := c.Get(ctx, request.NamespacedName, stored); err != nil {
		t.Fatal(err)
	}
	if !containsString(stored.Finalizers, finalizerName) {
		t.Fatalf("finalizer not installed: %#v", stored.Finalizers)
	}
}

func TestReconcileMissingSecretCreatesStorageButNoWorkload(t *testing.T) {
	ctx := context.Background()
	user := testUser()
	r, c := testReconciler(t, user)
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(user)}
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}

	pvc := &corev1.PersistentVolumeClaim{}
	key := client.ObjectKey{Namespace: "default", Name: resourceBaseName(user)}
	if err := c.Get(ctx, key, pvc); err != nil {
		t.Fatalf("PVC should be created before credentials are ready: %v", err)
	}
	sts := &appsv1.StatefulSet{}
	if err := c.Get(ctx, key, sts); err == nil {
		t.Fatal("StatefulSet should not exist without credentials")
	}
	stored := &botmuxv1alpha1.BotmuxUser{}
	if err := c.Get(ctx, request.NamespacedName, stored); err != nil {
		t.Fatal(err)
	}
	condition := meta.FindStatusCondition(stored.Status.Conditions, conditionCredentials)
	if condition == nil || condition.Status != metav1.ConditionFalse {
		t.Fatalf("CredentialsReady condition = %#v", condition)
	}
}

func TestReconcileReturnsScaleDownError(t *testing.T) {
	ctx := context.Background()
	user := testUser()
	user.Finalizers = []string{finalizerName}
	name := resourceBaseName(user)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: user.Namespace},
		Spec:       appsv1.StatefulSetSpec{Replicas: int32Ptr(1)},
	}
	r, baseClient := testReconciler(t, user, sts)
	r.Client = &failingStatefulSetPatchClient{Client: baseClient}

	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(user)})
	if err == nil || err.Error() != "forced StatefulSet patch failure" {
		t.Fatalf("Reconcile error = %v, want scale-down failure", err)
	}
}

func TestReconcileIngressToggle(t *testing.T) {
	ctx := context.Background()
	user := testUser()
	user.Spec.Ingress.Enabled = true
	user.Spec.Ingress.Host = "alice.example.com"
	larkSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "alice-lark", Namespace: "default"},
		Data:       map[string][]byte{"appSecret": []byte("secret")},
	}
	providerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "alice-provider", Namespace: "default"},
		Data:       map[string][]byte{"OPENAI_API_KEY": []byte("key")},
	}
	r, c := testReconciler(t, user, larkSecret, providerSecret)
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(user)}
	_, _ = r.Reconcile(ctx, request)
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
	key := client.ObjectKey{Namespace: "default", Name: resourceBaseName(user)}
	if err := c.Get(ctx, key, &networkingv1.Ingress{}); err != nil {
		t.Fatalf("Ingress not created: %v", err)
	}
}

func TestReconcileSecretRotationAndSuspendResume(t *testing.T) {
	ctx := context.Background()
	user := testUser()
	larkSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "alice-lark", Namespace: "default"},
		Data:       map[string][]byte{"appSecret": []byte("secret")},
	}
	providerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "alice-provider", Namespace: "default"},
		Data:       map[string][]byte{"OPENAI_API_KEY": []byte("key")},
	}
	r, c := testReconciler(t, user, larkSecret, providerSecret)
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(user)}
	reconcileTwice(t, ctx, r, request)

	key := client.ObjectKey{Namespace: user.Namespace, Name: resourceBaseName(user)}
	sts := &appsv1.StatefulSet{}
	if err := c.Get(ctx, key, sts); err != nil {
		t.Fatal(err)
	}
	oldRevision := sts.Spec.Template.Annotations["botmux.io/credentials-revision"]

	rotated := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(providerSecret), rotated); err != nil {
		t.Fatal(err)
	}
	rotated.Data["OPENAI_API_KEY"] = []byte("rotated")
	if err := c.Update(ctx, rotated); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, key, sts); err != nil {
		t.Fatal(err)
	}
	if revision := sts.Spec.Template.Annotations["botmux.io/credentials-revision"]; revision == oldRevision {
		t.Fatal("Secret rotation did not update the Pod template revision")
	}

	stored := &botmuxv1alpha1.BotmuxUser{}
	if err := c.Get(ctx, request.NamespacedName, stored); err != nil {
		t.Fatal(err)
	}
	stored.Spec.Suspend = true
	if err := c.Update(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, key, sts); err != nil {
		t.Fatal(err)
	}
	if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 0 {
		t.Fatalf("suspended replicas = %v, want 0", sts.Spec.Replicas)
	}

	if err := c.Get(ctx, request.NamespacedName, stored); err != nil {
		t.Fatal(err)
	}
	stored.Spec.Suspend = false
	if err := c.Update(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, key, sts); err != nil {
		t.Fatal(err)
	}
	if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 1 {
		t.Fatalf("resumed replicas = %v, want 1", sts.Spec.Replicas)
	}
}

func TestReconcileDeleteWaitsForPodAndRetainsPVC(t *testing.T) {
	ctx := context.Background()
	user := deletingTestUser()
	user.Spec.Workspace.ReclaimPolicy = botmuxv1alpha1.ReclaimPolicyRetain
	name := resourceBaseName(user)
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: user.Namespace}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name + "-0", Namespace: user.Namespace}}
	r, c := testReconciler(t, user, pvc, pod)

	if _, err := r.reconcileDelete(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(pod), &corev1.Pod{}); !apierrors.IsNotFound(err) {
		t.Fatalf("Pod should be deleted before the finalizer is removed: %v", err)
	}
	if !containsString(user.Finalizers, finalizerName) {
		t.Fatal("finalizer was removed before the Pod exited")
	}

	if _, err := r.reconcileDelete(ctx, user); err != nil {
		t.Fatal(err)
	}
	if containsString(user.Finalizers, finalizerName) {
		t.Fatal("finalizer should be removed after the Pod exits")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(pvc), &corev1.PersistentVolumeClaim{}); err != nil {
		t.Fatalf("Retain policy should keep the PVC: %v", err)
	}
}

func TestReconcileDeleteRemovesPVCForDeletePolicy(t *testing.T) {
	ctx := context.Background()
	user := deletingTestUser()
	user.Spec.Workspace.ReclaimPolicy = botmuxv1alpha1.ReclaimPolicyDelete
	name := resourceBaseName(user)
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: user.Namespace}}
	r, c := testReconciler(t, user, pvc)

	if _, err := r.reconcileDelete(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(pvc), &corev1.PersistentVolumeClaim{}); !apierrors.IsNotFound(err) {
		t.Fatalf("Delete policy should remove the PVC: %v", err)
	}
	if !containsString(user.Finalizers, finalizerName) {
		t.Fatal("finalizer was removed before PVC deletion was observed")
	}

	if _, err := r.reconcileDelete(ctx, user); err != nil {
		t.Fatal(err)
	}
	if containsString(user.Finalizers, finalizerName) {
		t.Fatal("finalizer should be removed after PVC deletion")
	}
}

func deletingTestUser() *botmuxv1alpha1.BotmuxUser {
	user := testUser()
	now := metav1.NewTime(time.Now())
	user.DeletionTimestamp = &now
	user.Finalizers = []string{finalizerName}
	return user
}

func testReconciler(t *testing.T, objects ...client.Object) (*BotmuxUserReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := botmuxv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&botmuxv1alpha1.BotmuxUser{}, &appsv1.StatefulSet{}).
		WithObjects(objects...).
		Build()
	return &BotmuxUserReconciler{Client: c, Scheme: scheme, OperatorImage: "operator:test"}, c
}

func reconcileTwice(t *testing.T, ctx context.Context, r *BotmuxUserReconciler, request ctrl.Request) {
	t.Helper()
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
}

type failingStatefulSetPatchClient struct {
	client.Client
}

func (c *failingStatefulSetPatchClient) Patch(
	ctx context.Context,
	object client.Object,
	patch client.Patch,
	options ...client.PatchOption,
) error {
	if _, ok := object.(*appsv1.StatefulSet); ok {
		return errors.New("forced StatefulSet patch failure")
	}
	return c.Client.Patch(ctx, object, patch, options...)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
