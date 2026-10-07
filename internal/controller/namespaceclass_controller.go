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
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/love-bhardwaj/k8s-namespaceclass-controller/api/v1alpha1"
)

const namespaceClassFinalizer = "namespaceclass.lovebhardwaj.io/cleanup"

// NamespaceClassReconciler reconciles a NamespaceClass object. It is used
// to perform actions when a NamespaceClass resource is deleted.
type NamespaceClassReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=namespaceclass.lovebhardwaj.io,resources=namespaceclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=namespaceclass.lovebhardwaj.io,resources=namespaceclasses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=namespaceclass.lovebhardwaj.io,resources=namespaceclasses/finalizers,verbs=update

// Reconcile adds the finalizer to the namespaceclass resources.
func (r *NamespaceClassReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	namespaceClassObj := &v1alpha1.NamespaceClass{}
	if err := r.Get(ctx, req.NamespacedName, namespaceClassObj); err != nil {
		log.Error(err, "Get namespaceclass resource")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if namespaceClassObj.DeletionTimestamp.IsZero() {
		if controllerutil.AddFinalizer(namespaceClassObj, namespaceClassFinalizer) {
			if err := r.Update(ctx, namespaceClassObj); err != nil {
				log.Error(err, "Add namespaceclass resource finalizer")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(namespaceClassObj, namespaceClassFinalizer) {
		return ctrl.Result{}, nil
	}

	if err := r.cleanup(ctx, namespaceClassObj); err != nil {
		log.Error(err, "Cleanup namespaceclass managed resources")
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(namespaceClassObj, namespaceClassFinalizer)
	if err := r.Update(ctx, namespaceClassObj); err != nil {
		log.Error(err, "Update namespaceclass object remove finalizer")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *NamespaceClassReconciler) cleanup(
	ctx context.Context,
	namespaceClass *v1alpha1.NamespaceClass,
) error {
	var errs []error
	for _, kind := range managedKinds {
		objs, err := r.listManaged(ctx, kind, namespaceClass.Name)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		for _, obj := range objs {
			id := fmt.Sprintf("%T %s/%s", obj, obj.GetNamespace(), obj.GetName())
			if namespaceClass.Spec.PruningOnDelete {
				if err := r.Delete(ctx, obj); client.IgnoreNotFound(err) != nil {
					errs = append(errs, fmt.Errorf("delete %s: %w", id, err))
				}
				continue
			}
			patch := client.MergeFrom(obj.DeepCopyObject().(client.Object))
			labels := obj.GetLabels()
			delete(labels, managedByLabel)
			obj.SetLabels(labels)
			if err := r.Patch(ctx, obj, patch); client.IgnoreNotFound(err) != nil {
				errs = append(errs, fmt.Errorf("release %s: %w", id, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (r *NamespaceClassReconciler) listManaged(
	ctx context.Context,
	kind managedKind,
	name string,
) ([]client.Object, error) {
	list := kind.list.DeepCopyObject().(client.ObjectList)
	if err := r.List(ctx, list, client.MatchingLabels{managedByLabel: name}); err != nil {
		return nil, fmt.Errorf("list managed %s: %w", kind.gvk.Kind, err)
	}

	items, err := meta.ExtractList(list)
	if err != nil {
		return nil, fmt.Errorf("extract list %s: %w", kind.gvk.Kind, err)
	}

	objects := make([]client.Object, 0, len(items))
	for _, item := range items {
		obj, ok := item.(client.Object)
		if !ok {
			return nil, fmt.Errorf("%T is not an object", item)
		}
		objects = append(objects, obj)
	}
	return objects, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NamespaceClassReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := checkManagedKinds(r.Scheme); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.NamespaceClass{}).
		Named("namespaceclass").
		Complete(r)
}
