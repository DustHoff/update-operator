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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strconv"
	"time"

	updatemanagerv1alpha1 "github.com/DustHoff/update-operator/api/v1alpha1"
)

const (
	NodeRebootType string = "Reboot"
)

// PodReconciler reconciles a NodeUpdate object
type PodReconciler struct {
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
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	pod := &corev1.Pod{}
	err := r.Get(ctx, req.NamespacedName, pod)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if pod.Labels == nil {
		return ctrl.Result{}, nil
	}

	execution, ok := pod.Labels["updatemanager.onesi.de/execution"]
	if ok {
		log.Info("found node update pod")
		nodeUpdate := &updatemanagerv1alpha1.NodeUpdate{}
		if err := r.Get(ctx, types.NamespacedName{Name: pod.OwnerReferences[0].Name, Namespace: pod.Namespace}, nodeUpdate); err != nil {
			return ctrl.Result{}, err
		}

		if execution != nodeUpdate.Labels["updatemanager.onesi.de/execution"] {
			log.Info("found old update pod. skipping")
			return ctrl.Result{}, nil
		}

		log.Info(nodeUpdate.Name + " update is in state " + string(pod.Status.Phase))
		switch pod.Status.Phase {
		case "Succeeded":

			log.Info("node update completed, schedule restart")
			err := r.scheduleNodeRestart(ctx, nodeUpdate)
			if err != nil {
				return ctrl.Result{}, err
			}
			meta.SetStatusCondition(&nodeUpdate.Status.Conditions, metav1.Condition{Type: typeProcessing,
				Status: metav1.ConditionTrue, Reason: "reboot", Message: "Reboot scheduled"})

		case "Failed":
			meta.SetStatusCondition(&nodeUpdate.Status.Conditions, metav1.Condition{Type: typeFailed,
				Status: metav1.ConditionTrue, Reason: "update", Message: "node update failed"})
		}
		nodeUpdate.Labels["updatemanager.onesi.de/state"] = string(pod.Status.Phase)
		if err = r.Update(ctx, nodeUpdate); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Complete(r)
}

func (r *PodReconciler) scheduleNodeRestart(ctx context.Context, update *updatemanagerv1alpha1.NodeUpdate) error {
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
	node.Annotations["updatemanager.onesi.de/reboot"] = strconv.FormatInt(time.Now().UnixNano(), 10)
	if update.Annotations == nil {
		update.Annotations = make(map[string]string)
	}
	update.Annotations["updatemanager.onesi.de/reboot"] = ""
	if err := r.Update(ctx, update); err != nil {
		log.FromContext(ctx).Error(err, "failed to nodeupdate "+update.Name)
		return err
	}
	if err := r.Update(ctx, node); err != nil {
		log.FromContext(ctx).Error(err, "failed to trigger pod eviction on node "+node.Name)
		return err
	}
	return nil
}
