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

package v1alpha1_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-openapi/testify/v2/require"
	"github.com/love-bhardwaj/k8s-namespaceclass-controller/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const testConfigMapTemplate = `{
	"apiVersion": "v1",
	"kind": "ConfigMap",
	"metadata": {"name": "test-config-map"},
	"data": {"LOG_LEVEL": "info"}
}`

func TestCreateNamespaceClass(t *testing.T) {
	wd, err := os.Getwd()
	require.NoError(t, err)
	env := &envtest.Environment{
		BinaryAssetsDirectory: filepath.Join(wd, "../../bin/k8s/1.37.0-darwin-amd64"),
		CRDDirectoryPaths:     []string{filepath.Join(wd, "../../config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		err := env.Stop()
		require.NoError(t, err)
	})

	scheme := runtime.NewScheme()
	err = v1alpha1.AddToScheme(scheme)
	require.NoError(t, err)

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)

	tests := []struct {
		name           string
		namespaceClass *v1alpha1.NamespaceClass
		wantErr        string
	}{
		{
			name:           "accepts config map",
			namespaceClass: newNamespaceClass("test-config", testConfigMapTemplate),
		},
		{
			name: "version not v1",
			namespaceClass: newNamespaceClass("invalid-version", `{
					"apiVersion": "v2",
					"kind": "ConfigMap",
					"metadata": {"name": "test-config-map"},
					"data": {"LOG_LEVEL": "info"}
				}`,
			),
			wantErr: "only v1 ConfigMap is allowed",
		},
		{
			name: "not a config map",
			namespaceClass: newNamespaceClass("invalid-version", `{
					"apiVersion": "v1",
					"kind": "Secret",
					"metadata": {"name": "test-config-map"},
					"data": {"LOG_LEVEL": "info"}
				}`,
			),
			wantErr: "only v1 ConfigMap is allowed",
		},
		{
			name: "kind missing",
			namespaceClass: newNamespaceClass("invalid-version", `{
					"apiVersion": "v1",
					"metadata": {"name": "test-config-map"},
					"data": {"LOG_LEVEL": "info"}
				}`,
			),
			wantErr: "template.kind: Required value",
		},
		{
			name: "version missing",
			namespaceClass: newNamespaceClass("invalid-version", `{
					"kind": "ConfigMap",
					"metadata": {"invalid": "test-config-map"},
					"data": {"LOG_LEVEL": "info"}
				}`,
			),
			wantErr: "template.apiVersion: Required value",
		},
		{
			name: "metadata name is missing",
			namespaceClass: newNamespaceClass("invalid-version", `{
					"apiVersion": "v1",
					"kind": "ConfigMap",
					"metadata": {"invalid": "test-config-map"},
					"data": {"LOG_LEVEL": "info"}
				}`,
			),
			wantErr: "template must set metadata.name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			createErr := k8sClient.Create(t.Context(), tt.namespaceClass)

			if tt.wantErr != "" {
				assert.ErrorContains(t, createErr, tt.wantErr)
				return
			}
			assert.NoError(t, createErr)
			// TODO: assert on the object stored is what we expect.
		})
	}
}

func newNamespaceClass(name string, template string) *v1alpha1.NamespaceClass {
	return &v1alpha1.NamespaceClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.NamespaceClassSpec{
			Resources: []v1alpha1.NamespaceClassResource{
				{
					Template: runtime.RawExtension{
						Raw: []byte(template),
					},
				},
			},
		},
	}
}
