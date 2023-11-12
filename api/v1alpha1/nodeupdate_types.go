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

// NodeUpdateSpec defines the desired state of NodeUpdate
type NodeUpdateSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of NodeUpdate. Edit nodeupdate_types.go to remove/update
	Foo string `json:"foo,omitempty"`
}

// NodeUpdateStatus defines the observed state of NodeUpdate
type NodeUpdateStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// NodeUpdate is the Schema for the nodeupdates API
type NodeUpdate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeUpdateSpec   `json:"spec,omitempty"`
	Status NodeUpdateStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// NodeUpdateList contains a list of NodeUpdate
type NodeUpdateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeUpdate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NodeUpdate{}, &NodeUpdateList{})
}
