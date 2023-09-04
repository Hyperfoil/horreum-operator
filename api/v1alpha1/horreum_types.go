/*
Copyright 2021.

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DatabaseSpec defines access info for a database
type DatabaseSpec struct {
	// Hostname for the database
	Host string `json:"host,omitempty"`
	// Database port; defaults to 5432
	Port int32 `json:"port,omitempty"`
	// Name of the database
	Name string `json:"name,omitempty"`
	// Name of secret resource with data `username` and `password`. Created if does not exist.
	Secret string `json:"secret,omitempty"`
}

// RouteSpec defines the route for external access.
type RouteSpec struct {
	// Host for the route leading to Controller REST endpoint. Example: horreum.apps.mycloud.example.com
	Host string `json:"host,omitempty"`
	// Either 'http' (for plain-text routes - not recommended), 'edge', 'reencrypt' or 'passthrough'
	Type string `json:"type,omitempty"`
	// Optional for edge and reencrypt routes, required for passthrough; Name of the secret hosting `tls.crt`, `tls.key` and optionally `ca.crt`
	TLS string `json:"tls,omitempty"`
}

// ExternalSpec defines endpoints for provided component (not deployed by this operator)
type ExternalSpec struct {
	// Public facing URI - Horreum will send this URI to the clients.
	PublicUri string `json:"publicUri,omitempty"`
	// Internal URI - Horreum will use this for communication but won't disclose that.
	InternalUri string `json:"internalUri,omitempty"`
}

// KeycloakSpec defines Keycloak setup
type KeycloakSpec struct {
	// When this is set Keycloak instance will not be deployed and Horreum will use this external instance.
	External ExternalSpec `json:"external,omitempty"`
	// Image that should be used for Keycloak deployment. Defaults to quay.io/keycloak/keycloak:latest
	Image string `json:"image,omitempty"`
	// Route for external access to the Keycloak instance.
	Route RouteSpec `json:"route,omitempty"`
	// Alternative service type when routes are not available (e.g. on vanilla K8s)
	ServiceType corev1.ServiceType `json:"serviceType,omitempty"`
	// Secret used for admin access to the deployed Keycloak instance. Created if does not exist.
	// Must contain keys `username` and `password`.
	AdminSecret string `json:"adminSecret,omitempty"`
	// Database coordinates Keycloak should use
	Database DatabaseSpec `json:"database,omitempty"`
}

// PostgresSpec defines PostgreSQL database setup
type PostgresSpec struct {
	// True (or omitted) to deploy PostgreSQL database
	Enabled *bool `json:"enabled,omitempty"`
	// Image used for PostgreSQL deployment. Defaults to registry.redhat.io/rhel8/postgresql-12:latest
	Image string `json:"image,omitempty"`
	// Secret used for unrestricted access to the database. Created if does not exist.
	// Must contain keys `username` and `password`.
	AdminSecret string `json:"adminSecret,omitempty"`
	// Name of PVC where the database will store the data. If empty, ephemeral storage will be used.
	PersistentVolumeClaim string `json:"persistentVolumeClaim,omitempty"`
	// Id of the user the container should run as
	User *int64 `json:"user,omitempty"`
}

// HorreumSpec defines the desired state of Horreum
type HorreumSpec struct {
	// Name of secret resource with data `username` and `password`. This will be the first user
	// that get's created in Horreum with the `admin` role, therefore it can create other users and teams.
	// Created automatically if it does not exist.
	AdminSecret string `json:"adminSecret,omitempty"`
	// Route for external access
	Route RouteSpec `json:"route,omitempty"`
	// Alternative service type when routes are not available (e.g. on vanilla K8s)
	ServiceType corev1.ServiceType `json:"serviceType,omitempty"`
	// Horreum image. Defaults to quay.io/hyperfoil/horreum:latest
	Image string `json:"image,omitempty"`
	// Database coordinates for Horreum data. Besides `username` and `password` the secret must
	// also contain key `dbsecret` that will be used to sign access to the database.
	Database DatabaseSpec `json:"database,omitempty"`
	// Keycloak specification
	Keycloak KeycloakSpec `json:"keycloak,omitempty"`
	// PostgreSQL specification
	Postgres PostgresSpec `json:"postgres,omitempty"`
	// Host used for NodePort services
	NodeHost string `json:"nodeHost,omitempty"`
}

// HorreumStatus defines the observed state of Horreum
type HorreumStatus struct {
	// Ready, Pending or Error.
	Status string `json:"status,omitempty"`
	// Last time state has changed.
	LastUpdate metav1.Time `json:"lastUpdate,omitempty"`
	// Explanation for the current status.
	Reason string `json:"reason,omitempty"`
	// Public URL of the Horreum application
	PublicUrl string `json:"publicUrl,omitempty"`
	// Public URL of Keycloak
	KeycloakUrl string `json:"keycloakUrl,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Horreum is the object configuring Horreum performance results repository
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=horreums,scope=Namespaced
// +kubebuilder:categories=all,hyperfoil
// +kubebuilder:resource:shortName=hrm
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.status",description="Overall status"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.reason",description="Reason for status"
// +kubebuilder:printcolumn:name="URL",type="string",JSONPath=".status.publicUrl",description="Horreum URL"
// +kubebuilder:printcolumn:name="Keycloak URL",type="string",JSONPath=".status.keycloakUrl",description="Keycloak URL"
type Horreum struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HorreumSpec   `json:"spec,omitempty"`
	Status HorreumStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// HorreumList contains a list of Horreum
type HorreumList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Horreum `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Horreum{}, &HorreumList{})
}
