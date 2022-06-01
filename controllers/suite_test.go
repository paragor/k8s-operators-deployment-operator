/*
Copyright 2022 paragor.

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

package controllers

import (
	"context"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"go.uber.org/atomic"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	vpav1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"path/filepath"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"strconv"
	"testing"
	"time"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	ns := SetupTest(context.Background())
	Context("Deployment vpa", func() {
		It("should create vpa for deployment", func() {
			ctx := context.Background()
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: ns.Name,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"a": "a",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"a": "a",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "test",
									Image: "alpine:latest",
									Args:  []string{"sleep", "infinity"},
								},
							},
						},
					},
					Strategy: appsv1.DeploymentStrategy{},
				},
				Status: appsv1.DeploymentStatus{},
			}
			err := k8sClient.Create(ctx, deployment)
			Expect(err).NotTo(HaveOccurred())

			timeout, cancel := context.WithTimeout(ctx, time.Second*5)
			defer cancel()
			vpa := vpav1.VerticalPodAutoscaler{}
			err = wait.PollImmediateInfiniteWithContext(timeout, time.Millisecond*100, func(ctx context.Context) (done bool, err error) {
				if err = k8sClient.Get(
					ctx,
					client.ObjectKey{Name: deployment.Name + "-deployment", Namespace: ns.Name},
					&vpa,
				); err != nil {
					if apierrors.IsNotFound(err) {
						return false, nil
					}
					return false, err
				}

				return true, nil
			})
			Expect(err).NotTo(HaveOccurred(), "cant get found vpa")

			Expect(vpa.Spec.TargetRef.Name).To(BeEquivalentTo(deployment.Name))
			Expect(vpa.Spec.ResourcePolicy.ContainerPolicies[0].ContainerName).To(BeEquivalentTo("*"))
			updateModeInitial := vpav1.UpdateModeInitial
			Expect(vpa.Spec.UpdatePolicy.UpdateMode).To(BeEquivalentTo(&updateModeInitial))
		})

	})
	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: false,
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	Expect(appsv1.AddToScheme(scheme.Scheme)).NotTo(HaveOccurred())
	Expect(vpav1.AddToScheme(scheme.Scheme)).NotTo(HaveOccurred())

	glob, err := filepath.Glob(filepath.Join(".", "testdata", "crds", "*.yaml"))
	Expect(err).NotTo(HaveOccurred())
	_, err = envtest.InstallCRDs(cfg, envtest.CRDInstallOptions{
		Paths: glob,
	})
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

var testNum atomic.Int64

// SetupTest will set up a testing environment.
// This includes:
// * creating a Namespace to be used during the test
// * starting the 'MyKindReconciler'
// * stopping the 'MyKindReconciler" after the test ends
// Call this function at the start of each of your tests.
func SetupTest(ctx context.Context) *corev1.Namespace {
	cancelingCtx, cancel := context.WithCancel(ctx)
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-" + rand.SafeEncodeString(rand.String(10)) + "-" + strconv.Itoa(int(testNum.Load())),
		},
	}

	BeforeEach(func() {
		testNum.Add(1)
		err := k8sClient.Create(ctx, ns)
		Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{})
		Expect(err).NotTo(HaveOccurred(), "failed to create manager")

		controller, err := NewGeneralReconciler(
			mgr.GetClient(),
			testEnv.Scheme,
			func() client.Object { return &appsv1.Deployment{} },
			UpdateModeInitial,
		)
		Expect(err).NotTo(HaveOccurred())
		err = controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		go func() {
			err := mgr.Start(cancelingCtx)
			Expect(err).NotTo(HaveOccurred(), "failed to start manager")
		}()
	})

	AfterEach(func() {
		cancel()
		err := k8sClient.Delete(ctx, ns)
		Expect(err).NotTo(HaveOccurred(), "failed to delete test namespace")
	})

	return ns
}
