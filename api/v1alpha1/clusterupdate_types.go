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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ClusterUpdateSpec defines the desired state of ClusterUpdate
type ClusterUpdateSpec struct {

	// Desired Kubernetes Version to install
	K8Version string `json:"version,omitempty"`
	// Describe the OS Update schedule
	Schedule string `json:"schedule,omitempty"`
}

// ClusterUpdateStatus defines the observed state of ClusterUpdate
type ClusterUpdateStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// ClusterUpdate is the Schema for the clusterupdates API
type ClusterUpdate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterUpdateSpec   `json:"spec,omitempty"`
	Status ClusterUpdateStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ClusterUpdateList contains a list of ClusterUpdate
type ClusterUpdateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterUpdate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterUpdate{}, &ClusterUpdateList{})
}
