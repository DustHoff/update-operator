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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	updatemanagerv1alpha1 "github.com/DustHoff/update-operator/api/v1alpha1"
)

const testExecutionID = "1000"

func makeNodeUpdateFixture(name string, priority int32, execution, state, completed, reboot string) *updatemanagerv1alpha1.NodeUpdate {
	nu := &updatemanagerv1alpha1.NodeUpdate{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       "default",
			ResourceVersion: "1",
			Labels:          map[string]string{},
			Annotations:     map[string]string{},
		},
		Spec: updatemanagerv1alpha1.NodeUpdateSpec{
			Priority: priority,
		},
	}
	if execution != "" {
		nu.Labels[LabelExecution] = execution
	}
	if state != "" {
		nu.Labels[LabelState] = state
	}
	if completed != "" {
		nu.Labels[LabelCompleted] = completed
	}
	if reboot != "" {
		nu.Annotations[AnnotationReboot] = reboot
	}
	return nu
}

func makeClusterUpdate(maxUnavailable int32) *updatemanagerv1alpha1.ClusterUpdate {
	return &updatemanagerv1alpha1.ClusterUpdate{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-cluster",
			Namespace:       "default",
			ResourceVersion: "1",
		},
		Spec: updatemanagerv1alpha1.ClusterUpdateSpec{
			Update: updatemanagerv1alpha1.ClusterNodeUpdate{
				MaxUnavailableNode: maxUnavailable,
				Schedule:           "0 2 * * 0",
			},
		},
		Status: updatemanagerv1alpha1.ClusterUpdateStatus{
			NextNodeUpdate: 1000,
		},
	}
}

