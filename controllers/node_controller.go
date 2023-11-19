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
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
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
	if _, ok := node.ObjectMeta.Labels["node-role.kubernetes.io/control-plan"]; ok {
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
	return ctrl.Result{RequeueAfter: time.Minute}, nil
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
