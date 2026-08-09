package controller

import (
	"context"
	"fmt"
	"os"
	"time"

	botmuxv1alpha1 "github.com/warjiang/botmux-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	finalizerName = "botmux.io/storage-protection"

	conditionCredentials = "CredentialsReady"
	conditionStorage     = "StorageReady"
	conditionWorkload    = "WorkloadReady"
	conditionReady       = "Ready"
)

// BotmuxUserReconciler reconciles a BotmuxUser object.
type BotmuxUserReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	OperatorImage string
}

// +kubebuilder:rbac:groups=botmux.io,resources=botmuxusers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=botmux.io,resources=botmuxusers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=botmux.io,resources=botmuxusers/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;delete

func (r *BotmuxUserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	user := &botmuxv1alpha1.BotmuxUser{}
	if err := r.Get(ctx, req.NamespacedName, user); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	statusBase := user.DeepCopy()
	user.Status.ObservedGeneration = user.Generation

	if !user.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, user)
	}
	if !controllerutil.ContainsFinalizer(user, finalizerName) {
		controllerutil.AddFinalizer(user, finalizerName)
		if err := r.Update(ctx, user); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Millisecond}, nil
	}

	storageReady, err := r.reconcilePVC(ctx, user)
	if err != nil {
		setCondition(user, conditionStorage, metav1.ConditionFalse, "StorageError", err.Error())
		setCondition(user, conditionReady, metav1.ConditionFalse, "StorageError", "storage is not ready")
		_ = r.patchStatus(ctx, user, statusBase)
		return ctrl.Result{}, err
	}
	if storageReady {
		setCondition(user, conditionStorage, metav1.ConditionTrue, "Bound", "persistent storage is bound")
	} else {
		setCondition(user, conditionStorage, metav1.ConditionFalse, "Provisioning", "waiting for persistent storage to bind")
	}

	secretVersions, err := r.validateSecrets(ctx, user)
	if err != nil {
		setCondition(user, conditionCredentials, metav1.ConditionFalse, "SecretUnavailable", err.Error())
		setCondition(user, conditionWorkload, metav1.ConditionFalse, "CredentialsUnavailable", "workload is suspended until credentials are available")
		setCondition(user, conditionReady, metav1.ConditionFalse, "CredentialsUnavailable", "credentials are not ready")
		user.Status.Phase = "Pending"
		if scaleErr := r.scaleWorkloadToZero(ctx, user); scaleErr != nil {
			setCondition(user, conditionWorkload, metav1.ConditionFalse, "ScaleDownFailed", scaleErr.Error())
			setCondition(user, conditionReady, metav1.ConditionFalse, "ScaleDownFailed", "failed to stop workload")
			if patchErr := r.patchStatus(ctx, user, statusBase); patchErr != nil {
				return ctrl.Result{}, patchErr
			}
			return ctrl.Result{}, scaleErr
		}
		if patchErr := r.patchStatus(ctx, user, statusBase); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	setCondition(user, conditionCredentials, metav1.ConditionTrue, "Available", "all referenced secrets are available")

	runtimeImage, found := resolveRuntimeImage(user)
	if !found {
		message := fmt.Sprintf("cliId %q has no catalog image; set spec.runtime.image", user.Spec.Runtime.CLIID)
		setCondition(user, conditionWorkload, metav1.ConditionFalse, "RuntimeImageUnknown", message)
		setCondition(user, conditionReady, metav1.ConditionFalse, "RuntimeImageUnknown", message)
		user.Status.Phase = "Pending"
		if scaleErr := r.scaleWorkloadToZero(ctx, user); scaleErr != nil {
			setCondition(user, conditionWorkload, metav1.ConditionFalse, "ScaleDownFailed", scaleErr.Error())
			setCondition(user, conditionReady, metav1.ConditionFalse, "ScaleDownFailed", "failed to stop workload")
			if patchErr := r.patchStatus(ctx, user, statusBase); patchErr != nil {
				return ctrl.Result{}, patchErr
			}
			return ctrl.Result{}, scaleErr
		}
		if patchErr := r.patchStatus(ctx, user, statusBase); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{}, nil
	}

	if err := r.reconcileService(ctx, user); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reconcileIngress(ctx, user); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reconcileStatefulSet(ctx, user, runtimeImage, credentialsRevision(secretVersions)); err != nil {
		return ctrl.Result{}, err
	}

	r.observeWorkload(ctx, user)
	user.Status.ServiceName = resourceBaseName(user)
	user.Status.PodName = resourceBaseName(user) + "-0"
	if user.Spec.Ingress.Enabled {
		user.Status.DashboardURL = dashboardURL(user)
	} else {
		user.Status.DashboardURL = fmt.Sprintf("http://%s.%s.svc.cluster.local", resourceBaseName(user), user.Namespace)
	}
	if err := r.patchStatus(ctx, user, statusBase); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *BotmuxUserReconciler) reconcilePVC(ctx context.Context, user *botmuxv1alpha1.BotmuxUser) (bool, error) {
	desired := desiredPVC(user)
	current := &corev1.PersistentVolumeClaim{}
	key := client.ObjectKeyFromObject(desired)
	err := r.Get(ctx, key, current)
	if apierrors.IsNotFound(err) {
		return false, r.Create(ctx, desired)
	}
	if err != nil {
		return false, err
	}
	requested := desired.Spec.Resources.Requests[corev1.ResourceStorage]
	currentSize := current.Spec.Resources.Requests[corev1.ResourceStorage]
	if requested.Cmp(currentSize) < 0 {
		return false, fmt.Errorf("pvc shrink is not supported: requested %s, current %s", requested.String(), currentSize.String())
	}
	if requested.Cmp(currentSize) > 0 {
		base := current.DeepCopy()
		current.Spec.Resources.Requests[corev1.ResourceStorage] = requested
		if err := r.Patch(ctx, current, client.MergeFrom(base)); err != nil {
			return false, err
		}
	}
	return current.Status.Phase == corev1.ClaimBound, nil
}

