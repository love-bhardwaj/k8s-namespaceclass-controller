//go:build e2e

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

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/love-bhardwaj/k8s-namespaceclass-controller/test/utils"
)

// namespace where the project is deployed in
const namespace = "namespaceclass-system"

// serviceAccountName created for the project
const serviceAccountName = "namespaceclass-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "namespaceclass-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "namespaceclass-metrics-binding"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				By("getting the name of the controller-manager pod")
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				By("validating the pod's status")
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating a ClusterRoleBinding for the service account to allow access to metrics")
			cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
				"--clusterrole=namespaceclass-metrics-reader",
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
			)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("ensuring the controller pod is ready")
			verifyControllerPodReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", controllerPodName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Controller pod not ready")
			}
			Eventually(verifyControllerPodReady, 3*time.Minute, time.Second).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("Serving metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted, 3*time.Minute, time.Second).Should(Succeed())

			// +kubebuilder:scaffold:e2e-metrics-webhooks-readiness

			By("creating the curl-metrics pod to access the metrics endpoint")
			curlScript := fmt.Sprintf("for i in $(seq 1 30); do "+
				"curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics "+
				"&& exit 0 || sleep 2; done; exit 1",
				token, metricsServiceName, namespace)
			curlScriptJSON, err := json.Marshal(curlScript)
			Expect(err).NotTo(HaveOccurred())
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": [%s],
							"securityContext": {
								"readOnlyRootFilesystem": true,
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccountName": "%s"
					}
				}`, curlScriptJSON, serviceAccountName))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			verifyMetricsAvailable := func(g Gomega) {
				metricsOutput, err := getMetricsOutput()
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
				g.Expect(metricsOutput).NotTo(BeEmpty())
				g.Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
			}
			Eventually(verifyMetricsAvailable, 2*time.Minute).Should(Succeed())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks

		Context("NamespaceClass", Ordered, func() {
			// NamespaceClasses for testing
			const (
				namespaceClassPublicNetwork         = "e2e-public-network"
				namespaceClassInternalNetwork       = "e2e-internal-network"
				namespaceClassTemplateSetsNamespace = "e2e-template-sets-namespace"
				namespaceClassPruneOnDelete         = "e2e-prune-on-delete"
				namespaceClassKeepOnDelete          = "e2e-keep-on-delete"
				namespaceClassInvalidDataKey        = "e2e-invalid-data-key"
				namespaceClassSecretTemplate        = "e2e-secret-template"
			)

			// Namespaces for testing
			const (
				namespaceWebPortal             = "e2e-web-portal"
				namespaceTemplateSetsNamespace = "e2e-template-sets-namespace"
				namespacePrune                 = "e2e-prune"
				namespaceKeep                  = "e2e-keep"
				namespaceInvalidDataKey        = "e2e-invalid-data-key"
			)

			// config map names for testing
			const (
				configMapAppSettings = "app-settings"
				configMapVPNSettings = "vpn-settings"
			)

			BeforeAll(func() {
				By("creating custom namespace class resources for test")
				Expect(applyManifest(createNamespaceClass(
					namespaceClassPublicNetwork,
					createConfigMap(configMapAppSettings, "info"),
				))).
					To(Succeed())
				Expect(applyManifest(createNamespaceClass(
					namespaceClassInternalNetwork,
					createConfigMap(configMapVPNSettings, "info"),
				))).
					To(Succeed())

				By("creating namespace resources for test")
				_, err := kubectl("create", "namespace", namespaceWebPortal)
				Expect(err).NotTo(HaveOccurred())
				_, err = kubectl("create", "namespace", namespaceTemplateSetsNamespace)
				Expect(err).NotTo(HaveOccurred())
				_, err = kubectl("create", "namespace", namespacePrune)
				Expect(err).NotTo(HaveOccurred())
				_, err = kubectl("create", "namespace", namespaceKeep)
				Expect(err).NotTo(HaveOccurred())
				_, err = kubectl("create", "namespace", namespaceInvalidDataKey)
				Expect(err).NotTo(HaveOccurred())
			})

			AfterAll(func() {
				By("deleting namespace class and namespaces created for test")
				_, _ = kubectl(
					"delete",
					"namespace",
					namespaceWebPortal,
					namespaceTemplateSetsNamespace,
					namespacePrune,
					namespaceKeep,
					namespaceInvalidDataKey,
					"--ignore-not-found",
					"--wait=false",
				)
				_, _ = kubectl(
					"delete",
					"namespaceclass",
					namespaceClassPublicNetwork,
					namespaceClassInternalNetwork,
					namespaceClassTemplateSetsNamespace,
					namespaceClassPruneOnDelete,
					namespaceClassKeepOnDelete,
					namespaceClassInvalidDataKey,
					"--ignore-not-found",
					"--timeout=2m",
				)
			})

			It("creates managed config maps when namespace is labeled", func() {
				_, err := kubectl("label", "namespace", namespaceWebPortal, classLabel(namespaceClassPublicNetwork))
				Expect(err).NotTo(HaveOccurred())

				Eventually(func(g Gomega) {
					g.Expect(getConfigMapField(namespaceWebPortal, configMapAppSettings, "{.data.LOG_LEVEL}")).
						To(Equal("info"))
					g.Expect(getConfigMapField(
						namespaceWebPortal,
						configMapAppSettings,
						`{.metadata.labels.namespaceclass\.lovebhardwaj\.io/managed-by}`,
					)).To(Equal(namespaceClassPublicNetwork))
				}).Should(Succeed())
			})

			It("updates managed config maps when the custom resource changes", func() {
				Expect(applyManifest(createNamespaceClass(
					namespaceClassPublicNetwork,
					createConfigMap(configMapAppSettings, "debug"),
				))).To(Succeed())

				Eventually(func(g Gomega) {
					g.Expect(getConfigMapField(namespaceWebPortal, configMapAppSettings, "{.data.LOG_LEVEL}")).To(Equal("debug"))
				}).Should(Succeed())
			})

			It("restores a managed config map deleted by hand", func() {
				_, err := kubectl("delete", "configmap", configMapAppSettings, "-n", namespaceWebPortal)
				Expect(err).NotTo(HaveOccurred())

				Eventually(func(g Gomega) {
					g.Expect(getConfigMapField(namespaceWebPortal, configMapAppSettings, "{.data.LOG_LEVEL}")).
						To(Equal("debug"))
				}).Should(Succeed())
			})

			It("reverts a managed config map edited by hand", func() {
				_, err := kubectl("patch", "configmap", configMapAppSettings, "-n", namespaceWebPortal,
					"--type=merge", "-p", `{"data":{"LOG_LEVEL":"trace"}}`)
				Expect(err).NotTo(HaveOccurred())

				Eventually(func(g Gomega) {
					g.Expect(getConfigMapField(namespaceWebPortal, configMapAppSettings, "{.data.LOG_LEVEL}")).
						To(Equal("debug"))
				}).Should(Succeed())
			})

			It("leaves a hand made config map alone", func() {
				_, err := kubectl("create", "configmap", "team-notes", "-n", namespaceWebPortal, "--from-literal=owner=web")
				Expect(err).NotTo(HaveOccurred())

				Consistently(func(g Gomega) {
					g.Expect(getConfigMapField(namespaceWebPortal, "team-notes", "{.data.owner}")).
						To(Equal("web"))
				}, 10*time.Second).Should(Succeed())
			})

			It("switches managed config maps when label changes", func() {
				_, err := kubectl(
					"label",
					"namespace",
					namespaceWebPortal,
					classLabel(namespaceClassInternalNetwork),
					"--overwrite",
				)
				Expect(err).NotTo(HaveOccurred())

				Eventually(func(g Gomega) {
					g.Expect(getManagedConfigMaps(namespaceWebPortal)).To(ConsistOf(configMapVPNSettings))
				}).Should(Succeed())
			})

			It("removes managed config maps when the label is removed", func() {
				_, err := kubectl("label", "namespace", namespaceWebPortal, "namespaceclass.lovebhardwaj.io/name-")
				Expect(err).NotTo(HaveOccurred())

				Eventually(func(g Gomega) {
					g.Expect(getManagedConfigMaps(namespaceWebPortal)).To(BeEmpty())
				}).Should(Succeed())
				Expect(getConfigMapField(namespaceWebPortal, "team-notes", "{.data.owner}")).
					To(Equal("web"), "hand made config map must survive")
			})

			It("removes label when namespaceclass resource deleted and pruning disabled", func() {
				By("labeling a namespace for a custom resource with pruningOnDelete false")

				Expect(applyManifest(createNamespaceClass(
					namespaceClassKeepOnDelete,
					createConfigMap(configMapAppSettings, "info"),
				))).To(Succeed())

				_, err := kubectl("label", "namespace", namespaceKeep, classLabel(namespaceClassKeepOnDelete))
				Expect(err).NotTo(HaveOccurred())

				Eventually(func(g Gomega) {
					g.Expect(getManagedConfigMaps(namespaceKeep)).To(ConsistOf(configMapAppSettings))
				}).Should(Succeed())

				By("deleting the custom resource")

				_, err = kubectl("delete", "namespaceclass", namespaceClassKeepOnDelete, "--timeout=2m")
				Expect(err).NotTo(HaveOccurred())

				Expect(getConfigMapField(namespaceKeep, configMapAppSettings, "{.data.LOG_LEVEL}")).
					To(Equal("info"), "config map should be kept")
				Expect(getManagedConfigMaps(namespaceKeep)).To(BeEmpty(), "managed-by label should be removed")
			})

			It("prunes resources when namespaceclass resource deleted and pruning enabled", func() {
				By("labeling a namespace for a custom resource with pruningOnDelete true")
				Expect(applyManifest(withPruningOnDelete(
					createNamespaceClass(
						namespaceClassPruneOnDelete,
						createConfigMap(configMapAppSettings, "info"),
					)))).To(Succeed())

				_, err := kubectl("label", "namespace", namespacePrune, classLabel(namespaceClassPruneOnDelete))
				Expect(err).NotTo(HaveOccurred())

				Eventually(func(g Gomega) {
					g.Expect(getManagedConfigMaps(namespacePrune)).To(ConsistOf(configMapAppSettings))
				}).Should(Succeed())

				By("deleting the custom resource")
				_, err = kubectl("delete", "namespaceclass", namespaceClassPruneOnDelete, "--timeout=2m")
				Expect(err).NotTo(HaveOccurred())

				_, err = kubectl("get", "configmap", configMapAppSettings, "-n", namespacePrune)
				Expect(err).To(HaveOccurred(), "config map should be deleted")
			})

			It("reports a template that sets metadata.namespace", func() {
				Expect(applyManifest(createNamespaceClass(namespaceClassTemplateSetsNamespace, `apiVersion: v1
