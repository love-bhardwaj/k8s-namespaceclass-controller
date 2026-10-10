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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
)

func TestCheckManagedKinds(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T) *runtime.Scheme
		wantErr string
	}{
		{
			name: "object gvk error",
			setup: func(_ *testing.T) *runtime.Scheme {
				return runtime.NewScheme()
			},
			wantErr: "object gvk *v1.ConfigMap",
		},
		{
			name: "gvk don't match",
			setup: func(_ *testing.T) *runtime.Scheme {
				s := runtime.NewScheme()
				s.AddKnownTypeWithName(
					schema.GroupVersionKind{Group: testGroup, Version: "v1", Kind: "ConfigMap"},
					&corev1.ConfigMap{},
				)
				return s
			},
			wantErr: "managed kind *v1.ConfigMap is test.example.io/v1," +
				" Kind=ConfigMap in the scheme expected /v1, Kind=ConfigMap",
		},
		{
			name: "list gvk error",
			setup: func(_ *testing.T) *runtime.Scheme {
				s := runtime.NewScheme()
				s.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.ConfigMap{})
				s.AddKnownTypes(networkingv1.SchemeGroupVersion, &networkingv1.NetworkPolicy{})
				return s
			},
			wantErr: "list gvk *v1.ConfigMapList",
		},
		{
			name: "list gvk not equal to wanted",
			setup: func(_ *testing.T) *runtime.Scheme {
				s := runtime.NewScheme()
				s.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.ConfigMap{})
				s.AddKnownTypeWithName(
					corev1.SchemeGroupVersion.WithKind("TestConfigMapCollection"),
					&corev1.ConfigMapList{},
				)
				return s
			},
			wantErr: "managed kind list *v1.ConfigMapList is /v1," +
				" Kind=TestConfigMapCollection in the scheme expected /v1, Kind=ConfigMapList",
		},
		{
			name: "valid kinds",
			setup: func(t *testing.T) *runtime.Scheme {
				s := runtime.NewScheme()
				require.NoError(t, scheme.AddToScheme(s))
				return s
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkManagedKinds(tt.setup(t))
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}