func (r *BotmuxUserReconciler) validateSecrets(ctx context.Context, user *botmuxv1alpha1.BotmuxUser) (map[string]string, error) {
	versions := map[string]string{}
	larkSecret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: user.Namespace, Name: user.Spec.Lark.CredentialsSecretRef.Name}
	if err := r.Get(ctx, key, larkSecret); err != nil {
		return nil, fmt.Errorf("get Lark credentials Secret %s: %w", key.Name, err)
	}
	if len(larkSecret.Data["appSecret"]) == 0 {
		return nil, fmt.Errorf("lark credentials Secret %s must contain non-empty appSecret", key.Name)
	}
	versions[key.Name] = larkSecret.ResourceVersion
	for _, ref := range user.Spec.Runtime.EnvFromSecretRefs {
		secret := &corev1.Secret{}
		key.Name = ref.Name
		if err := r.Get(ctx, key, secret); err != nil {
			return nil, fmt.Errorf("get runtime Secret %s: %w", key.Name, err)
		}
		versions[key.Name] = secret.ResourceVersion
	}
	return versions, nil
}

func (r *BotmuxUserReconciler) reconcileService(ctx context.Context, user *botmuxv1alpha1.BotmuxUser) error {
	desired := desiredService(user)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		if err := controllerutil.SetControllerReference(user, desired, r.Scheme); err != nil {
			return err
		}
		desired.Labels = labelsFor(user)
		desired.Spec.Selector = labelsFor(user)
		desired.Spec.Type = corev1.ServiceTypeClusterIP
		desired.Spec.Ports = desiredService(user).Spec.Ports
		return nil
	})
	return err
}

func (r *BotmuxUserReconciler) reconcileIngress(ctx context.Context, user *botmuxv1alpha1.BotmuxUser) error {
	key := types.NamespacedName{Namespace: user.Namespace, Name: resourceBaseName(user)}
	if !user.Spec.Ingress.Enabled {
		current := &networkingv1.Ingress{}
		if err := r.Get(ctx, key, current); err == nil {
			return r.Delete(ctx, current)
		} else if !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}
	desired := desiredIngress(user)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		if err := controllerutil.SetControllerReference(user, desired, r.Scheme); err != nil {
			return err
		}
		target := desiredIngress(user)
		desired.Labels = target.Labels
		desired.Annotations = target.Annotations
		desired.Spec = target.Spec
		return nil
	})
	return err
}

func (r *BotmuxUserReconciler) reconcileStatefulSet(
	ctx context.Context,
	user *botmuxv1alpha1.BotmuxUser,
	runtimeImage, revision string,
) error {
	operatorImage := r.OperatorImage
	if operatorImage == "" {
		operatorImage = envOr("BOTMUX_OPERATOR_IMAGE", "ghcr.io/warjiang/botmux-operator:v0.1.0")
	}
	desired := desiredStatefulSet(user, runtimeImage, operatorImage, revision)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		if err := controllerutil.SetControllerReference(user, desired, r.Scheme); err != nil {
			return err
		}
		target := desiredStatefulSet(user, runtimeImage, operatorImage, revision)
		desired.Labels = target.Labels
		desired.Spec = target.Spec
		return nil
	})
	return err
}

func (r *BotmuxUserReconciler) scaleWorkloadToZero(ctx context.Context, user *botmuxv1alpha1.BotmuxUser) error {
	sts := &appsv1.StatefulSet{}
	key := types.NamespacedName{Namespace: user.Namespace, Name: resourceBaseName(user)}
	if err := r.Get(ctx, key, sts); err != nil {
		return client.IgnoreNotFound(err)
	}
	if sts.Spec.Replicas != nil && *sts.Spec.Replicas == 0 {
		return nil
	}
	base := sts.DeepCopy()
	zero := int32(0)
	sts.Spec.Replicas = &zero
	return r.Patch(ctx, sts, client.MergeFrom(base))
}