kind: ConfigMap
metadata:
  name: flags
  namespace: kube-system`))).To(Succeed())
				_, err := kubectl(
					"label",
					"namespace",
					namespaceTemplateSetsNamespace,
					classLabel(namespaceClassTemplateSetsNamespace),
				)
				Expect(err).NotTo(HaveOccurred())

				Eventually(func(g Gomega) {
					g.Expect(getTemplatesValidReason(namespaceClassTemplateSetsNamespace)).To(Equal("InvalidTemplate"))
				}).Should(Succeed())
				Expect(getManagedConfigMaps(namespaceTemplateSetsNamespace)).
					To(BeEmpty(), "nothing is created from a bad template")
			})

			It("reports a config map the API server rejects", func() {
				Expect(applyManifest(createNamespaceClass(namespaceClassInvalidDataKey, `apiVersion: v1
kind: ConfigMap
metadata:
  name: bad-keys
data:
  "bad key!": "foobar"`))).To(Succeed())
				_, err := kubectl("label", "namespace", namespaceInvalidDataKey, classLabel(namespaceClassInvalidDataKey))
				Expect(err).NotTo(HaveOccurred())

				Eventually(func(g Gomega) {
					g.Expect(getTemplatesValidReason(namespaceClassInvalidDataKey)).To(Equal("InvalidTemplate"))
				}).Should(Succeed())

				_, err = kubectl("get", "configmap", "bad-keys", "-n", namespaceInvalidDataKey)
				Expect(err).To(HaveOccurred(), "rejected config map should not exist")
			})

			It("rejects a custom resource with a kind other than config map", func() {
				err := applyManifest(createNamespaceClass(namespaceClassSecretTemplate, `apiVersion: v1
