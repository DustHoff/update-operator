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
	"github.com/gorhill/cronexpr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
//+kubebuilder:rbac:groups=v1,resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=v1,resources=pods/status,verbs=get;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
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
		clusterUpdate.Status.NextNodeUpdate = nextTime.UnixMilli()
		meta.SetStatusCondition(&clusterUpdate.Status.Conditions, metav1.Condition{Type: typeAvailable, Status: metav1.ConditionTrue, Reason: "Schedule identified", Message: "next node update execution identified"})
		if err = r.Status().Update(ctx, clusterUpdate); err != nil {
			log.Error(err, "Failed to update node update status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterUpdateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&updatemanagerv1alpha1.ClusterUpdate{}).
		Complete(r)
}

func (r *ClusterUpdateReconciler) createNodeUpdatePod(update *updatemanagerv1alpha1.NodeUpdate) (*corev1.Pod, error) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      update.Name,
			Namespace: update.Namespace,
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": update.Name,
			},
			Tolerations: []corev1.Toleration{
				corev1.Toleration{Key: "node.kubernetes.io/unschedulable", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
			},
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: &[]bool{false}[0],
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Volumes: []corev1.Volume{
				{Name: "host", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/"}}},
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
