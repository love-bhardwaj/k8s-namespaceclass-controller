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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/love-bhardwaj/k8s-namespaceclass-controller/api/v1alpha1"
)

type testNoItemsList struct {
	metav1.TypeMeta
	metav1.ListMeta
}

func (l *testNoItemsList) DeepCopyObject() runtime.Object {
	c := *l
	return &c
}

type testUnknownItemsList struct {
	metav1.TypeMeta
	metav1.ListMeta
	Items []runtime.Unknown
}

func (l *testUnknownItemsList) DeepCopyObject() runtime.Object {
	c := *l
	c.Items = append([]runtime.Unknown(nil), l.Items...)
	return &c
}

func TestNamespaceClassReconciler_Reconcile(t *testing.T) {
	patchNotCalledInterceptor := func(t *testing.T) func(
		context.Context,
		client.WithWatch,
		client.Object,
		client.Patch,
		...client.PatchOption,
	) error {
		return func(
			_ context.Context,
			_ client.WithWatch,
			_ client.Object,
			_ client.Patch,
			_ ...client.PatchOption,
		) error {
			assert.Fail(t, "patch should not run")
			return nil
		}
	}

	tests := []struct {
		name    string
		setup   func(t *testing.T) client.Client
		wantErr string
	}{
		{
			name: "error get namespaceclass object",
			setup: func(_ *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithInterceptorFuncs(interceptor.Funcs{
						Get: func(
							_ context.Context,
							_ client.WithWatch,
							_ client.ObjectKey,
							_ client.Object,
							_ ...client.GetOption,
						) error {
							return errors.New("connection refused")
						},
					}).
					Build()
			},
			wantErr: "get namespaceclass",
		},
		{
			name: "namespaceclass object not found",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithInterceptorFuncs(interceptor.Funcs{
						Patch: patchNotCalledInterceptor(t),
					}).
					Build()
			},
		},
		{
			name: "namespaceclass not set for deletion finalizer exists we skip",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithObjects(&v1alpha1.NamespaceClass{
						ObjectMeta: metav1.ObjectMeta{
							Name:       testNamespaceClass,
							Finalizers: []string{namespaceClassFinalizer},
						},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						Patch: patchNotCalledInterceptor(t),
					}).
					Build()
			},
		},
		{
			name: "namespaceclass not set for deletion finalizer patch failed",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithObjects(&v1alpha1.NamespaceClass{
						ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						Patch: func(
							_ context.Context,
							_ client.WithWatch,
							_ client.Object,
							_ client.Patch,
							_ ...client.PatchOption,
						) error {
							return errors.New("patch failure")
						},
					}).
					Build()
			},
			wantErr: "add finalizer to namespaceclass",
		},
		{
			name: "namespaceclass not set for deletion finalizer patch success",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithObjects(&v1alpha1.NamespaceClass{
						ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						Patch: func(
							ctx context.Context,
							c client.WithWatch,
							obj client.Object,
							patch client.Patch,
							opts ...client.PatchOption,
						) error {
							require.NoError(t, c.Patch(ctx, obj, patch, opts...))
							stored := &v1alpha1.NamespaceClass{}
							require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), stored))
							assert.True(
								t,
								controllerutil.ContainsFinalizer(stored, namespaceClassFinalizer),
								"stored namespace class should have the finalizer",
							)
							return nil
						},
					}).
					Build()
			},
		},
		{
			name: "namespaceclass set for deletion finalizer not found skipped",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithObjects(&v1alpha1.NamespaceClass{
						ObjectMeta: metav1.ObjectMeta{
							Name:              testNamespaceClass,
							DeletionTimestamp: &metav1.Time{Time: time.Now()},
							Finalizers:        []string{"test-other-finalizer"},
						},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						List: func(
							_ context.Context,
							_ client.WithWatch,
							_ client.ObjectList,
							_ ...client.ListOption,
						) error {
							assert.Fail(t, "cleanup should not run")
							return nil
						},
						Patch: patchNotCalledInterceptor(t),
					}).
					Build()
			},
		},
		{
			name: "namespaceclass cleanup error",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, scheme.AddToScheme(s))
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithObjects(&v1alpha1.NamespaceClass{
						ObjectMeta: metav1.ObjectMeta{
							Name:              testNamespaceClass,
							DeletionTimestamp: &metav1.Time{Time: time.Now()},
							Finalizers:        []string{namespaceClassFinalizer},
						},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						List: func(
							_ context.Context,
							_ client.WithWatch,
							_ client.ObjectList,
							_ ...client.ListOption,
						) error {
							return errors.New("list failure")
						},
						Patch: patchNotCalledInterceptor(t),
					}).
					Build()
			},
			wantErr: "clean up namespaceclass managed objects",
		},
		{
			name: "namespaceclass cleanup success path finalizer error",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, scheme.AddToScheme(s))
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithObjects(&v1alpha1.NamespaceClass{
						ObjectMeta: metav1.ObjectMeta{
							Name:              testNamespaceClass,
							DeletionTimestamp: &metav1.Time{Time: time.Now()},
							Finalizers:        []string{namespaceClassFinalizer},
						},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						Patch: func(
							_ context.Context,
							_ client.WithWatch,
							_ client.Object,
							_ client.Patch,
							_ ...client.PatchOption,
						) error {
							return errors.New("patch failure")
						},
					}).
					Build()
			},
			wantErr: "remove finalizer from namespaceclass",
		},
		{
			name: "namespaceclass clean and patch finalizer success",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, scheme.AddToScheme(s))
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithObjects(&v1alpha1.NamespaceClass{
						ObjectMeta: metav1.ObjectMeta{
							Name:              testNamespaceClass,
							DeletionTimestamp: &metav1.Time{Time: time.Now()},
							Finalizers:        []string{namespaceClassFinalizer},
						},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						Patch: func(
							ctx context.Context,
							c client.WithWatch,
							obj client.Object,
							patch client.Patch,
							opts ...client.PatchOption,
						) error {
							require.NoError(t, c.Patch(ctx, obj, patch, opts...))
							err := c.Get(ctx, client.ObjectKeyFromObject(obj), &v1alpha1.NamespaceClass{})
							assert.True(t, apierrors.IsNotFound(err), "namespace class should be deleted, got %v", err)
							return nil
						},
					}).
					Build()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.setup(t)
			ncr := &NamespaceClassReconciler{Client: c, Scheme: c.Scheme()}

			res, err := ncr.Reconcile(t.Context(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: testNamespaceClass},
			})
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, ctrl.Result{}, res)
		})
	}
}

