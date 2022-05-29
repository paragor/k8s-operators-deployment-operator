package controllers

import (
	"context"
	"fmt"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	vpav1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strings"
)

import "sigs.k8s.io/controller-runtime/pkg/client"

type GeneralReconciler struct {
	client         client.Client
	scheme         *runtime.Scheme
	createEmptyObj func() client.Object
	gkv            schema.GroupVersionKind
	nsSelector     string
}

func NewGeneralReconciler(
	client client.Client,
	scheme *runtime.Scheme,
	nsSelector string,
	createEmptyObj func() client.Object,
) (*GeneralReconciler, error) {
	gkv, err := apiutil.GVKForObject(createEmptyObj(), scheme)
	if err != nil {
		return nil, err
	}
	return &GeneralReconciler{
		client:         client,
		scheme:         scheme,
		createEmptyObj: createEmptyObj,
		gkv:            gkv,
		nsSelector:     nsSelector,
	}, nil
}

//+kubebuilder:rbac:groups=apps,resources=daemonsets;deployments;statefulsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=autoscaling.k8s.io,resources=verticalpodautoscalers,verbs=get;list;watch;create;update;patch;delete

func (r *GeneralReconciler) Reconcile(ctx context.Context, req ctrl.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("got request")
	obj := r.createEmptyObj()

	//todo фильтрация ns по r.nsSelector

	err := r.client.Get(ctx, req.NamespacedName, obj)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(4).Info("delete vpa")
			if err := r.client.Delete(
				ctx,
				&vpav1.VerticalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{
					Name:      r.generateVpaNameFromObjName(req.Name),
					Namespace: req.Namespace,
				}},
			); err != nil {
				return ctrl.Result{}, fmt.Errorf("cant delete vpa: %w", err)
			}
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("cant get resouce: %w", err)
	}
	foundVpa := vpav1.VerticalPodAutoscaler{}
	err = r.client.Get(
		ctx,
		types.NamespacedName{Name: r.generateVpaNameFromObjName(obj.GetName()), Namespace: obj.GetNamespace()},
		&foundVpa,
	)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(4).Info("create vpa")
			// Define and create a new deployment.
			creatingVpa, err := r.vpaForObject(obj)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("cant generate vpa for creating: %w", err)
			}
			if err = r.client.Create(ctx, creatingVpa); err != nil {
				if apierrors.IsAlreadyExists(err) {
					return ctrl.Result{Requeue: true}, nil
				}
				return ctrl.Result{}, fmt.Errorf("cant create vpa: %w", err)
			}
			// success created
			return ctrl.Result{}, nil
		}
		// !isNotFound
		return ctrl.Result{}, fmt.Errorf("cant found vpa :%w", err)
	}

	generatedVpa, err := r.vpaForObject(obj)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("cant generate vpa for comparing: %w", err)
	}

	if !reflect.DeepEqual(foundVpa.Spec, generatedVpa.Spec) {
		foundVpa.Labels = generatedVpa.Labels
		foundVpa.Spec = *generatedVpa.Spec.DeepCopy()
		foundVpa.Namespace = generatedVpa.Namespace
		foundVpa.OwnerReferences = generatedVpa.OwnerReferences
		if err := r.client.Update(ctx, &foundVpa); err != nil {
			return ctrl.Result{}, fmt.Errorf("cant update vpa: %w", err)
		}
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

// deploymentForMemcached returns a Deployment object for data from m.
func (r *GeneralReconciler) vpaForObject(obj client.Object) (*vpav1.VerticalPodAutoscaler, error) {
	lbls := map[string]string{"paragor.ru/owned": "deployment-operator"}
	updateMode := vpav1.UpdateModeInitial
	controlledValuesRequestsOnly := vpav1.ContainerControlledValuesRequestsOnly
	vpa := &vpav1.VerticalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.generateVpaNameFromObjName(obj.GetName()),
			Namespace: obj.GetNamespace(),
			Labels:    lbls,
		},
		Spec: vpav1.VerticalPodAutoscalerSpec{
			TargetRef: &autoscalingv1.CrossVersionObjectReference{
				Kind:       obj.GetObjectKind().GroupVersionKind().Kind,
				APIVersion: obj.GetObjectKind().GroupVersionKind().Version,
				Name:       obj.GetName(),
			},
			UpdatePolicy: &vpav1.PodUpdatePolicy{
				UpdateMode: &updateMode,
			},
			ResourcePolicy: &vpav1.PodResourcePolicy{ContainerPolicies: []vpav1.ContainerResourcePolicy{{
				ContainerName: "*",
				ControlledResources: &[]corev1.ResourceName{
					corev1.ResourceCPU,
					corev1.ResourceMemory,
				},
				ControlledValues: &controlledValuesRequestsOnly,
			}}},
		},
		Status: vpav1.VerticalPodAutoscalerStatus{},
	}

	err := controllerutil.SetControllerReference(obj, vpa, r.scheme)
	return vpa, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *GeneralReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(r.createEmptyObj()).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Owns(&vpav1.VerticalPodAutoscaler{}).
		Complete(r)
}

func (r *GeneralReconciler) generateVpaNameFromObjName(name string) string {
	return name + "-" + strings.ToLower(r.gkv.Kind)
}
