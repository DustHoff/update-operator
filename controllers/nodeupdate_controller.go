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
	"strconv"
	"strings"
	"time"

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

// NodeUpdateReconciler is fully responsible for the entire OS update lifecycle of a single node:
// it creates an OS-specific update pod (which copies apt repositories to the host and runs
// apt upgrade), watches the pod to completion, schedules the node reboot, and tracks state.
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

func (r *NodeUpdateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	nodeUpdate := &updatemanagerv1alpha1.NodeUpdate{}
	err := r.Get(ctx, req.NamespacedName, nodeUpdate)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("NodeUpdate resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get NodeUpdate")
		return ctrl.Result{}, err
	}

	if len(nodeUpdate.Status.Conditions) == 0 {
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

	execution, hasExecution := nodeUpdate.Labels[LabelExecution]
	if !hasExecution || execution == "" {
		return ctrl.Result{}, nil
	}

	podName := nodeUpdate.Name + "-" + execution
	pod := &corev1.Pod{}
	err = r.Get(ctx, types.NamespacedName{Name: podName, Namespace: nodeUpdate.Namespace}, pod)

	if apierrors.IsNotFound(err) {
		trigger, hasTrigger := nodeUpdate.Annotations[AnnotationExecute]
		if hasTrigger && trigger == TriggerNodeUpdate && nodeUpdate.Spec.Image != "" {
			log.Info("creating node update pod", "node", nodeUpdate.Name, "execution", execution)
			newPod, buildErr := r.createNodeUpdatePod(nodeUpdate)
			if buildErr != nil {
				log.Error(buildErr, "failed to build node update pod spec")
				return ctrl.Result{}, buildErr
			}
			if createErr := r.Create(ctx, newPod); createErr != nil {
				log.Error(createErr, "failed to create node update pod")
				return ctrl.Result{}, createErr
			}
			if nodeUpdate.Annotations == nil {
				nodeUpdate.Annotations = make(map[string]string)
			}
			delete(nodeUpdate.Annotations, AnnotationExecute)
			if updateErr := r.Update(ctx, nodeUpdate); updateErr != nil {
				return ctrl.Result{}, updateErr
			}
		}
		return ctrl.Result{}, nil
	}
	if err != nil {
		log.Error(err, "failed to get node update pod")
		return ctrl.Result{}, err
	}

	// Pod exists — handle its phase to drive the update lifecycle forward.
	switch pod.Status.Phase {
	case corev1.PodSucceeded:
		if _, hasReboot := nodeUpdate.Annotations[AnnotationReboot]; !hasReboot {
			log.Info("node update pod succeeded, scheduling reboot", "node", nodeUpdate.Name)
			if schedErr := r.scheduleNodeRestart(ctx, nodeUpdate); schedErr != nil {
				return ctrl.Result{}, schedErr
			}
			if nodeUpdate.Labels == nil {
				nodeUpdate.Labels = make(map[string]string)
			}
			nodeUpdate.Labels[LabelState] = StateSucceeded
			if updateErr := r.Update(ctx, nodeUpdate); updateErr != nil {
				return ctrl.Result{}, updateErr
			}
			meta.SetStatusCondition(&nodeUpdate.Status.Conditions, metav1.Condition{
				Type: typeProcessing, Status: metav1.ConditionTrue,
				Reason: "reboot", Message: "Reboot scheduled",
			})
			if statusErr := r.Status().Update(ctx, nodeUpdate); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
		}

	case corev1.PodFailed:
		if nodeUpdate.Labels == nil {
			nodeUpdate.Labels = make(map[string]string)
		}
		nodeUpdate.Labels[LabelState] = StateFailed
		if updateErr := r.Update(ctx, nodeUpdate); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		meta.SetStatusCondition(&nodeUpdate.Status.Conditions, metav1.Condition{
			Type: typeFailed, Status: metav1.ConditionTrue,
			Reason: "update", Message: "node update failed",
		})
		if statusErr := r.Status().Update(ctx, nodeUpdate); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
// Owns(&corev1.Pod{}) ensures the controller is re-triggered whenever an owned update pod
// changes phase, allowing it to react to pod completion without a separate PodReconciler.
func (r *NodeUpdateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&updatemanagerv1alpha1.NodeUpdate{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}

// scheduleNodeRestart taints the node to drain workloads and annotates both the node and the
// NodeUpdate resource to signal that a reboot is pending.  The NodeReconciler later detects
// when the node comes back healthy and marks the reboot as done.
func (r *NodeUpdateReconciler) scheduleNodeRestart(ctx context.Context, update *updatemanagerv1alpha1.NodeUpdate) error {
	node := &corev1.Node{}
	if err := r.Get(ctx, types.NamespacedName{Name: update.Name}, node); err != nil {
		return err
	}

	node.Spec.Unschedulable = true
	node.Spec.Taints = []corev1.Taint{
		{Key: "node.kubernetes.io/unschedulable", Value: "NoSchedule", Effect: corev1.TaintEffectNoExecute},
	}
	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}
	node.Annotations[AnnotationReboot] = strconv.FormatInt(time.Now().UnixNano(), 10)

	if update.Annotations == nil {
		update.Annotations = make(map[string]string)
	}
	update.Annotations[AnnotationReboot] = ""

	if err := r.Update(ctx, node); err != nil {
		log.FromContext(ctx).Error(err, "failed to taint node for reboot", "node", node.Name)
		return err
	}
	return nil
}

// createNodeUpdatePod builds the Pod spec for running an OS-level update on the target node.
// The pod image is OS-specific (e.g. ubuntu-22-04-patch) and is expected to:
//  1. Copy apt repository lists from the image into /host/etc/apt/sources.list.d/
//  2. Copy apt keyrings into /host/etc/apt/trusted.gpg.d/
//  3. Run apt-get update/upgrade inside the host namespace
//
// Package hold-back and selective install are controlled via HOLDPKG / INSTALLPKG env vars.
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
			Name:      update.Name + "-" + update.Labels[LabelExecution],
			Namespace: update.Namespace,
			Labels: map[string]string{
				LabelExecution: update.Labels[LabelExecution],
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
				{Key: "node.kubernetes.io/unschedulable", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoExecute},
				{Key: "node.kubernetes.io/unschedulable", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
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
				// The update container requires elevated privileges to perform OS-level package
				// management and host filesystem access. This is intentional and documented.
				SecurityContext: &corev1.SecurityContext{
					RunAsNonRoot:             &[]bool{false}[0],
					RunAsUser:                &[]int64{0}[0],
					AllowPrivilegeEscalation: &[]bool{true}[0],
					Capabilities: &corev1.Capabilities{
						Add: []corev1.Capability{"ALL"},
					},
				},
				Ports:   []corev1.ContainerPort{},
				Command: []string{},
				VolumeMounts: []corev1.VolumeMount{
					{Name: "host", MountPath: "/host"},
				},
			}},
		},
	}

	if err := ctrl.SetControllerReference(update, pod, r.Scheme); err != nil {
		return nil, err
	}
	return pod, nil
}
