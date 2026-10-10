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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/love-bhardwaj/k8s-namespaceclass-controller/api/v1alpha1"
)

const (
	testNamespace       = "test-namespace"
	testSecondNamespace = "test-second-namespace"
	testNamespaceClass  = "test-class"
	testConfigMap       = "test-config-map"
	testGroup           = "test.example.io"
)

func TestNamespaceReconciler_Reconcile(t *testing.T) {
	errorInterceptor := func(
		_ context.Context,
		_ client.WithWatch,
		_ client.ObjectKey,
		_ client.Object,
		_ ...client.GetOption,
	) error {
		return errors.New("connection refused")
	}

	applyInvalidInterceptor := func(
		_ context.Context,
		_ client.WithWatch,
		_ runtime.ApplyConfiguration,
		_ ...client.ApplyOption,
	) error {
		return apierrors.NewInvalid(
			schema.GroupKind{Kind: "ConfigMap"},
			testConfigMap,
			field.ErrorList{field.Invalid(field.NewPath("data"), "bad key!", "invalid key")},
		)
	}

	applyNotCalledInterceptor := func(t *testing.T) func(
		context.Context,
		client.WithWatch,
		runtime.ApplyConfiguration,
		...client.ApplyOption,
	) error {
		return func(
			_ context.Context,
			_ client.WithWatch,
			_ runtime.ApplyConfiguration,
			_ ...client.ApplyOption,
		) error {
			assert.Fail(t, "apply should not run")
			return nil
		}
	}

	pruneNotCalledInterceptor := func(t *testing.T) func(
		context.Context,
		client.WithWatch,
		client.Object,
		...client.DeleteAllOfOption,
	) error {
		return func(
			_ context.Context,
			_ client.WithWatch,
			_ client.Object,
			_ ...client.DeleteAllOfOption,
		) error {
			assert.Fail(t, "prune should not run")
			return nil
		}
	}

	statusUpdateInterceptor := func(
		t *testing.T,
		wantStatus metav1.ConditionStatus,
		wantReason string,
		updateErr error,
	) func(
		context.Context,
		client.Client,
		string,
		client.Object,
		...client.SubResourceUpdateOption,
	) error {
		return func(
			ctx context.Context,
			c client.Client,
			subResourceName string,
			obj client.Object,
			opts ...client.SubResourceUpdateOption,
		) error {
			nc, ok := obj.(*v1alpha1.NamespaceClass)
			require.True(t, ok, "status update should be for the namespace class")
			cond := meta.FindStatusCondition(nc.Status.Conditions, typeTemplateValid)
			require.NotNil(t, cond)
			assert.Equal(t, wantStatus, cond.Status)
			assert.Equal(t, wantReason, cond.Reason)
			if updateErr != nil {
				return updateErr
			}
			return c.SubResource(subResourceName).Update(ctx, obj, opts...)
		}
	}

	tests := []struct {
		name         string
		setup        func(t *testing.T) client.Client
		wantErr      string
		wantTerminal bool
	}{
		{
			name: "namespace not found",
			setup: func(_ *testing.T) client.Client {
				return fake.NewClientBuilder().Build()
			},
		},
		{
			name: "get namespace error",
			setup: func(_ *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithInterceptorFuncs(interceptor.Funcs{
						Get: errorInterceptor,
					}).
					Build()
			},
			wantErr: "get namespace",
		},
		{
			name: "namespace set for deletion",
			setup: func(t *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithObjects(&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:              testNamespace,
							DeletionTimestamp: &metav1.Time{Time: time.Now()},
							Finalizers:        []string{"kubernetes"},
						},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						Get: func(
							ctx context.Context,
							c client.WithWatch,
							key client.ObjectKey,
							obj client.Object,
							opts ...client.GetOption,
						) error {
							err := c.Get(ctx, key, obj, opts...)
							assert.NoError(t, err, "namespace should be found")
							return err
						},
						DeleteAllOf: pruneNotCalledInterceptor(t),
					}).
					Build()
			},
		},
		{
			name: "namespace does not have class label is pruned",
			setup: func(t *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithObjects(&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{Name: testNamespace},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						Apply: applyNotCalledInterceptor(t),
						DeleteAllOf: func(
							_ context.Context,
							_ client.WithWatch,
							_ client.Object,
							opts ...client.DeleteAllOfOption,
						) error {
							o := &client.DeleteAllOfOptions{}
							o.ApplyOptions(opts)
							assert.Equal(t, testNamespace, o.Namespace)
							assert.Equal(t, managedByLabel, o.LabelSelector.String())
							assert.Empty(t, o.FieldSelector.String(), "all managed objects should be pruned")
							return nil
						},
					}).
					Build()
			},
		},
		{
			name: "namespaceclass not found",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, scheme.AddToScheme(s))
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithObjects(&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:   testNamespace,
							Labels: map[string]string{classLabel: "test-missing-namespace-class"},
						},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						Get: func(
							ctx context.Context,
							c client.WithWatch,
							key client.ObjectKey,
							obj client.Object,
							opts ...client.GetOption,
						) error {
							err := c.Get(ctx, key, obj, opts...)
							if _, ok := obj.(*v1alpha1.NamespaceClass); ok {
								assert.True(t, apierrors.IsNotFound(err), "namespace class should not be found, got %v", err)
							}
							return err
						},
						DeleteAllOf: pruneNotCalledInterceptor(t),
					}).
					Build()
			},
		},
		{
			name: "get namespaceclass error",
			setup: func(_ *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithObjects(&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:   testNamespace,
							Labels: map[string]string{classLabel: testNamespaceClass},
						},
					}).
					WithInterceptorFuncs(interceptor.Funcs{
						Get: func(
							ctx context.Context,
							c client.WithWatch,
							key client.ObjectKey,
							obj client.Object,
							opts ...client.GetOption,
						) error {
							if _, ok := obj.(*v1alpha1.NamespaceClass); ok {
								return errors.New("connection refused")
							}
							return c.Get(ctx, key, obj, opts...)
						},
					}).
					Build()
			},
			wantErr: "get namespaceclass test-class",
		},
		{
			name: "namespaceclass set for deletion",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, scheme.AddToScheme(s))
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithObjects(
						&corev1.Namespace{
							ObjectMeta: metav1.ObjectMeta{
								Name:   testNamespace,
								Labels: map[string]string{classLabel: testNamespaceClass},
							},
						},
						&v1alpha1.NamespaceClass{
							ObjectMeta: metav1.ObjectMeta{
								Name:              testNamespaceClass,
								DeletionTimestamp: &metav1.Time{Time: time.Now()},
								Finalizers:        []string{namespaceClassFinalizer},
							},
						},
					).
					WithInterceptorFuncs(interceptor.Funcs{
						Get: func(
							ctx context.Context,
							c client.WithWatch,
							key client.ObjectKey,
							obj client.Object,
							opts ...client.GetOption,
						) error {
							err := c.Get(ctx, key, obj, opts...)
							if _, ok := obj.(*v1alpha1.NamespaceClass); ok {
								assert.NoError(t, err, "namespace class should be found")
							}
							return err
						},
						Apply:       applyNotCalledInterceptor(t),
						DeleteAllOf: pruneNotCalledInterceptor(t),
					}).
					Build()
			},
		},
		{
			name: "rendering error set condition failure",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, scheme.AddToScheme(s))
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithObjects(
						&corev1.Namespace{
							ObjectMeta: metav1.ObjectMeta{
								Name:   testNamespace,
								Labels: map[string]string{classLabel: testNamespaceClass},
							},
						},
						&v1alpha1.NamespaceClass{
							ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
							Spec: v1alpha1.NamespaceClassSpec{
								Resources: []v1alpha1.NamespaceClassResource{{
									Template: runtime.RawExtension{Raw: []byte(
										`{"apiVersion":"v1","kind":"ConfigMap",` +
											`"metadata":{"name":"test-config-map","namespace":"test-other-namespace"}}`,
									)},
								}},
							},
						},
					).
					WithInterceptorFuncs(interceptor.Funcs{
						SubResourceUpdate: statusUpdateInterceptor(
							t, metav1.ConditionFalse, reasonInvalidTemplate, errors.New("status update failure"),
						),
					}).
					Build()
			},
			wantErr: "set template valid after render failure",
		},
		{
			name: "render error condition set terminal error",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, scheme.AddToScheme(s))
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithStatusSubresource(&v1alpha1.NamespaceClass{}).
					WithObjects(
						&corev1.Namespace{
							ObjectMeta: metav1.ObjectMeta{
								Name:   testNamespace,
								Labels: map[string]string{classLabel: testNamespaceClass},
							},
						},
						&v1alpha1.NamespaceClass{
							ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
							Spec: v1alpha1.NamespaceClassSpec{
								Resources: []v1alpha1.NamespaceClassResource{{
									Template: runtime.RawExtension{Raw: []byte(
										`{"apiVersion":"v1","kind":"ConfigMap",` +
											`"metadata":{"name":"test-config-map","namespace":"test-other-namespace"}}`,
									)},
								}},
							},
						},
					).
					WithInterceptorFuncs(interceptor.Funcs{
						SubResourceUpdate: statusUpdateInterceptor(t, metav1.ConditionFalse, reasonInvalidTemplate, nil),
						Apply:             applyNotCalledInterceptor(t),
						DeleteAllOf:       pruneNotCalledInterceptor(t),
					}).
					Build()
			},
			wantErr:      "resource test-class has invalid template",
			wantTerminal: true,
		},
		{
			name: "rendering success set condition failure",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, scheme.AddToScheme(s))
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithObjects(
						&corev1.Namespace{
							ObjectMeta: metav1.ObjectMeta{
								Name:   testNamespace,
								Labels: map[string]string{classLabel: testNamespaceClass},
							},
						},
						&v1alpha1.NamespaceClass{
							ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
							Spec: v1alpha1.NamespaceClassSpec{
								Resources: []v1alpha1.NamespaceClassResource{{
									Template: runtime.RawExtension{Raw: []byte(
										`{"apiVersion":"v1","kind":"ConfigMap",` +
											`"metadata":{"name":"test-config-map"},"data":{"LOG_LEVEL":"info"}}`,
									)},
								}},
							},
						},
					).
					WithInterceptorFuncs(interceptor.Funcs{
						Apply: func(
							_ context.Context,
							_ client.WithWatch,
							obj runtime.ApplyConfiguration,
							_ ...client.ApplyOption,
						) error {
							named, ok := obj.(interface{ GetName() string })
							require.True(t, ok)
							assert.Equal(t, testConfigMap, named.GetName())
							return nil
						},
						SubResourceUpdate: statusUpdateInterceptor(
							t, metav1.ConditionTrue, reasonValidTemplate, errors.New("status update failure"),
						),
						DeleteAllOf: pruneNotCalledInterceptor(t),
					}).
					Build()
			},
			wantErr: "set template valid after apply",
		},
		{
			name: "apply error",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, scheme.AddToScheme(s))
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithObjects(
						&corev1.Namespace{
							ObjectMeta: metav1.ObjectMeta{
								Name:   testNamespace,
								Labels: map[string]string{classLabel: testNamespaceClass},
							},
						},
						&v1alpha1.NamespaceClass{
							ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
							Spec: v1alpha1.NamespaceClassSpec{
								Resources: []v1alpha1.NamespaceClassResource{{
									Template: runtime.RawExtension{Raw: []byte(
										`{"apiVersion":"v1","kind":"ConfigMap",` +
											`"metadata":{"name":"test-config-map"},"data":{"LOG_LEVEL":"info"}}`,
									)},
								}},
							},
						},
					).
					WithInterceptorFuncs(interceptor.Funcs{
						Apply: func(
							_ context.Context,
							_ client.WithWatch,
							_ runtime.ApplyConfiguration,
							_ ...client.ApplyOption,
						) error {
							return errors.New("apply failure")
						},
						SubResourceUpdate: func(
							_ context.Context,
							_ client.Client,
							_ string,
							_ client.Object,
							_ ...client.SubResourceUpdateOption,
						) error {
							assert.Fail(t, "status should not be updated when apply fails")
							return nil
						},
						DeleteAllOf: pruneNotCalledInterceptor(t),
					}).
					Build()
			},
			wantErr: "apply test-config-map",
		},
		{
			name: "invalid objects set condition failure",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, scheme.AddToScheme(s))
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithObjects(
						&corev1.Namespace{
							ObjectMeta: metav1.ObjectMeta{
								Name:   testNamespace,
								Labels: map[string]string{classLabel: testNamespaceClass},
							},
						},
						&v1alpha1.NamespaceClass{
							ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
							Spec: v1alpha1.NamespaceClassSpec{
								Resources: []v1alpha1.NamespaceClassResource{{
									Template: runtime.RawExtension{Raw: []byte(
										`{"apiVersion":"v1","kind":"ConfigMap",` +
											`"metadata":{"name":"test-config-map"},"data":{"LOG_LEVEL":"info"}}`,
									)},
								}},
							},
						},
					).
					WithInterceptorFuncs(interceptor.Funcs{
						Apply: applyInvalidInterceptor,
						SubResourceUpdate: statusUpdateInterceptor(
							t, metav1.ConditionFalse, reasonInvalidTemplate, errors.New("status update failure"),
						),
						DeleteAllOf: pruneNotCalledInterceptor(t),
					}).
					Build()
			},
			wantErr: "set template valid after rejected objects",
		},
		{
			name: "invalid object condition set terminal error",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, scheme.AddToScheme(s))
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithStatusSubresource(&v1alpha1.NamespaceClass{}).
					WithObjects(
						&corev1.Namespace{
							ObjectMeta: metav1.ObjectMeta{
								Name:   testNamespace,
								Labels: map[string]string{classLabel: testNamespaceClass},
							},
						},
						&v1alpha1.NamespaceClass{
							ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
							Spec: v1alpha1.NamespaceClassSpec{
								Resources: []v1alpha1.NamespaceClassResource{{
									Template: runtime.RawExtension{Raw: []byte(
										`{"apiVersion":"v1","kind":"ConfigMap",` +
											`"metadata":{"name":"test-config-map"},"data":{"LOG_LEVEL":"info"}}`,
									)},
								}},
							},
						},
					).
					WithInterceptorFuncs(interceptor.Funcs{
						Apply:             applyInvalidInterceptor,
						SubResourceUpdate: statusUpdateInterceptor(t, metav1.ConditionFalse, reasonInvalidTemplate, nil),
						DeleteAllOf:       pruneNotCalledInterceptor(t),
					}).
					Build()
			},
			wantErr:      "resource test-class has rejected objects",
			wantTerminal: true,
		},
		{
			name: "prune error",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, scheme.AddToScheme(s))
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithStatusSubresource(&v1alpha1.NamespaceClass{}).
					WithObjects(
						&corev1.Namespace{
							ObjectMeta: metav1.ObjectMeta{
								Name:   testNamespace,
								Labels: map[string]string{classLabel: testNamespaceClass},
							},
						},
						&v1alpha1.NamespaceClass{
							ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
							Spec: v1alpha1.NamespaceClassSpec{
								Resources: []v1alpha1.NamespaceClassResource{{
									Template: runtime.RawExtension{Raw: []byte(
										`{"apiVersion":"v1","kind":"ConfigMap",` +
											`"metadata":{"name":"test-config-map"},"data":{"LOG_LEVEL":"info"}}`,
									)},
								}},
							},
						},
					).
					WithInterceptorFuncs(interceptor.Funcs{
						Apply: func(
							_ context.Context,
							_ client.WithWatch,
							_ runtime.ApplyConfiguration,
							_ ...client.ApplyOption,
						) error {
							return nil
						},
						DeleteAllOf: func(
							_ context.Context,
							_ client.WithWatch,
							_ client.Object,
							_ ...client.DeleteAllOfOption,
						) error {
							return errors.New("delete failure")
						},
					}).
					Build()
			},
			wantErr: "prune managed ConfigMap",
		},
		{
			name: "success",
			setup: func(t *testing.T) client.Client {
				s := runtime.NewScheme()
				require.NoError(t, scheme.AddToScheme(s))
				require.NoError(t, v1alpha1.AddToScheme(s))
				return fake.NewClientBuilder().
					WithScheme(s).
					WithStatusSubresource(&v1alpha1.NamespaceClass{}).
					WithObjects(
						&corev1.Namespace{
							ObjectMeta: metav1.ObjectMeta{
								Name:   testNamespace,
								Labels: map[string]string{classLabel: testNamespaceClass},
							},
						},
						&v1alpha1.NamespaceClass{
							ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
							Spec: v1alpha1.NamespaceClassSpec{
								Resources: []v1alpha1.NamespaceClassResource{{
									Template: runtime.RawExtension{Raw: []byte(
										`{"apiVersion":"v1","kind":"ConfigMap",` +
											`"metadata":{"name":"test-config-map"},"data":{"LOG_LEVEL":"info"}}`,
									)},
								}},
							},
						},
					).
					WithInterceptorFuncs(interceptor.Funcs{
						Apply: func(
							_ context.Context,
							_ client.WithWatch,
							obj runtime.ApplyConfiguration,
							_ ...client.ApplyOption,
						) error {
							applied, ok := obj.(interface {
								GetName() string
								GetNamespace() string
								GetLabels() map[string]string
							})
							require.True(t, ok)
							assert.Equal(t, testConfigMap, applied.GetName())
							assert.Equal(t, testNamespace, applied.GetNamespace())
							assert.Equal(t, testNamespaceClass, applied.GetLabels()[managedByLabel])
							return nil
						},
						SubResourceUpdate: func(
							ctx context.Context,
							c client.Client,
							subResourceName string,
							obj client.Object,
							opts ...client.SubResourceUpdateOption,
						) error {
							nc, ok := obj.(*v1alpha1.NamespaceClass)
							require.True(t, ok, "status update should be for the namespace class")
							cond := meta.FindStatusCondition(nc.Status.Conditions, typeTemplateValid)
							require.NotNil(t, cond)
							assert.Equal(t, metav1.ConditionTrue, cond.Status)
							assert.Equal(t, reasonValidTemplate, cond.Reason)
							return c.SubResource(subResourceName).Update(ctx, obj, opts...)
						},
						DeleteAllOf: func(
							_ context.Context,
							_ client.WithWatch,
							obj client.Object,
							opts ...client.DeleteAllOfOption,
						) error {
							o := &client.DeleteAllOfOptions{}
							o.ApplyOptions(opts)
							assert.Equal(t, testNamespace, o.Namespace)
							assert.Equal(t, managedByLabel, o.LabelSelector.String())
							switch obj.(type) {
							case *corev1.ConfigMap:
								assert.Equal(t, "metadata.name!=test-config-map", o.FieldSelector.String())
							default:
								assert.Empty(t, o.FieldSelector.String(), "kinds with no desired objects prune everything")
							}
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
			nsr := &NamespaceReconciler{Client: c, Scheme: c.Scheme()}

			res, err := nsr.Reconcile(t.Context(), ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name: testNamespace,
				},
			})
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				if tt.wantTerminal {
					assert.ErrorIs(t, err, reconcile.TerminalError(nil))
				} else {
					assert.NotErrorIs(t, err, reconcile.TerminalError(nil))
				}
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, ctrl.Result{}, res)
		})
	}
}

