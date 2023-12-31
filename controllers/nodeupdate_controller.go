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
	"fmt"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strings"

	updatemanagerv1alpha1 "github.com/DustHoff/update-operator/api/v1alpha1"
)

const (
	// typeFailed represents the status of a failed node update
	typeFailed = "Failed"
	// typeProcessing represents the status of progressing updates of the given Node
	typeProcessing = "Processing"
	// typeWaiting represents the status of a managed state of the update process
	typeWaiting = "Waiting"
	// typeStopped represents the status where the managed state has been disabled for the given Node
	typeStopped = "Stopped"
	finalizer   = "updatemanager.onesi.de/finalizer"
)

// NodeUpdateReconciler reconciles a NodeUpdate object
type NodeUpdateReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=updatemanager.onesi.de,resources=nodeupdates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=updatemanager.onesi.de,resources=nodeupdates/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=updatemanager.onesi.de,resources=nodeupdates/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=pods/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=pods/log,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the NodeUpdate object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *NodeUpdateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	nodeUpdate := &updatemanagerv1alpha1.NodeUpdate{}
	err := r.Get(ctx, req.NamespacedName, nodeUpdate)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then, it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			log.Info("NodeUpdate resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get NodeUpdate")
		return ctrl.Result{}, err
	}
	if nodeUpdate.Status.Conditions == nil || len(nodeUpdate.Status.Conditions) == 0 {
		meta.SetStatusCondition(&nodeUpdate.Status.Conditions, metav1.Condition{Type: typeProcessing, Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
		if err = r.Status().Update(ctx, nodeUpdate); err != nil {
			log.Error(err, "Failed to update node update status")
			return ctrl.Result{}, err
		}

		if err := r.Get(ctx, req.NamespacedName, nodeUpdate); err != nil {
			log.Error(err, "Failed to re-fetch node update")
			return ctrl.Result{}, err
		}
	}
	// Let's add a finalizer. Then, we can define some operations which should
	// occurs before the custom resource to be deleted.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers
	if !controllerutil.ContainsFinalizer(nodeUpdate, finalizer) {
		log.Info("Adding Finalizer for node Update")
		if ok := controllerutil.AddFinalizer(nodeUpdate, finalizer); !ok {
			log.Error(err, "Failed to add finalizer into the custom resource")
			return ctrl.Result{Requeue: true}, nil
		}

		if err = r.Update(ctx, nodeUpdate); err != nil {
			log.Error(err, "Failed to update custom resource to add finalizer")
			return ctrl.Result{}, err
		}
	}
	// Check if the nodeUpdate instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	if nodeUpdate.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(nodeUpdate, finalizer) {
			log.Info("Performing Finalizer Operations for node update before delete CR")

			meta.SetStatusCondition(&nodeUpdate.Status.Conditions, metav1.Condition{Type: typeStopped,
				Status: metav1.ConditionUnknown, Reason: "Finalizing",
				Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", nodeUpdate.Name)})

			if err := r.Status().Update(ctx, nodeUpdate); err != nil {
				log.Error(err, "Failed to update node update status")
				return ctrl.Result{}, err
			}

			if err := r.Get(ctx, req.NamespacedName, nodeUpdate); err != nil {
				log.Error(err, "Failed to re-fetch node update")
				return ctrl.Result{}, err
			}
			meta.SetStatusCondition(&nodeUpdate.Status.Conditions, metav1.Condition{Type: typeStopped,
				Status: metav1.ConditionTrue, Reason: "Finalizing",
				Message: fmt.Sprintf("Finalizer operations for custom resource %s name were successfully accomplished", nodeUpdate.Name)})

			if err := r.Status().Update(ctx, nodeUpdate); err != nil {
				log.Error(err, "Failed to update status")
				return ctrl.Result{}, err
			}

			log.Info("Removing Finalizer after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(nodeUpdate, finalizer); !ok {
				log.Error(err, "Failed to remove finalizer")
				return ctrl.Result{Requeue: true}, nil
			}

			if err := r.Update(ctx, nodeUpdate); err != nil {
				log.Error(err, "Failed to remove finalizer")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	execution, ok := nodeUpdate.Labels["updatemanager.onesi.de/execution"]
	trigger, triggerOk := nodeUpdate.Annotations["updatemanager.onesi.de/execute"]
	if ok {
		// found execution label
		pod := &corev1.Pod{}
		err = r.Get(ctx, types.NamespacedName{Name: nodeUpdate.Name + "-" + execution, Namespace: nodeUpdate.Namespace}, pod)
		if err != nil && apierrors.IsNotFound(err) {
			if triggerOk && trigger == "nodeUpdate" && nodeUpdate.Spec.Image != "" {
				log.Info("create node update pod")
				pod, _ := r.createNodeUpdatePod(nodeUpdate)
				if err := r.Create(ctx, pod); err != nil {
					log.Error(err, "failed to create patch")
				}
				delete(nodeUpdate.Annotations, "updatemanager.onesi.de/execute")
				if err := r.Update(ctx, nodeUpdate); err != nil {
					return ctrl.Result{}, err
				}
			}
			return ctrl.Result{}, nil
		}
	}
	//https://stackoverflow.com/questions/53852530/how-to-get-logs-from-kubernetes-using-go
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeUpdateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&updatemanagerv1alpha1.NodeUpdate{}).
		Complete(r)
}

func (r *NodeUpdateReconciler) createNodeUpdatePod(update *updatemanagerv1alpha1.NodeUpdate) (*corev1.Pod, error) {
	volumeType := corev1.HostPathDirectory
	hold := update.Spec.Packages.Hold
	install := update.Spec.Packages.Install

	if hold == nil {
		hold = []string{}
	}
	if install == nil {
		install = []string{}
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      update.Name + "-" + update.Labels["updatemanager.onesi.de/execution"],
			Namespace: update.Namespace,
			Labels: map[string]string{
				"updatemanager.onesi.de/execution": update.Labels["updatemanager.onesi.de/execution"],
			},
			Annotations: map[string]string{
				"container.apparmor.security.beta.kubernetes.io/update": "unconfined",
			},
		},
		Spec: corev1.PodSpec{
			HostPID:     true,
			HostNetwork: true,
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": update.Name,
			},
			Tolerations: []corev1.Toleration{
				corev1.Toleration{Key: "node.kubernetes.io/unschedulable", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoExecute},
				corev1.Toleration{Key: "node.kubernetes.io/unschedulable", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
			},
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: &[]bool{false}[0],
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeUnconfined,
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
			Volumes: []corev1.Volume{
				{Name: "host", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/", Type: &volumeType}}},
			},
			Containers: []corev1.Container{{
				Image: update.Spec.Image,
				Env: []corev1.EnvVar{
					{Name: "HOLDPKG", Value: strings.Join(hold, " ")},
					{Name: "INSTALLPKG", Value: strings.Join(install, " ")},
				},
				Name:            "update",
				ImagePullPolicy: corev1.PullAlways,
				SecurityContext: &corev1.SecurityContext{
					RunAsNonRoot:             &[]bool{false}[0],
					RunAsUser:                &[]int64{0}[0],
					AllowPrivilegeEscalation: &[]bool{true}[0],
					Capabilities: &corev1.Capabilities{
						Add: []corev1.Capability{
							"All",
						},
					},
				},
				Ports:   []corev1.ContainerPort{},
				Command: []string{},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "host",
						MountPath: "/host",
					},
				},
			}},
		},
	}

	if err := ctrl.SetControllerReference(update, pod, r.Scheme); err != nil {
		return nil, err
	}
	return pod, nil
}
