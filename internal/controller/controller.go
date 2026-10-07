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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// managedKind is a kind a custom resource that can be created.
type managedKind struct {
	// obj is an empty object of the kind.
	obj client.Object
	// list is an empty list of the kind.
	list client.ObjectList
	// gvk is the group, version and kind of obj.
	gvk schema.GroupVersionKind
}

var managedKinds = []managedKind{
	{
		obj:  &corev1.ConfigMap{},
		list: &corev1.ConfigMapList{},
		gvk:  corev1.SchemeGroupVersion.WithKind("ConfigMap"),
	},
	{
		obj:  &networkingv1.NetworkPolicy{},
		list: &networkingv1.NetworkPolicyList{},
		gvk:  networkingv1.SchemeGroupVersion.WithKind("NetworkPolicy"),
	},
}

func checkManagedKinds(scheme *runtime.Scheme) error {
	for _, kind := range managedKinds {
		gvk, err := apiutil.GVKForObject(kind.obj, scheme)
		if err != nil {
			return fmt.Errorf("object gvk %T: %w", kind.obj, err)
		}
		if gvk != kind.gvk {
			return fmt.Errorf("managed kind %T is %s in the scheme expected %s", kind.obj, gvk, kind.gvk)
		}
		listGVK, err := apiutil.GVKForObject(kind.list, scheme)
		if err != nil {
			return fmt.Errorf("list gvk %T: %w", kind.list, err)
		}
		if want := kind.gvk.GroupVersion().WithKind(kind.gvk.Kind + "List"); listGVK != want {
			return fmt.Errorf("managed kind list %T is %s in the scheme expected %s", kind.list, listGVK, want)
		}
	}
	return nil
}
