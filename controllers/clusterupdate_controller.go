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
	"sort"
	"strconv"
	"time"

	"github.com/gorhill/cronexpr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// ClusterUpdateReconciler reconciles a ClusterUpdate object
type ClusterUpdateReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=updatemanager.onesi.de,resources=clusterupdates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=updatemanager.onesi.de,resources=clusterupdates/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=updatemanager.onesi.de,resources=clusterupdates/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=pods/status,verbs=get;update;patch

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
		clusterUpdate.Status.NextNodeUpdate = 0
		meta.SetStatusCondition(&clusterUpdate.Status.Conditions, metav1.Condition{Type: typeStopped, Status: metav1.ConditionTrue, Reason: "disabled", Message: "node update has been disabled"})
		if err = r.Status().Update(ctx, clusterUpdate); err != nil {
			log.Error(err, "Failed to update node update status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if clusterUpdate.Spec.Update.Schedule == "" {
		log.Info("missing schedule definition")
		meta.SetStatusCondition(&clusterUpdate.Status.Conditions, metav1.Condition{Type: typeDegraded, Status: metav1.ConditionTrue, Reason: "Reconciling", Message: "missing node update schedule"})
		if err = r.Status().Update(ctx, clusterUpdate); err != nil {
			log.Error(err, "Failed to update node update status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Validate cron expression before using it to avoid a panic from MustParse.
	cronExpr, err := cronexpr.Parse(clusterUpdate.Spec.Update.Schedule)
	if err != nil {
		log.Error(err, "Invalid cron expression", "schedule", clusterUpdate.Spec.Update.Schedule)
		meta.SetStatusCondition(&clusterUpdate.Status.Conditions, metav1.Condition{Type: typeDegraded, Status: metav1.ConditionTrue, Reason: "InvalidSchedule", Message: "invalid cron expression: " + err.Error()})
		if statusErr := r.Status().Update(ctx, clusterUpdate); statusErr != nil {
			log.Error(statusErr, "Failed to update node update status")
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, nil
	}

	if clusterUpdate.Status.NextNodeUpdate == 0 {
		log.Info("evaluating next node update schedule time")
		log.Info("configured schedule is " + clusterUpdate.Spec.Update.Schedule)
		nextTime := cronExpr.Next(time.Now())
		log.Info("evaluated next run is " + nextTime.String())
		clusterUpdate.Status.NextNodeUpdate = nextTime.Round(time.Minute).UnixMilli()
		meta.SetStatusCondition(&clusterUpdate.Status.Conditions, metav1.Condition{Type: typeAvailable, Status: metav1.ConditionTrue, Reason: "nextExecution", Message: "next node update execution is " + nextTime.String()})
		if err = r.Status().Update(ctx, clusterUpdate); err != nil {
			log.Error(err, "Failed to update node update status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// Capture time.Now() once to avoid a race between two calls in the same condition.
	now := time.Now().Round(time.Minute)
	nextUpdate := time.UnixMilli(clusterUpdate.Status.NextNodeUpdate)
	if now.Equal(nextUpdate) || now.After(nextUpdate) {
		log.Info("check node update process")
		nodeUpdateList := &updatemanagerv1alpha1.NodeUpdateList{}
		if err := r.List(ctx, nodeUpdateList); err != nil {
			log.Error(err, "failed to fetch node update list")
			return ctrl.Result{}, err
		}
		finished, flowErr := r.executeNodeUpdateFlow(ctx, nodeUpdateList, clusterUpdate)
		if flowErr != nil {
			log.Info("remove next schedule")
			clusterUpdate.Status.NextNodeUpdate = 0
			meta.SetStatusCondition(&clusterUpdate.Status.Conditions, metav1.Condition{Type: typeDegraded, Status: metav1.ConditionTrue, Reason: "Update", Message: "Node Update failed"})
		} else {
			meta.SetStatusCondition(&clusterUpdate.Status.Conditions, metav1.Condition{Type: typeProcessing, Status: metav1.ConditionTrue, Reason: "Update", Message: "Running Node Update"})
		}
		if err = r.Status().Update(ctx, clusterUpdate); err != nil {
			log.Error(err, "Failed to update node update status")
			return ctrl.Result{}, err
		}

		if finished {
			nextTime := cronExpr.Next(time.Now())
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

	sort.Sort(list)

	for index, item := range list.Items {
		log.Info(item.Name + " identified as " + strconv.Itoa(index+1) + " element")

		// Ensure Labels map is initialised before any read or write.
		if item.Labels == nil {
			item.Labels = make(map[string]string)
		}
		if item.Annotations == nil {
			item.Annotations = make(map[string]string)
		}

		if item.Labels["updatemanager.onesi.de/execution"] != strconv.FormatInt(update.Status.NextNodeUpdate, 10) {
			// Node update not initialized yet; mark it and return – only one at a time.
			log.Info("initializing update process for " + item.Name)
			item.Labels["updatemanager.onesi.de/execution"] = strconv.FormatInt(update.Status.NextNodeUpdate, 10)
			item.Labels["updatemanager.onesi.de/state"] = "initialized"
			item.Annotations["updatemanager.onesi.de/execute"] = "nodeUpdate"
			delete(item.Annotations, "updatemanager.onesi.de/reboot")

			if err := r.Update(ctx, &item); err != nil {
				log.Error(err, "failed to label and annotate node update")
				return false, err
			}
			return false, nil
		}

		// Node already initialized for this execution cycle.
		if completed := item.Labels["updatemanager.onesi.de/completed"]; completed == strconv.FormatInt(update.Status.NextNodeUpdate, 10) {
			continue
		}

		label, hasState := item.Labels["updatemanager.onesi.de/state"]
		if !hasState {
			log.Info("state label not found")
			return false, nil
		}

		switch label {
		case "Failed":
			log.Info("Something went wrong during node update on " + item.Name)
			// Mark the node update as failed without disabling the entire cluster update.
			return false, errors.New("node update failed for " + item.Name)

		case "Succeeded":
			rebootVal, hasReboot := item.Annotations["updatemanager.onesi.de/reboot"]
			if !hasReboot {
				log.Info("reboot not yet scheduled, wait")
				return false, nil
			}
			if rebootVal != "done" {
				log.Info(item.Name + " reboot is scheduled, but not yet done. waiting for completion")
				return false, nil
			}
			log.Info(item.Name + " has been restarted")
			item.Labels["updatemanager.onesi.de/completed"] = strconv.FormatInt(update.Status.NextNodeUpdate, 10)
			delete(item.Annotations, "updatemanager.onesi.de/reboot")
			if err := r.Update(ctx, &item); err != nil {
				log.Error(err, "failed to remove reboot annotation")
				return false, err
			}
			continue

		default:
			log.Info("update not finished yet on index " + strconv.Itoa(index+1))
			return false, nil
		}
	}
	return true, nil
}
