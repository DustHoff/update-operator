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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	updatemanagerv1alpha1 "github.com/DustHoff/update-operator/api/v1alpha1"
)

const (
	// typeAvailable Memcached represents the status of the Deployment reconciliation
	typeAvailable = "Available"
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
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete

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
		meta.SetStatusCondition(&nodeUpdate.Status.Conditions, metav1.Condition{Type: typeAvailable, Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
		if err = r.Status().Update(ctx, nodeUpdate); err != nil {
			log.Error(err, "Failed to update Memcached status")
			return ctrl.Result{}, err
		}
		// Let's re-fetch the memcached Custom Resource after update the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raise the issue "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
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
			log.Info("Performing Finalizer Operations for Memcached before delete CR")

			meta.SetStatusCondition(&nodeUpdate.Status.Conditions, metav1.Condition{Type: typeStopped,
				Status: metav1.ConditionUnknown, Reason: "Finalizing",
				Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", nodeUpdate.Name)})

			if err := r.Status().Update(ctx, nodeUpdate); err != nil {
				log.Error(err, "Failed to update Memcached status")
				return ctrl.Result{}, err
			}

			if err := r.Get(ctx, req.NamespacedName, nodeUpdate); err != nil {
				log.Error(err, "Failed to re-fetch node update")
				return ctrl.Result{}, err
			}
			//TODO: cleanup update pods
			meta.SetStatusCondition(&nodeUpdate.Status.Conditions, metav1.Condition{Type: typeStopped,
				Status: metav1.ConditionTrue, Reason: "Finalizing",
				Message: fmt.Sprintf("Finalizer operations for custom resource %s name were successfully accomplished", nodeUpdate.Name)})

			if err := r.Status().Update(ctx, nodeUpdate); err != nil {
				log.Error(err, "Failed to update Memcached status")
				return ctrl.Result{}, err
			}

			log.Info("Removing Finalizer for Memcached after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(nodeUpdate, finalizer); !ok {
				log.Error(err, "Failed to remove finalizer for Memcached")
				return ctrl.Result{Requeue: true}, nil
			}

			if err := r.Update(ctx, nodeUpdate); err != nil {
				log.Error(err, "Failed to remove finalizer for Memcached")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	pod := &corev1.Pod{}
	err = r.Get(ctx, types.NamespacedName{Name: nodeUpdate.Name, Namespace: nodeUpdate.Namespace}, pod)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new pod
		dep, err := r.createSchedule(nodeUpdate)
		if err != nil {
			log.Error(err, "Failed to define new Pod resource for Node Update")

			// The following implementation will update the status
			meta.SetStatusCondition(&nodeUpdate.Status.Conditions, metav1.Condition{Type: typeAvailable,
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to create Deployment for the custom resource (%s): (%s)", nodeUpdate.Name, err)})

			if err := r.Status().Update(ctx, nodeUpdate); err != nil {
				log.Error(err, "Failed to update Memcached status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}
		log.Info("Creating a new POD",
			"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		if err = r.Create(ctx, pod); err != nil {
			log.Error(err, "Failed to create new POD",
				"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}

		// Deployment created successfully
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeUpdateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&updatemanagerv1alpha1.NodeUpdate{}).
		Complete(r)
}

func (r *NodeUpdateReconciler) createSchedule(update *updatemanagerv1alpha1.NodeUpdate) (*corev1.Pod, error) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      update.Name,
			Namespace: update.Namespace,
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "",
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: &[]bool{false}[0],
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Containers: []corev1.Container{{
				Image:           update.Spec.Image,
				Name:            "update",
				ImagePullPolicy: corev1.PullIfNotPresent,
				SecurityContext: &corev1.SecurityContext{
					RunAsNonRoot:             &[]bool{false}[0],
					RunAsUser:                &[]int64{0}[0],
					AllowPrivilegeEscalation: &[]bool{true}[0],
					Capabilities: &corev1.Capabilities{
						Drop: []corev1.Capability{
							"ALL",
						},
					},
				},
				Ports:   []corev1.ContainerPort{},
				Command: []string{},
			}},
		},
	}

	if err := ctrl.SetControllerReference(update, pod, r.Scheme); err != nil {
		return nil, err
	}
	return pod, nil
}
