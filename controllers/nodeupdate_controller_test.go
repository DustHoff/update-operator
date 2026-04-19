/*
Copyright 2023.

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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	updatemanagerv1alpha1 "github.com/DustHoff/update-operator/api/v1alpha1"
)

const (
	testNodeName      = "worker-1"
	testNamespace     = "default"
	testImage         = "registry.example.com/ubuntu-22-04-patch:latest"
	testExecID        = "999"
)

func makeNodeUpdate(name, image, execution string) *updatemanagerv1alpha1.NodeUpdate {
	nu := &updatemanagerv1alpha1.NodeUpdate{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       testNamespace,
			ResourceVersion: "1",
			Labels:          map[string]string{},
			Annotations:     map[string]string{},
		},
		Spec: updatemanagerv1alpha1.NodeUpdateSpec{
			Image:    image,
			Priority: 1,
			Packages: updatemanagerv1alpha1.NodeUpdatePackages{
				Hold:    []string{"kubeadm", "kubelet"},
				Install: []string{},
			},
		},
	}
	if execution != "" {
		nu.Labels[LabelExecution] = execution
	}
	return nu
}

func makeNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			ResourceVersion: "1",
			Labels:          map[string]string{},
			Annotations:     map[string]string{},
		},
	}
}

func makePod(name, namespace, execution string, phase corev1.PodPhase, ownerNU *updatemanagerv1alpha1.NodeUpdate) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			ResourceVersion: "1",
			Labels: map[string]string{
				LabelExecution: execution,
			},
		},
		Status: corev1.PodStatus{Phase: phase},
	}
	if ownerNU != nil {
		pod.OwnerReferences = []metav1.OwnerReference{
			{
				APIVersion: "updatemanager.onesi.de/v1alpha1",
				Kind:       "NodeUpdate",
				Name:       ownerNU.Name,
				UID:        ownerNU.UID,
			},
		}
	}
	return pod
}

var _ = Describe("NodeUpdateController", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("Pod creation", func() {
		It("creates an update pod when execution label and trigger annotation are set", func() {
			nu := makeNodeUpdate(testNodeName, testImage, testExecID)
			nu.Annotations[AnnotationExecute] = TriggerNodeUpdate

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(nu).
				WithStatusSubresource(nu).
				Build()

			r := &NodeUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: testNodeName, Namespace: testNamespace}})
			Expect(err).NotTo(HaveOccurred())

			podList := &corev1.PodList{}
			Expect(fakeClient.List(ctx, podList)).To(Succeed())
			Expect(podList.Items).To(HaveLen(1))

			pod := podList.Items[0]
			Expect(pod.Name).To(Equal(testNodeName + "-" + testExecID))
			Expect(pod.Spec.NodeSelector["kubernetes.io/hostname"]).To(Equal(testNodeName))
			Expect(pod.Spec.Containers).To(HaveLen(1))
			Expect(pod.Spec.Containers[0].Image).To(Equal(testImage))
		})

		It("passes HOLDPKG env var to the update container", func() {
			nu := makeNodeUpdate(testNodeName, testImage, testExecID)
			nu.Annotations[AnnotationExecute] = TriggerNodeUpdate
			nu.Spec.Packages.Hold = []string{"kubeadm", "kubelet", "kubectl"}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(nu).
				WithStatusSubresource(nu).
				Build()

			r := &NodeUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: testNodeName, Namespace: testNamespace}})
			Expect(err).NotTo(HaveOccurred())

			podList := &corev1.PodList{}
			Expect(fakeClient.List(ctx, podList)).To(Succeed())
			Expect(podList.Items).To(HaveLen(1))

			envVars := podList.Items[0].Spec.Containers[0].Env
			var holdVal string
			for _, e := range envVars {
				if e.Name == "HOLDPKG" {
					holdVal = e.Value
				}
			}
			Expect(holdVal).To(Equal("kubeadm kubelet kubectl"))
		})

		It("does not create a pod when execution label is missing", func() {
			nu := makeNodeUpdate(testNodeName, testImage, "")
			nu.Annotations[AnnotationExecute] = TriggerNodeUpdate

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(nu).
				WithStatusSubresource(nu).
				Build()

			r := &NodeUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: testNodeName, Namespace: testNamespace}})
			Expect(err).NotTo(HaveOccurred())

			podList := &corev1.PodList{}
			Expect(fakeClient.List(ctx, podList)).To(Succeed())
			Expect(podList.Items).To(BeEmpty())
		})

		It("does not create a pod when trigger annotation is absent", func() {
			nu := makeNodeUpdate(testNodeName, testImage, testExecID)
			// No AnnotationExecute set

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(nu).
				WithStatusSubresource(nu).
				Build()

			r := &NodeUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: testNodeName, Namespace: testNamespace}})
			Expect(err).NotTo(HaveOccurred())

			podList := &corev1.PodList{}
			Expect(fakeClient.List(ctx, podList)).To(Succeed())
			Expect(podList.Items).To(BeEmpty())
		})

		It("does not create a pod when image is empty", func() {
			nu := makeNodeUpdate(testNodeName, "", testExecID)
			nu.Annotations[AnnotationExecute] = TriggerNodeUpdate

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(nu).
				WithStatusSubresource(nu).
				Build()

			r := &NodeUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: testNodeName, Namespace: testNamespace}})
			Expect(err).NotTo(HaveOccurred())

			podList := &corev1.PodList{}
			Expect(fakeClient.List(ctx, podList)).To(Succeed())
			Expect(podList.Items).To(BeEmpty())
		})

		It("removes the trigger annotation after creating the pod", func() {
			nu := makeNodeUpdate(testNodeName, testImage, testExecID)
			nu.Annotations[AnnotationExecute] = TriggerNodeUpdate

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(nu).
				WithStatusSubresource(nu).
				Build()

			r := &NodeUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: testNodeName, Namespace: testNamespace}})
			Expect(err).NotTo(HaveOccurred())

			fetched := &updatemanagerv1alpha1.NodeUpdate{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: testNodeName, Namespace: testNamespace}, fetched)).To(Succeed())
			Expect(fetched.Annotations[AnnotationExecute]).To(BeEmpty())
		})
	})

	Describe("Pod state handling", func() {
		It("schedules a reboot and sets state=Succeeded when the pod succeeds", func() {
			nu := makeNodeUpdate(testNodeName, testImage, testExecID)
			node := makeNode(testNodeName)
			pod := makePod(testNodeName+"-"+testExecID, testNamespace, testExecID, corev1.PodSucceeded, nu)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(nu, node, pod).
				WithStatusSubresource(nu).
				Build()

			r := &NodeUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: testNodeName, Namespace: testNamespace}})
			Expect(err).NotTo(HaveOccurred())

			fetchedNU := &updatemanagerv1alpha1.NodeUpdate{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: testNodeName, Namespace: testNamespace}, fetchedNU)).To(Succeed())
			Expect(fetchedNU.Labels[LabelState]).To(Equal(StateSucceeded))
			Expect(fetchedNU.Annotations[AnnotationReboot]).To(Equal(""), "reboot annotation should be set (empty string = pending)")

			fetchedNode := &corev1.Node{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: testNodeName}, fetchedNode)).To(Succeed())
			Expect(fetchedNode.Spec.Unschedulable).To(BeTrue())
			Expect(fetchedNode.Annotations[AnnotationReboot]).NotTo(BeEmpty(), "node should have reboot timestamp annotation")
		})

		It("does not schedule a duplicate reboot if annotation is already set", func() {
			nu := makeNodeUpdate(testNodeName, testImage, testExecID)
			nu.Annotations[AnnotationReboot] = "" // already set
			node := makeNode(testNodeName)
			pod := makePod(testNodeName+"-"+testExecID, testNamespace, testExecID, corev1.PodSucceeded, nu)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(nu, node, pod).
				WithStatusSubresource(nu).
				Build()

			r := &NodeUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: testNodeName, Namespace: testNamespace}})
			Expect(err).NotTo(HaveOccurred())

			// Node should NOT have been tainted again
			fetchedNode := &corev1.Node{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: testNodeName}, fetchedNode)).To(Succeed())
			Expect(fetchedNode.Spec.Unschedulable).To(BeFalse(), "node should not be tainted again")
		})

		It("sets state=Failed when the pod fails", func() {
			nu := makeNodeUpdate(testNodeName, testImage, testExecID)
			node := makeNode(testNodeName)
			pod := makePod(testNodeName+"-"+testExecID, testNamespace, testExecID, corev1.PodFailed, nu)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(nu, node, pod).
				WithStatusSubresource(nu).
				Build()

			r := &NodeUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: testNodeName, Namespace: testNamespace}})
			Expect(err).NotTo(HaveOccurred())

			fetched := &updatemanagerv1alpha1.NodeUpdate{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: testNodeName, Namespace: testNamespace}, fetched)).To(Succeed())
			Expect(fetched.Labels[LabelState]).To(Equal(StateFailed))
		})

		It("does nothing while the pod is still running", func() {
			nu := makeNodeUpdate(testNodeName, testImage, testExecID)
			node := makeNode(testNodeName)
			pod := makePod(testNodeName+"-"+testExecID, testNamespace, testExecID, corev1.PodRunning, nu)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(nu, node, pod).
				WithStatusSubresource(nu).
				Build()

			r := &NodeUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: testNodeName, Namespace: testNamespace}})
			Expect(err).NotTo(HaveOccurred())

			fetched := &updatemanagerv1alpha1.NodeUpdate{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: testNodeName, Namespace: testNamespace}, fetched)).To(Succeed())
			Expect(fetched.Labels[LabelState]).To(BeEmpty(), "state should not change while pod is running")
		})
	})

	Describe("HOLDPKG / INSTALLPKG environment variables", func() {
		It("sets empty HOLDPKG when no packages are held", func() {
			nu := makeNodeUpdate(testNodeName, testImage, testExecID)
			nu.Annotations[AnnotationExecute] = TriggerNodeUpdate
			nu.Spec.Packages.Hold = nil

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(nu).
				WithStatusSubresource(nu).
				Build()

			r := &NodeUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: testNodeName, Namespace: testNamespace}})
			Expect(err).NotTo(HaveOccurred())

			podList := &corev1.PodList{}
			Expect(fakeClient.List(ctx, podList)).To(Succeed())
			Expect(podList.Items).To(HaveLen(1))

			for _, e := range podList.Items[0].Spec.Containers[0].Env {
				if e.Name == "HOLDPKG" {
					Expect(e.Value).To(BeEmpty())
				}
			}
		})

		It("passes INSTALLPKG when specific packages are requested", func() {
			nu := makeNodeUpdate(testNodeName, testImage, testExecID)
			nu.Annotations[AnnotationExecute] = TriggerNodeUpdate
			nu.Spec.Packages.Install = []string{"curl", "wget"}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(nu).
				WithStatusSubresource(nu).
				Build()

			r := &NodeUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: testNodeName, Namespace: testNamespace}})
			Expect(err).NotTo(HaveOccurred())

			podList := &corev1.PodList{}
			Expect(fakeClient.List(ctx, podList)).To(Succeed())
			Expect(podList.Items).To(HaveLen(1))

			var installVal string
			for _, e := range podList.Items[0].Spec.Containers[0].Env {
				if e.Name == "INSTALLPKG" {
					installVal = e.Value
				}
			}
			Expect(installVal).To(Equal("curl wget"))
		})
	})
})
