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

// ClusterUpdateReconciler reconciles a ClusterUpdate object and orchestrates node updates
// across the cluster respecting the MaxUnavailableNode setting.
type ClusterUpdateReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=updatemanager.onesi.de,resources=clusterupdates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=updatemanager.onesi.de,resources=clusterupdates/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=updatemanager.onesi.de,resources=clusterupdates/finalizers,verbs=update

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
			log.Info("all nodes updated, next run is " + nextTime.String())
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

// executeNodeUpdateFlow orchestrates node updates respecting the MaxUnavailableNode limit.
// It initialises up to MaxUnavailableNode nodes simultaneously (default 1) and returns true
// when every node has completed the current update cycle.
func (r *ClusterUpdateReconciler) executeNodeUpdateFlow(ctx context.Context, list *updatemanagerv1alpha1.NodeUpdateList, update *updatemanagerv1alpha1.ClusterUpdate) (bool, error) {
	log := log.FromContext(ctx)

	for _, item := range list.Items {
		if item.Spec.Priority < 1 {
			return false, errors.New("unsupported priority for node " + item.Name + "; lowest allowed priority is 1")
		}
	}

	sort.Sort(list)

	maxUnavailable := update.Spec.Update.MaxUnavailableNode
	if maxUnavailable < 1 {
		maxUnavailable = 1
	}

	executionID := strconv.FormatInt(update.Status.NextNodeUpdate, 10)

	// First pass: transition completed nodes and detect failures.
	for i := range list.Items {
		item := &list.Items[i]
		if item.Labels == nil {
			item.Labels = make(map[string]string)
		}
		if item.Annotations == nil {
			item.Annotations = make(map[string]string)
		}

		if item.Labels[LabelCompleted] == executionID {
			continue
		}
		if item.Labels[LabelExecution] != executionID {
			continue
		}

		switch item.Labels[LabelState] {
		case StateFailed:
			return false, errors.New("node update failed for " + item.Name)
		case StateSucceeded:
			if item.Annotations[AnnotationReboot] == RebootDone {
				log.Info(item.Name + " reboot completed, marking cycle as done")
				item.Labels[LabelCompleted] = executionID
				delete(item.Annotations, AnnotationReboot)
				if err := r.Update(ctx, item); err != nil {
					log.Error(err, "failed to mark node update as completed", "node", item.Name)
					return false, err
				}
			}
		}
	}

	// Count nodes that are currently in progress (initialized but not yet completed).
	inProgress := int32(0)
	allCompleted := true
	for i := range list.Items {
		item := &list.Items[i]
		if item.Labels[LabelCompleted] == executionID {
			continue
		}
		allCompleted = false
		if item.Labels[LabelExecution] == executionID {
			inProgress++
		}
	}

	if allCompleted {
		return true, nil
	}

	// Second pass: initialise additional nodes up to the MaxUnavailableNode limit.
	for i := range list.Items {
		if inProgress >= maxUnavailable {
			break
		}
		item := &list.Items[i]

		if item.Labels[LabelCompleted] == executionID {
			continue
		}
		if item.Labels[LabelExecution] == executionID {
			continue
		}

		log.Info("initializing update process for "+item.Name, "index", i+1, "inProgress", inProgress, "maxUnavailable", maxUnavailable)
		if item.Labels == nil {
			item.Labels = make(map[string]string)
		}
		if item.Annotations == nil {
			item.Annotations = make(map[string]string)
		}
		item.Labels[LabelExecution] = executionID
		item.Labels[LabelState] = StateInitialized
		item.Annotations[AnnotationExecute] = TriggerNodeUpdate
		delete(item.Annotations, AnnotationReboot)

		if err := r.Update(ctx, item); err != nil {
			log.Error(err, "failed to initialize node update", "node", item.Name)
			return false, err
		}
		inProgress++
	}

	return false, nil
}
