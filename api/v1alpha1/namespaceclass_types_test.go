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
	"fmt"
	"os"
	"testing"

	"github.com/love-bhardwaj/k8s-namespaceclass-controller/api/v1alpha1"
	"github.com/love-bhardwaj/k8s-namespaceclass-controller/internal/testenv"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const testConfigMapTemplate = `{
	"apiVersion": "v1",
	"kind": "ConfigMap",
	"metadata": {"name": "test-config-map"},
	"data": {"LOG_LEVEL": "info"}
}`

const testNetworkPolicyTemplate = `{
	"apiVersion": "networking.k8s.io/v1", 
	"kind": "NetworkPolicy", 
	"metadata": {"name": "allow-world-ingress"}, 
	"spec": {"podSelector": {}}
}`

var k8sClient client.Client

func TestMain(m *testing.M) {
	code := 1
	defer func() { os.Exit(code) }()

	env, err := testenv.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	cfg, err := env.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, "start envtest:", err)
		return
	}
	defer func() {
		if err := env.Stop(); err != nil {
			fmt.Fprintln(os.Stderr, "stop envtest:", err)
		}
	}()

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}

	code = m.Run()
}

func TestCreateNamespaceClass(t *testing.T) {
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
			name:           "accepts network policy",
			namespaceClass: newNamespaceClass("test-policy", testNetworkPolicyTemplate),
		},
		{
			name:           "accepts config map and network policy",
			namespaceClass: newNamespaceClass("test-both", testConfigMapTemplate, testNetworkPolicyTemplate),
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
			wantErr: "only v1 ConfigMap and networking.k8s.io/v1 Network policy is allowed",
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
			wantErr: "only v1 ConfigMap and networking.k8s.io/v1 Network policy is allowed",
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

func newNamespaceClass(name string, templates ...string) *v1alpha1.NamespaceClass {
	resources := make([]v1alpha1.NamespaceClassResource, 0, len(templates))

	for _, tpl := range templates {
		resources = append(resources, v1alpha1.NamespaceClassResource{
			Template: runtime.RawExtension{
				Raw: []byte(tpl),
			},
		})
	}
	return &v1alpha1.NamespaceClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.NamespaceClassSpec{
			Resources: resources,
		},
	}
}