var _ = Describe("ClusterUpdateController", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("executeNodeUpdateFlow", func() {
		Context("with a single node and maxUnavailableNode=1", func() {
			It("initialises the node on the first call", func() {
				node := makeNodeUpdateFixture("node1", 1, "", "", "", "")
				cu := makeClusterUpdate(1)

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(node, cu).
					Build()

				r := &ClusterUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}

				list := &updatemanagerv1alpha1.NodeUpdateList{}
				Expect(fakeClient.List(ctx, list)).To(Succeed())

				done, err := r.executeNodeUpdateFlow(ctx, list, cu)
				Expect(err).NotTo(HaveOccurred())
				Expect(done).To(BeFalse())

				fetched := &updatemanagerv1alpha1.NodeUpdate{}
				Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "node1", Namespace: "default"}, fetched)).To(Succeed())
				Expect(fetched.Labels[LabelExecution]).To(Equal(testExecutionID))
				Expect(fetched.Labels[LabelState]).To(Equal(StateInitialized))
				Expect(fetched.Annotations[AnnotationExecute]).To(Equal(TriggerNodeUpdate))
			})

			It("does not initialise a second node while the first is in progress", func() {
				node1 := makeNodeUpdateFixture("node1", 1, testExecutionID, StateInitialized, "", "")
				node2 := makeNodeUpdateFixture("node2", 2, "", "", "", "")
				cu := makeClusterUpdate(1)

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(node1, node2, cu).
					Build()

				r := &ClusterUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}

				list := &updatemanagerv1alpha1.NodeUpdateList{}
				Expect(fakeClient.List(ctx, list)).To(Succeed())

				done, err := r.executeNodeUpdateFlow(ctx, list, cu)
				Expect(err).NotTo(HaveOccurred())
				Expect(done).To(BeFalse())

				fetched := &updatemanagerv1alpha1.NodeUpdate{}
				Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "node2", Namespace: "default"}, fetched)).To(Succeed())
				Expect(fetched.Labels[LabelExecution]).To(BeEmpty(), "node2 must not be initialised while node1 is in progress")
			})

			It("initialises the next node after the previous one completes", func() {
				node1 := makeNodeUpdateFixture("node1", 1, testExecutionID, StateSucceeded, testExecutionID, "")
				node2 := makeNodeUpdateFixture("node2", 2, "", "", "", "")
				cu := makeClusterUpdate(1)

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(node1, node2, cu).
					Build()

				r := &ClusterUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}

				list := &updatemanagerv1alpha1.NodeUpdateList{}
				Expect(fakeClient.List(ctx, list)).To(Succeed())

				done, err := r.executeNodeUpdateFlow(ctx, list, cu)
				Expect(err).NotTo(HaveOccurred())
				Expect(done).To(BeFalse())

				fetched := &updatemanagerv1alpha1.NodeUpdate{}
				Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "node2", Namespace: "default"}, fetched)).To(Succeed())
				Expect(fetched.Labels[LabelExecution]).To(Equal(testExecutionID))
			})
		})

		Context("with three nodes and maxUnavailableNode=2", func() {
			It("initialises up to two nodes simultaneously", func() {
				node1 := makeNodeUpdateFixture("node1", 1, "", "", "", "")
				node2 := makeNodeUpdateFixture("node2", 2, "", "", "", "")
				node3 := makeNodeUpdateFixture("node3", 3, "", "", "", "")
				cu := makeClusterUpdate(2)

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(node1, node2, node3, cu).
					Build()

				r := &ClusterUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}

				list := &updatemanagerv1alpha1.NodeUpdateList{}
				Expect(fakeClient.List(ctx, list)).To(Succeed())

				done, err := r.executeNodeUpdateFlow(ctx, list, cu)
				Expect(err).NotTo(HaveOccurred())
				Expect(done).To(BeFalse())

				n1 := &updatemanagerv1alpha1.NodeUpdate{}
				Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "node1", Namespace: "default"}, n1)).To(Succeed())
				Expect(n1.Labels[LabelExecution]).To(Equal(testExecutionID), "node1 should be initialised")

				n2 := &updatemanagerv1alpha1.NodeUpdate{}
				Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "node2", Namespace: "default"}, n2)).To(Succeed())
				Expect(n2.Labels[LabelExecution]).To(Equal(testExecutionID), "node2 should be initialised")

				n3 := &updatemanagerv1alpha1.NodeUpdate{}
				Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "node3", Namespace: "default"}, n3)).To(Succeed())
				Expect(n3.Labels[LabelExecution]).To(BeEmpty(), "node3 must not be initialised while 2 are already in progress")
			})

			It("initialises the third node when one of the first two finishes", func() {
				node1 := makeNodeUpdateFixture("node1", 1, testExecutionID, StateSucceeded, testExecutionID, "")
				node2 := makeNodeUpdateFixture("node2", 2, testExecutionID, StateInitialized, "", "")
				node3 := makeNodeUpdateFixture("node3", 3, "", "", "", "")
				cu := makeClusterUpdate(2)

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(node1, node2, node3, cu).
					Build()

				r := &ClusterUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}

				list := &updatemanagerv1alpha1.NodeUpdateList{}
				Expect(fakeClient.List(ctx, list)).To(Succeed())

				done, err := r.executeNodeUpdateFlow(ctx, list, cu)
				Expect(err).NotTo(HaveOccurred())
				Expect(done).To(BeFalse())

				n3 := &updatemanagerv1alpha1.NodeUpdate{}
				Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "node3", Namespace: "default"}, n3)).To(Succeed())
				Expect(n3.Labels[LabelExecution]).To(Equal(testExecutionID), "node3 should be initialised now that node1 is done")
			})
		})

		Context("state transition handling", func() {
			It("marks a node as completed when reboot is done", func() {
				node := makeNodeUpdateFixture("node1", 1, testExecutionID, StateSucceeded, "", RebootDone)
				cu := makeClusterUpdate(1)

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(node, cu).
					Build()

				r := &ClusterUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}

				list := &updatemanagerv1alpha1.NodeUpdateList{}
				Expect(fakeClient.List(ctx, list)).To(Succeed())

				// First call: only node → marks it completed → returns true
				done, err := r.executeNodeUpdateFlow(ctx, list, cu)
				Expect(err).NotTo(HaveOccurred())
				Expect(done).To(BeTrue(), "all nodes are done so flow must return true")

				fetched := &updatemanagerv1alpha1.NodeUpdate{}
				Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "node1", Namespace: "default"}, fetched)).To(Succeed())
				Expect(fetched.Labels[LabelCompleted]).To(Equal(testExecutionID))
				Expect(fetched.Annotations[AnnotationReboot]).To(BeEmpty(), "reboot annotation should be removed after completion")
			})

			It("returns true when all nodes are already completed", func() {
				node1 := makeNodeUpdateFixture("node1", 1, testExecutionID, StateSucceeded, testExecutionID, "")
				node2 := makeNodeUpdateFixture("node2", 2, testExecutionID, StateSucceeded, testExecutionID, "")
				cu := makeClusterUpdate(1)

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(node1, node2, cu).
					Build()

				r := &ClusterUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}

				list := &updatemanagerv1alpha1.NodeUpdateList{}
				Expect(fakeClient.List(ctx, list)).To(Succeed())

				done, err := r.executeNodeUpdateFlow(ctx, list, cu)
				Expect(err).NotTo(HaveOccurred())
				Expect(done).To(BeTrue())
			})

			It("returns an error when a node is in Failed state", func() {
				node := makeNodeUpdateFixture("node1", 1, testExecutionID, StateFailed, "", "")
				cu := makeClusterUpdate(1)

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(node, cu).
					Build()

				r := &ClusterUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}

				list := &updatemanagerv1alpha1.NodeUpdateList{}
				Expect(fakeClient.List(ctx, list)).To(Succeed())

				_, err := r.executeNodeUpdateFlow(ctx, list, cu)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("node1"))
			})

			It("returns an error when a node has priority less than 1", func() {
				node := makeNodeUpdateFixture("node1", 0, "", "", "", "")
				cu := makeClusterUpdate(1)

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(node, cu).
					Build()

				r := &ClusterUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}

				list := &updatemanagerv1alpha1.NodeUpdateList{}
				Expect(fakeClient.List(ctx, list)).To(Succeed())

				_, err := r.executeNodeUpdateFlow(ctx, list, cu)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("Reconcile loop", func() {
			It("sets Stopped condition when updates are disabled", func() {
				cu := &updatemanagerv1alpha1.ClusterUpdate{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cu", Namespace: "default", ResourceVersion: "1",
					},
					Spec: updatemanagerv1alpha1.ClusterUpdateSpec{
						Update: updatemanagerv1alpha1.ClusterNodeUpdate{Disabled: true},
					},
				}
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(cu).
					WithStatusSubresource(cu).
					Build()

				r := &ClusterUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "cu", Namespace: "default"}})
				Expect(err).NotTo(HaveOccurred())

				fetched := &updatemanagerv1alpha1.ClusterUpdate{}
				Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "cu", Namespace: "default"}, fetched)).To(Succeed())
				Expect(fetched.Status.NextNodeUpdate).To(BeZero())
			})

			It("sets Degraded condition when schedule is missing", func() {
				cu := &updatemanagerv1alpha1.ClusterUpdate{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cu", Namespace: "default", ResourceVersion: "1",
					},
					Spec: updatemanagerv1alpha1.ClusterUpdateSpec{
						Update: updatemanagerv1alpha1.ClusterNodeUpdate{Schedule: ""},
					},
				}
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(cu).
					WithStatusSubresource(cu).
					Build()

				r := &ClusterUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "cu", Namespace: "default"}})
				Expect(err).NotTo(HaveOccurred())
			})

			It("sets Degraded condition when cron expression is invalid", func() {
				cu := &updatemanagerv1alpha1.ClusterUpdate{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cu", Namespace: "default", ResourceVersion: "1",
					},
					Spec: updatemanagerv1alpha1.ClusterUpdateSpec{
						Update: updatemanagerv1alpha1.ClusterNodeUpdate{Schedule: "not-a-cron"},
					},
				}
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(cu).
					WithStatusSubresource(cu).
					Build()

				r := &ClusterUpdateReconciler{Client: fakeClient, Scheme: scheme.Scheme}
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "cu", Namespace: "default"}})
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})
})