kind: Secret
metadata:
  name: db-password
stringData:
  password: changeme`))
				Expect(err).To(HaveOccurred(), "the CRD should reject a Secret template")
				Expect(err.Error()).To(ContainSubstring("only v1 ConfigMap is allowed"))

				_, err = kubectl("get", "namespaceclass", namespaceClassSecretTemplate)
				Expect(err).To(HaveOccurred(), "the custom resource should not be stored")
			})
		})
	})
})

// getManagedConfigMaps returns the config maps in the
// given namespace that have the managed by label
func getManagedConfigMaps(namespace string) ([]string, error) {
	out, err := kubectl(
		"get",
		"configmap",
		"-n",
		namespace,
		"-l",
		"namespaceclass.lovebhardwaj.io/managed-by",
		"-o",
		`jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`,
	)
	if err != nil {
		return nil, err
	}
	return utils.GetNonEmptyLines(out), nil
}

// getConfigMapField returns a json path field of the
// config map with the given name in the provided namespace.
func getConfigMapField(namespace string, configMapName string, jsonPath string) (string, error) {
	return kubectl("get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath="+jsonPath)
}

// classLabel provides the namespace label for the namespace class.
func classLabel(name string) string {
	return "namespaceclass.lovebhardwaj.io/name=" + name
}

// kubectl runs the kubectl commands.
func kubectl(args ...string) (string, error) {
	return utils.Run(exec.Command("kubectl", args...))
}

// applyManifest applies the provided k8s manifest.
func applyManifest(manifest string) error {
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	_, err := utils.Run(cmd)
	return err
}

// createConfigMap creates a test config map template.
func createConfigMap(name string, logLevel string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
data:
  LOG_LEVEL: %s`, name, logLevel)
}

