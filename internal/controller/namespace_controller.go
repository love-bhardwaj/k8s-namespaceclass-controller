/*
Copyright 2026.

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

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/love-bhardwaj/k8s-namespaceclass-controller/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// classLabel is the namespace label that names the
	// namespaceclass resource to be used.
	classLabel = "namespaceclass.lovebhardwaj.io/name"
	// fieldOwner for the resources we manage.
	fieldOwner = "namespaceclass-controller"
	// managedByLabel to signal that the resource is managed
	// by a namespaceclass.
	managedByLabel = "namespaceclass.lovebhardwaj.io/managed-by"

	typeTemplateValid     = "TemplatesValid"
	reasonInvalidTemplate = "InvalidTemplate"
	reasonValidTemplate   = "Valid"
)

// NamespaceReconciler creates the managed objects listed by
// custom resources that a namespace points to via labels.
type NamespaceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete;deletecollection
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete;deletecollection
// +kubebuilder:rbac:groups="namespaceclass.lovebhardwaj.io",resources=namespaceclasses,verbs=get;list;watch;
// +kubebuilder:rbac:groups="namespaceclass.lovebhardwaj.io",resources=namespaceclasses/status,verbs=get;update;patch

// Reconcile makes that manages objects in the namespace named by req,
// that are managed by the controller.
func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	namespaceObj := &corev1.Namespace{}
	if err := r.Get(ctx, req.NamespacedName, namespaceObj); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get namespace: %w", err)
	}

	if !namespaceObj.DeletionTimestamp.IsZero() {
		log.Info("Skipped Namespace being deleted")
		return ctrl.Result{}, nil
	}

	namespaceClassName := namespaceObj.Labels[classLabel]
	if namespaceClassName == "" {
		// Prune any resources that were managed by the namespace class
		// in this namespace.
		return ctrl.Result{}, r.prune(ctx, namespaceObj.Name, nil)
	}

	// Has the namespaceclassname label, fetch the object
	namespaceClassObj := &v1alpha1.NamespaceClass{}
	if err := r.Get(ctx, client.ObjectKey{Name: namespaceClassName}, namespaceClassObj); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get namespaceclass %s: %w", namespaceClassName, err)
	}

	// Being deleted so leave it as it is.
	if !namespaceClassObj.DeletionTimestamp.IsZero() {
		log.Info("Skipped NamespaceClass being deleted", "namespaceclass", namespaceClassName)
		return ctrl.Result{}, nil
	}

	resourceObjects, errMsgs := renderTemplates(r.Scheme, namespaceClassObj, namespaceObj.Name)
	if len(errMsgs) > 0 {
		if err := r.setCondition(ctx, namespaceClassObj, metav1.Condition{
			Type:               typeTemplateValid,
			Status:             metav1.ConditionFalse,
			Reason:             reasonInvalidTemplate,
			Message:            strings.Join(errMsgs, ", "),
			ObservedGeneration: namespaceClassObj.Generation,
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("set template valid after render failure: %w", err)
		}
		return ctrl.Result{}, reconcile.TerminalError(
			fmt.Errorf(
				"resource %s has invalid template: %s",
				namespaceClassObj.Name,
				strings.Join(errMsgs, ", "),
			),
		)
	}

	invalidObjects, applyErr := r.applyAll(ctx, resourceObjects)
	if applyErr != nil {
		return ctrl.Result{}, applyErr
	}
	if len(invalidObjects) > 0 {
		if err := r.setCondition(ctx, namespaceClassObj, metav1.Condition{
			Type:               typeTemplateValid,
			Status:             metav1.ConditionFalse,
			Reason:             reasonInvalidTemplate,
			Message:            strings.Join(invalidObjects, ", "),
			ObservedGeneration: namespaceClassObj.Generation,
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("set template valid after rejected objects: %w", err)
		}
	}
	if len(invalidObjects) == 0 {
		if err := r.setCondition(ctx, namespaceClassObj, metav1.Condition{
			Type:               typeTemplateValid,
			Status:             metav1.ConditionTrue,
			Reason:             reasonValidTemplate,
			Message:            "All templates are valid",
			ObservedGeneration: namespaceClassObj.Generation,
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("set template valid after apply: %w", err)
		}
	}

	return ctrl.Result{}, r.prune(ctx, namespaceObj.Name, resourceObjects)
}

func (r *NamespaceReconciler) setCondition(
	ctx context.Context,
	namespaceClass *v1alpha1.NamespaceClass,
	cond metav1.Condition,
) error {
	if !meta.SetStatusCondition(&namespaceClass.Status.Conditions, cond) {
		return nil
	}
	if err := r.Status().Update(ctx, namespaceClass); err != nil {
		return fmt.Errorf("status update: %w", err)
	}
	return nil
}

// applyAll applies the resource objects one by one collecting
// error if there are any from applying the object. Returns
// the a list of objects that failed apply due to invalid template
// or error if there was any error during apply.
func (r *NamespaceReconciler) applyAll(ctx context.Context, objs []*unstructured.Unstructured) ([]string, error) {
	var errs []error
	var invalidObjects []string

	for i, obj := range objs {
		if err := r.Apply(
			ctx,
			client.ApplyConfigurationFromUnstructured(obj),
			client.FieldOwner(fieldOwner),
			client.ForceOwnership,
		); err != nil {
			if apierrors.IsInvalid(err) {
				invalidObjects = append(invalidObjects, fmt.Sprintf("resources[%d]: %v", i, err))
				continue
			}
			errs = append(errs, fmt.Errorf("apply %s: %w", obj.GetName(), err))
		}
	}
	return invalidObjects, errors.Join(errs...)
}

// renderTemplates renders the resource templates from the namespaceclass resource.
// Returns the raw rendered objects and error messages for any resources that
// failed rendering.
func renderTemplates(
	scheme *runtime.Scheme,
	namespaceClass *v1alpha1.NamespaceClass,
	namespace string,
) ([]*unstructured.Unstructured, []string) {
	var objs []*unstructured.Unstructured
	var errMsgs []string

	for i, res := range namespaceClass.Spec.Resources {
		obj, err := render(scheme, namespaceClass.Name, namespace, res.Template.Raw)
		if err != nil {
			errMsgs = append(errMsgs, fmt.Sprintf("resources[%d]: %v", i, err))
			continue
		}
		objs = append(objs, obj)
	}
	return objs, errMsgs
}

func render(
	scheme *runtime.Scheme,
	namespaceClassLabel string,
	namespace string,
	raw []byte,
) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}
	if err := json.Unmarshal(raw, &obj.Object); err != nil {
		return nil, fmt.Errorf("template is not a valid JSON: %w", err)
	}

	if obj.GetNamespace() != "" {
		return nil, fmt.Errorf("template must not set namespace")
	}

	typed, err := scheme.New(obj.GroupVersionKind())
	if err != nil {
		return nil, fmt.Errorf("kind %s is not supported: %w", obj.GroupVersionKind(), err)
	}

	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, typed); err != nil {
		return nil, fmt.Errorf("template has a wrong field type: %w", err)
	}

	obj.SetNamespace(namespace)
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[managedByLabel] = namespaceClassLabel
	obj.SetLabels(labels)
	return obj, nil
}

func (r *NamespaceReconciler) prune(ctx context.Context, namespace string, keep []*unstructured.Unstructured) error {
	var errs []error
	for _, kind := range managedKinds {
		var notKeptSelectors []fields.Selector
		for _, obj := range keep {
			if obj.GroupVersionKind() == kind.gvk {
				notKeptSelectors = append(notKeptSelectors, fields.OneTermNotEqualSelector("metadata.name", obj.GetName()))
			}
		}

		if err := r.DeleteAllOf(
			ctx,
			kind.obj,
			client.InNamespace(namespace),
			client.HasLabels{managedByLabel},
			client.MatchingFieldsSelector{Selector: fields.AndSelectors(notKeptSelectors...)},
		); err != nil {
			errs = append(errs, fmt.Errorf("prune managed %s: %w", kind.gvk.Kind, err))
		}
	}
	return errors.Join(errs...)
}

// namespaces returns a request for all namespaces that refer to
// the namespaceclass custom resource.
func (r *NamespaceReconciler) namespaces(ctx context.Context, obj client.Object) []reconcile.Request {
	list := &corev1.NamespaceList{}
	if err := r.List(ctx, list, client.MatchingLabels{classLabel: obj.GetName()}); err != nil {
		logf.FromContext(ctx).Error(err, "Could not list namespaces for namespaceclass", "namespaceclass", obj.GetName())
		return nil
	}

	reqs := make([]reconcile.Request, 0, len(list.Items))
	for _, ns := range list.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Name: ns.Name}})
	}
	return reqs
}

// namespaceOf return the namespace of the resource managed
// by the controller.
func namespaceOf(_ context.Context, obj client.Object) []reconcile.Request {
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: obj.GetNamespace()}}}
}

// SetupWithManager registers the reconciler with mgr.
// It watches namespaces for namespaceclass custom resources.
func (r *NamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := checkManagedKinds(r.Scheme); err != nil {
		return err
	}
	// Only react to resources that have the manged by label.
	managed := predicate.NewPredicateFuncs(func(object client.Object) bool {
		_, ok := object.GetLabels()[managedByLabel]
		return ok
	})
	// Watch for namespace that refer to a namespaceclass resource.
	// Watch for resource that are managed by the namespaceclass controller
	b := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Watches(
			&v1alpha1.NamespaceClass{},
			handler.EnqueueRequestsFromMapFunc(r.namespaces),
		)
	for _, kind := range managedKinds {
		b = b.Watches(
			kind.obj,
			handler.EnqueueRequestsFromMapFunc(namespaceOf),
			builder.WithPredicates(managed),
		)
	}
	return b.Named("namespaceclass-namespace").Complete(r)
}
