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
	"github.com/DustHoff/update-operator/api/v1alpha1"
	"github.com/DustHoff/update-operator/controllers/helper"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strconv"
	"time"
)

// NodeReconciler reconciles a ClusterUpdate object
type NodeReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Namespace string
}

//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;
//+kubebuilder:rbac:groups=updatemanager.onesi.de,resources=nodeupdates,verbs=get;create;update;list;watch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ClusterUpdate object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (n *NodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	node := &v1.Node{}
	err := n.Get(ctx, req.NamespacedName, node)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("node resource not found. deleting node update resource")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get node")
		return ctrl.Result{}, err
	}
	found := &v1alpha1.NodeUpdate{}
	err = n.Get(ctx, types.NamespacedName{Name: node.Name, Namespace: n.Namespace}, found)
	if err != nil && apierrors.IsNotFound(err) {
		nodeUpdate, err := n.generateNodeUpdate(node)
		if err != nil {
			log.Error(err, "Failed to generate NodeUpdate resource")
			return ctrl.Result{}, err
		}
		if err = n.Create(ctx, nodeUpdate); err != nil {
			log.Error(err, "Failed to create NodeUpdate resource")
		}

		if err = n.Get(ctx, types.NamespacedName{Name: node.Name, Namespace: n.Namespace}, found); err != nil {
			log.Error(err, "Failed to fetch nodeUpdate resource")
			return ctrl.Result{}, err
		}
	} else if err != nil {
		log.Error(err, "Failed to get Node Update")
		// Let's return the error for the reconciliation be re-trigged again
		return ctrl.Result{}, err
	}
	var role string
	if _, ok := node.ObjectMeta.Labels["node-role.kubernetes.io/control-plane"]; ok {
		role = "control-plane"
	} else {
		role = "data-plane"
	}
	if found.Spec.Role != role {
		found.Spec.Role = role
		if err = n.Update(ctx, found); err != nil {
			log.Error(err, "Failed to Update node update resource")
			return ctrl.Result{}, err
		}
	}

	if _, trigger := found.Annotations["updatemanager.onesi.de/reboot"]; trigger && node.Spec.Unschedulable {
		log.Info("found node with scheduled reboot " + found.Name)
		ready, condition := helper.KubletReadyCondition(node.Status.Conditions)
		if condition == nil {
			return ctrl.Result{}, nil
		}
		if !ready {
			log.Info("node currently not available, remove taints")
			err := n.removeTaints(ctx, node, found)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else {
			if value, ok := node.Annotations["updatemanager.onesi.de/reboot"]; ok {
				nsec, err := strconv.ParseInt(value, 10, 0)
				if err != nil {
					nsec = 0
				}
				if time.Now().Sub(time.Unix(0, nsec)) > 5*time.Minute {
					log.Info("node available assume safe reboot")
					err := n.removeTaints(ctx, node, found)
					if err != nil {
						return ctrl.Result{}, err
					}
				}
			}
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (n *NodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Node{}).
		Complete(n)
}

func (n *NodeReconciler) generateNodeUpdate(node *v1.Node) (*v1alpha1.NodeUpdate, error) {
	nodeUpdate := &v1alpha1.NodeUpdate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      node.Name,
			Namespace: n.Namespace,
		},
		Spec: v1alpha1.NodeUpdateSpec{},
	}
	return nodeUpdate, nil
}

func (n *NodeReconciler) removeTaints(ctx context.Context, node *v1.Node, found *v1alpha1.NodeUpdate) error {
	node.Spec.Unschedulable = false
	node.Spec.Taints = nil
	if node.Annotations != nil {
		delete(node.Annotations, "updatemanager.onesi.de/reboot")
	}
	found.Labels["updatemanager.onesi.de/state"] = "Succeeded"
	found.Annotations["updatemanager.onesi.de/reboot"] = "done"

	if err := n.Update(ctx, found); err != nil {
		return err
	}
	if err := n.Update(ctx, node); err != nil {
		return err
	}
	return nil
}