func TestNamespaceClassReconciler_cleanup(t *testing.T) {
	deleteNotCalledInterceptor := func(t *testing.T) func(
		context.Context,
		client.WithWatch,
		client.Object,
		...client.DeleteOption,
	) error {
		return func(
			_ context.Context,
			_ client.WithWatch,
			_ client.Object,
			_ ...client.DeleteOption,
		) error {
			assert.Fail(t, "delete should not run")
			return nil
		}
	}

	patchNotCalledInterceptor := func(t *testing.T) func(
		context.Context,
		client.WithWatch,
		client.Object,
		client.Patch,
		...client.PatchOption,
	) error {
		return func(
			_ context.Context,
			_ client.WithWatch,
			_ client.Object,
			_ client.Patch,
			_ ...client.PatchOption,
		) error {
			assert.Fail(t, "patch should not run")
			return nil
		}
	}

	tests := []struct {
		name           string
		namespaceClass *v1alpha1.NamespaceClass
		setup          func(t *testing.T) client.Client
		wantErr        string
	}{
		{
			name: "list managed error",
			namespaceClass: &v1alpha1.NamespaceClass{
				ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
				Spec:       v1alpha1.NamespaceClassSpec{PruningOnDelete: true},
			},
			setup: func(t *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithObjects(&networkingv1.NetworkPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-network-policy",
							Namespace: testNamespace,
							Labels:    map[string]string{managedByLabel: testNamespaceClass},
						},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						List: func(
							ctx context.Context,
							c client.WithWatch,
							list client.ObjectList,
							opts ...client.ListOption,
						) error {
							if _, ok := list.(*corev1.ConfigMapList); ok {
								return errors.New("list failure")
							}
							return c.List(ctx, list, opts...)
						},
						Delete: func(
							ctx context.Context,
							c client.WithWatch,
							obj client.Object,
							opts ...client.DeleteOption,
						) error {
							_, ok := obj.(*networkingv1.NetworkPolicy)
							assert.True(t, ok, "cleanup should continue to the network policy, got %T", obj)
							return c.Delete(ctx, obj, opts...)
						},
						Patch: patchNotCalledInterceptor(t),
					}).
					Build()
			},
			wantErr: "list managed ConfigMap",
		},
		{
			name: "object for delete not found skipped",
			namespaceClass: &v1alpha1.NamespaceClass{
				ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
				Spec:       v1alpha1.NamespaceClassSpec{PruningOnDelete: true},
			},
			setup: func(t *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithObjects(&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      testConfigMap,
							Namespace: testNamespace,
							Labels:    map[string]string{managedByLabel: testNamespaceClass},
						},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						Delete: func(
							_ context.Context,
							_ client.WithWatch,
							obj client.Object,
							_ ...client.DeleteOption,
						) error {
							return apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, obj.GetName())
						},
						Patch: patchNotCalledInterceptor(t),
					}).
					Build()
			},
		},
		{
			name: "delete object error",
			namespaceClass: &v1alpha1.NamespaceClass{
				ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
				Spec:       v1alpha1.NamespaceClassSpec{PruningOnDelete: true},
			},
			setup: func(t *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithObjects(&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      testConfigMap,
							Namespace: testNamespace,
							Labels:    map[string]string{managedByLabel: testNamespaceClass},
						},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						Delete: func(
							_ context.Context,
							_ client.WithWatch,
							_ client.Object,
							_ ...client.DeleteOption,
						) error {
							return errors.New("delete failure")
						},
						Patch: patchNotCalledInterceptor(t),
					}).
					Build()
			},
			wantErr: "delete object *v1.ConfigMap test-namespace/test-config-map",
		},
		{
			name: "patching labels object not found skipped",
			namespaceClass: &v1alpha1.NamespaceClass{
				ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
			},
			setup: func(t *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithObjects(&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      testConfigMap,
							Namespace: testNamespace,
							Labels:    map[string]string{managedByLabel: testNamespaceClass},
						},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						Delete: deleteNotCalledInterceptor(t),
						Patch: func(
							_ context.Context,
							_ client.WithWatch,
							obj client.Object,
							_ client.Patch,
							_ ...client.PatchOption,
						) error {
							return apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, obj.GetName())
						},
					}).
					Build()
			},
		},
		{
			name: "patching labels error",
			namespaceClass: &v1alpha1.NamespaceClass{
				ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
			},
			setup: func(t *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithObjects(&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      testConfigMap,
							Namespace: testNamespace,
							Labels:    map[string]string{managedByLabel: testNamespaceClass},
						},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						Delete: deleteNotCalledInterceptor(t),
						Patch: func(
							_ context.Context,
							_ client.WithWatch,
							_ client.Object,
							_ client.Patch,
							_ ...client.PatchOption,
						) error {
							return errors.New("patch failure")
						},
					}).
					Build()
			},
			wantErr: "remove label patch *v1.ConfigMap test-namespace/test-config-map",
		},
		{
			name: "cleanup success",
			namespaceClass: &v1alpha1.NamespaceClass{
				ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
			},
			setup: func(t *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithObjects(&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      testConfigMap,
							Namespace: testNamespace,
							Labels: map[string]string{
								managedByLabel: testNamespaceClass,
								"test-label":   "test-value",
							},
						},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						Delete: deleteNotCalledInterceptor(t),
						Patch: func(
							ctx context.Context,
							c client.WithWatch,
							obj client.Object,
							patch client.Patch,
							opts ...client.PatchOption,
						) error {
							require.NoError(t, c.Patch(ctx, obj, patch, opts...))
							stored := &corev1.ConfigMap{}
							require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), stored))
							assert.Equal(t, map[string]string{"test-label": "test-value"}, stored.Labels)
							return nil
						},
					}).
					Build()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ncr := &NamespaceClassReconciler{Client: tt.setup(t)}

			err := ncr.cleanup(t.Context(), tt.namespaceClass)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestNamespaceClassReconciler_listManaged(t *testing.T) {
	configMapKind := managedKind{
		obj:  &corev1.ConfigMap{},
		list: &corev1.ConfigMapList{},
		gvk:  corev1.SchemeGroupVersion.WithKind("ConfigMap"),
	}

	tests := []struct {
		name               string
		kind               managedKind
		namespaceClassName string
		setup              func(t *testing.T) client.Client
		want               []string
		wantErr            string
	}{
		{
			name:               "list managed error",
			kind:               configMapKind,
			namespaceClassName: testNamespaceClass,
			setup: func(_ *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithInterceptorFuncs(interceptor.Funcs{
						List: func(
							_ context.Context,
							_ client.WithWatch,
							_ client.ObjectList,
							_ ...client.ListOption,
						) error {
							return errors.New("list failure")
						},
					}).
					Build()
			},
			wantErr: "list managed ConfigMap",
		},
		{
			name: "extract list error",
			kind: managedKind{
				list: &testNoItemsList{},
				gvk:  schema.GroupVersionKind{Group: testGroup, Version: "v1", Kind: "TestNoItems"},
			},
			namespaceClassName: testNamespaceClass,
			setup: func(_ *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithInterceptorFuncs(interceptor.Funcs{
						List: func(
							_ context.Context,
							_ client.WithWatch,
							_ client.ObjectList,
							_ ...client.ListOption,
						) error {
							return nil
						},
					}).
					Build()
			},
			wantErr: "extract list TestNoItems",
		},
		{
			name: "not an object error",
			kind: managedKind{
				list: &testUnknownItemsList{},
				gvk:  schema.GroupVersionKind{Group: testGroup, Version: "v1", Kind: "TestUnknownItems"},
			},
			namespaceClassName: testNamespaceClass,
			setup: func(t *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithInterceptorFuncs(interceptor.Funcs{
						List: func(
							_ context.Context,
							_ client.WithWatch,
							list client.ObjectList,
							_ ...client.ListOption,
						) error {
							unknownList, ok := list.(*testUnknownItemsList)
							require.True(t, ok)
							unknownList.Items = []runtime.Unknown{{}}
							return nil
						},
					}).
					Build()
			},
			wantErr: "*runtime.Unknown is not an object",
		},
		{
			name:               "listed all objects",
			kind:               configMapKind,
			namespaceClassName: testNamespaceClass,
			setup: func(_ *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithObjects(
						&corev1.ConfigMap{
							ObjectMeta: metav1.ObjectMeta{
								Name:      testConfigMap,
								Namespace: testNamespace,
								Labels:    map[string]string{managedByLabel: testNamespaceClass},
							},
						},
						&corev1.ConfigMap{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-second-config-map",
								Namespace: testSecondNamespace,
								Labels:    map[string]string{managedByLabel: testNamespaceClass},
							},
						},
						&corev1.ConfigMap{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-other-class-config-map",
								Namespace: testNamespace,
								Labels:    map[string]string{managedByLabel: "test-other-class"},
							},
						},
						&corev1.ConfigMap{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-unmanaged-config-map",
								Namespace: testNamespace,
							},
						},
					).
					Build()
			},
			want: []string{testConfigMap, "test-second-config-map"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ncr := &NamespaceClassReconciler{Client: tt.setup(t)}

			objs, err := ncr.listManaged(t.Context(), tt.kind, tt.namespaceClassName)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			names := make([]string, 0, len(objs))
			for _, obj := range objs {
				names = append(names, obj.GetName())
			}
			assert.ElementsMatch(t, tt.want, names)
		})
	}
}
