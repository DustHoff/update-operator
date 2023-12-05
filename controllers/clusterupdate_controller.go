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
	"errors"
	"fmt"
	"github.com/gorhill/cronexpr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	updatemanagerv1alpha1 "github.com/DustHoff/update-operator/api/v1alpha1"
)

const (
	typeReconcile = "Reconcile"
	typeAvailable = "Available"
	typeDegraded  = "Degraded"
)

// NodeReconciler reconciles a ClusterUpdate object
type ClusterUpdateReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=updatemanager.onesi.de,resources=clusterupdates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=updatemanager.onesi.de,resources=clusterupdates/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=updatemanager.onesi.de,resources=clusterupdates/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=pods/status,verbs=get;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// the ClusterUpdate object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *ClusterUpdateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	clusterUpdate := &updatemanagerv1alpha1.ClusterUpdate{}
	err := r.Get(ctx, req.NamespacedName, clusterUpdate)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("cluster update not found ignoring the resource")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to fetch cluster update resource")
		return ctrl.Result{}, err
	}

	if clusterUpdate.Spec.Update.Disabled {
		log.Info("node update has been disabled by cluster update definition")
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	if clusterUpdate.Spec.Update.Schedule == "" {
		log.Info("missing schedule definition")
		meta.SetStatusCondition(&clusterUpdate.Status.Conditions, metav1.Condition{Type: typeDegraded, Status: metav1.ConditionTrue, Reason: "Reconciling", Message: "missing node update schedule"})
		if err = r.Status().Update(ctx, clusterUpdate); err != nil {
			log.Error(err, "Failed to update node update status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	if clusterUpdate.Status.NextNodeUpdate == 0 {
		log.Info("evaluating next node update schedule time")
		log.Info("configured schedule is " + clusterUpdate.Spec.Update.Schedule)
		nextTime := cronexpr.MustParse(clusterUpdate.Spec.Update.Schedule).Next(time.Now())
		log.Info("evaluated next run is " + nextTime.String())
		clusterUpdate.Status.NextNodeUpdate = nextTime.Round(time.Minute).UnixMilli()
		meta.SetStatusCondition(&clusterUpdate.Status.Conditions, metav1.Condition{Type: typeAvailable, Status: metav1.ConditionTrue, Reason: "nextExecution", Message: "next node update execution is " + nextTime.String()})
		if err = r.Status().Update(ctx, clusterUpdate); err != nil {
			log.Error(err, "Failed to update node update status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	if time.Now().Round(time.Minute).Equal(time.UnixMilli(clusterUpdate.Status.NextNodeUpdate)) || time.Now().Round(time.Minute).After(time.UnixMilli(clusterUpdate.Status.NextNodeUpdate)) {
		log.Info("start node update process")
		nodeUpdateList := &updatemanagerv1alpha1.NodeUpdateList{}
		if err := r.List(ctx, nodeUpdateList); err != nil {
			log.Error(err, "failed to fetch node update list")
			return ctrl.Result{}, err
		}
		finished, err := r.executeNodeUpdateFlow(ctx, nodeUpdateList, clusterUpdate)
		meta.SetStatusCondition(&clusterUpdate.Status.Conditions, metav1.Condition{Type: typeProcessing, Status: metav1.ConditionTrue, Reason: "Update", Message: "Running Node Update"})
		if err = r.Status().Update(ctx, clusterUpdate); err != nil {
			log.Error(err, "Failed to update node update status")
			return ctrl.Result{}, err
		}

		if finished {
			nextTime := cronexpr.MustParse(clusterUpdate.Spec.Update.Schedule).Next(time.Now())
			log.Info("evaluated next run is " + nextTime.String())
			clusterUpdate.Status.NextNodeUpdate = nextTime.Round(time.Minute).UnixMilli()
			meta.SetStatusCondition(&clusterUpdate.Status.Conditions, metav1.Condition{Type: typeAvailable, Status: metav1.ConditionTrue, Reason: "nextExecution", Message: "next node update execution is " + nextTime.String()})
			if err = r.Status().Update(ctx, clusterUpdate); err != nil {
				log.Error(err, "Failed to update node update status")
				return ctrl.Result{}, err
			}
		}

	}
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterUpdateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&updatemanagerv1alpha1.ClusterUpdate{}).
		Complete(r)
}

func (r *ClusterUpdateReconciler) executeNodeUpdateFlow(ctx context.Context, list *updatemanagerv1alpha1.NodeUpdateList, update *updatemanagerv1alpha1.ClusterUpdate) (bool, error) {
	log := log.FromContext(ctx)
	for _, item := range list.Items {
		if item.Spec.Priority < 1 {
			return false, errors.New("Unsupported Priority for node " + item.Name + ". lowest node priority is 1")
		}
	}
	items := list.Items
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Spec.Priority > items[i].Spec.Priority
	})
	for _, item := range items {
		//check if the node update has already been executed
		if item.Labels["updatemanager.onesi.de/execution"] != string(update.Status.NextNodeUpdate) {
			//node update not initialized yet
			log.Info("initializing update process for " + item.Name)
			clone := item.DeepCopy()
			if clone.Labels == nil {
				clone.Labels = make(map[string]string)
			}
			clone.Labels["updatemanager.onesi.de/execution"] = string(update.Status.NextNodeUpdate)
			if clone.Annotations == nil {
				clone.Annotations = make(map[string]string)
			}
			clone.Annotations["updatemanager.onesi.de/execute"] = "nodeUpdate"
			log.Info(fmt.Sprintf("%+v\\n", clone))
			if err := r.Update(ctx, clone); err != nil {
				log.Error(err, "failed to label and annotate node update")
				return false, err
			}
			return false, nil
		} else {
			pod := &corev1.Pod{}
			if err := r.Get(ctx, types.NamespacedName{Name: item.Name + "-" + string(update.Status.NextNodeUpdate), Namespace: item.Namespace}, pod); err != nil {
				log.Error(err, "failed to fetch update pod")
				return false, err
			}
			switch pod.Status.Phase {
			case "Running":
			case "Failed":
				//TODO: Fetch pod logs
				err := errors.New("error during node update")
				log.Error(err, "Something went wrong during node update")
				return true, err
			case "Succeeded":

			}
		}
	}
	return true, nil
}
