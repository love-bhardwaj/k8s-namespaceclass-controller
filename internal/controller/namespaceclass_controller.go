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

	corev1 "k8s.io/api/core/v1"
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
	cfgMapList := &corev1.ConfigMapList{}
	if err := r.List(ctx, cfgMapList, client.MatchingLabels{managedByLabel: namespaceClass.Name}); err != nil {
		return fmt.Errorf("list managed config maps: %w", err)
	}

	var errs []error
	for i := range cfgMapList.Items {
		cfgMap := &cfgMapList.Items[i]
		if namespaceClass.Spec.PruningOnDelete {
			if err := r.Delete(ctx, cfgMap); client.IgnoreNotFound(err) != nil {
				errs = append(errs, fmt.Errorf("delete resource %s/%s: %w", cfgMap.Namespace, cfgMap.Name, err))
			}
			continue
		}

		// If pruning is disable, we can remove our label.
		patch := client.MergeFrom(cfgMap.DeepCopy())
		delete(cfgMap.Labels, managedByLabel)
		if err := r.Patch(ctx, cfgMap, patch); client.IgnoreNotFound(err) != nil {
			errs = append(errs, fmt.Errorf("remove label: %s/%s: %w", cfgMap.Namespace, cfgMap.Name, err))
		}
	}
	return errors.Join(errs...)
}

// SetupWithManager sets up the controller with the Manager.
func (r *NamespaceClassReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.NamespaceClass{}).
		Named("namespaceclass").
		Complete(r)
}