func TestNamespaceReconciler_namespaces(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) client.Client
		want  []reconcile.Request
	}{
		{
			name: "failed to list namespace with class label",
			setup: func(_ *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithInterceptorFuncs(interceptor.Funcs{
						List: func(
							_ context.Context,
							_ client.WithWatch,
							_ client.ObjectList,
							_ ...client.ListOption,
						) error {
							return errors.New("connection refused")
						},
					}).
					Build()
			},
		},
		{
			name: "return reconcile request for all namespaces with class label",
			setup: func(_ *testing.T) client.Client {
				return fake.NewClientBuilder().
					WithObjects(
						&corev1.Namespace{
							ObjectMeta: metav1.ObjectMeta{
								Name:   testNamespace,
								Labels: map[string]string{classLabel: testNamespaceClass},
							},
						},
						&corev1.Namespace{
							ObjectMeta: metav1.ObjectMeta{
								Name:   testSecondNamespace,
								Labels: map[string]string{classLabel: testNamespaceClass},
							},
						},
						&corev1.Namespace{
							ObjectMeta: metav1.ObjectMeta{
								Name:   "test-other-class-namespace",
								Labels: map[string]string{classLabel: "test-other-class"},
							},
						},
						&corev1.Namespace{
							ObjectMeta: metav1.ObjectMeta{Name: "test-unlabeled-namespace"},
						},
					).
					Build()
			},
			want: []reconcile.Request{
				{
					NamespacedName: types.NamespacedName{
						Name: testNamespace,
					},
				},
				{
					NamespacedName: types.NamespacedName{
						Name: testSecondNamespace,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.setup(t)
			nsr := &NamespaceReconciler{Client: c, Scheme: c.Scheme()}

			got := nsr.namespaces(t.Context(), &v1alpha1.NamespaceClass{
				ObjectMeta: metav1.ObjectMeta{Name: testNamespaceClass},
			})
			assert.ElementsMatch(t, tt.want, got)
		})
	}
}

func TestNamespaceOf(t *testing.T) {
	got := namespaceOf(t.Context(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testConfigMap,
			Namespace: testNamespace,
		},
	})
	assert.Equal(
		t,
		[]reconcile.Request{
			{
				NamespacedName: types.NamespacedName{
					Name: testNamespace,
				},
			},
		},
		got,
	)
}