func (r *BotmuxUserReconciler) observeWorkload(ctx context.Context, user *botmuxv1alpha1.BotmuxUser) {
	if user.Spec.Suspend {
		user.Status.Phase = "Suspended"
		setCondition(user, conditionWorkload, metav1.ConditionFalse, "Suspended", "workload is suspended")
		setCondition(user, conditionReady, metav1.ConditionFalse, "Suspended", "workload is suspended")
		return
	}
	sts := &appsv1.StatefulSet{}
	key := types.NamespacedName{Namespace: user.Namespace, Name: resourceBaseName(user)}
	if err := r.Get(ctx, key, sts); err != nil {
		user.Status.Phase = "Pending"
		setCondition(user, conditionWorkload, metav1.ConditionFalse, "StatefulSetUnavailable", err.Error())
		setCondition(user, conditionReady, metav1.ConditionFalse, "StatefulSetUnavailable", "workload is not ready")
		return
	}
	pod := &corev1.Pod{}
	podKey := types.NamespacedName{Namespace: user.Namespace, Name: resourceBaseName(user) + "-0"}
	if err := r.Get(ctx, podKey, pod); err == nil {
		for _, initStatus := range pod.Status.InitContainerStatuses {
			if initStatus.Name == "sandbox-probe" && initStatus.State.Terminated != nil && initStatus.State.Terminated.ExitCode != 0 {
				user.Status.Phase = "Error"
				setCondition(user, conditionWorkload, metav1.ConditionFalse, "SandboxUnsupported", "bubblewrap sandbox probe failed on the selected node")
				setCondition(user, conditionReady, metav1.ConditionFalse, "SandboxUnsupported", "sandbox is not supported")
				return
			}
		}
	}
	if sts.Status.ReadyReplicas == 1 && sts.Status.CurrentReplicas == 1 {
		user.Status.Phase = "Running"
		setCondition(user, conditionWorkload, metav1.ConditionTrue, "Available", "daemon and dashboard are ready")
		setCondition(user, conditionReady, metav1.ConditionTrue, "Available", "BotmuxUser is ready")
		return
	}
	user.Status.Phase = "Starting"
	setCondition(user, conditionWorkload, metav1.ConditionFalse, "Starting", "waiting for the StatefulSet to become ready")
	setCondition(user, conditionReady, metav1.ConditionFalse, "Starting", "workload is starting")
}

func (r *BotmuxUserReconciler) reconcileDelete(ctx context.Context, user *botmuxv1alpha1.BotmuxUser) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(user, finalizerName) {
		return ctrl.Result{}, nil
	}
	sts := &appsv1.StatefulSet{}
	key := types.NamespacedName{Namespace: user.Namespace, Name: resourceBaseName(user)}
	if err := r.Get(ctx, key, sts); err == nil {
		if err := r.Delete(ctx, sts); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	} else if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	pod := &corev1.Pod{}
	podKey := types.NamespacedName{Namespace: user.Namespace, Name: resourceBaseName(user) + "-0"}
	if err := r.Get(ctx, podKey, pod); err == nil {
		if pod.DeletionTimestamp.IsZero() {
			if err := r.Delete(ctx, pod); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	} else if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	if effectiveReclaimPolicy(user) == botmuxv1alpha1.ReclaimPolicyDelete {
		pvc := &corev1.PersistentVolumeClaim{}
		if err := r.Get(ctx, key, pvc); err == nil {
			if err := r.Delete(ctx, pvc); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		} else if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}
	controllerutil.RemoveFinalizer(user, finalizerName)
	return ctrl.Result{}, r.Update(ctx, user)
}

func (r *BotmuxUserReconciler) patchStatus(ctx context.Context, user, base *botmuxv1alpha1.BotmuxUser) error {
	return r.Status().Patch(ctx, user, client.MergeFrom(base))
}

func setCondition(user *botmuxv1alpha1.BotmuxUser, conditionType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&user.Status.Conditions, metav1.Condition{
		Type: conditionType, Status: status, Reason: reason, Message: message,
		ObservedGeneration: user.Generation,
	})
}

func (r *BotmuxUserReconciler) requestsForSecret(ctx context.Context, object client.Object) []reconcile.Request {
	secret, ok := object.(*corev1.Secret)
	if !ok {
		return nil
	}
	users := &botmuxv1alpha1.BotmuxUserList{}
	if err := r.List(ctx, users, client.InNamespace(secret.Namespace)); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0)
	for i := range users.Items {
		user := &users.Items[i]
		if user.Spec.Lark.CredentialsSecretRef.Name == secret.Name || runtimeReferencesSecret(user, secret.Name) {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(user)})
		}
	}
	return requests
}

func runtimeReferencesSecret(user *botmuxv1alpha1.BotmuxUser, name string) bool {
	for _, ref := range user.Spec.Runtime.EnvFromSecretRefs {
		if ref.Name == name {
			return true
		}
	}
	return false
}

func (r *BotmuxUserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	secretEvents := predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return true },
		UpdateFunc:  func(event.UpdateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&botmuxv1alpha1.BotmuxUser{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&networkingv1.Ingress{}).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForSecret),
			builder.WithPredicates(secretEvents),
		).
		Complete(r)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