// createNamespaceClass creates the namespace class template.
func createNamespaceClass(name string, templates ...string) string {
	var resources strings.Builder
	for _, tmpl := range templates {
		resources.WriteString("\n  - template:")
		for line := range strings.SplitSeq(strings.TrimSpace(tmpl), "\n") {
			resources.WriteString("\n      ")
			resources.WriteString(line)
		}
	}
	return fmt.Sprintf(`apiVersion: namespaceclass.lovebhardwaj.io/v1alpha1
kind: NamespaceClass
metadata:
  name: %s
spec:
  resources:%s
`, name, resources.String())
}

// getTemplatesValidReason returns the reason of the TemplatesValid condition of the namespace class name.
func getTemplatesValidReason(name string) (string, error) {
	return kubectl("get", "namespaceclass", name, "-o",
		`jsonpath={.status.conditions[?(@.type=="TemplatesValid")].reason}`)
}

// withPruningOnDelete sets spec.pruningOnDelete to true in a manifest from createNamespaceClass.
func withPruningOnDelete(manifest string) string {
	return strings.Replace(manifest, "spec:\n", "spec:\n  pruningOnDelete: true\n", 1)
}

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	By("creating temporary file to store the token request")
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		By("executing kubectl command to create the token")
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		By("parsing the JSON output to extract the token")
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() (string, error) {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	return utils.Run(cmd)
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
