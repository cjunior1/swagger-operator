/*
Copyright 2025.

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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// BackendServiceSpec define a informação do serviço de backend.
type BackendServiceSpec struct {
	// Nome do serviço Kubernetes de backend.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Porta do serviço de backend.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	Port int `json:"port"`

	// padrao "/swagger/doc.json"
	// +optional
	SwaggerEndpoint string `json:"swaggerEndpoint,omitempty"`
}

// SwaggerKongSpec defines the desired state of SwaggerKong
type SwaggerKongSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	// foo is an example field of SwaggerKong. Edit swaggerkong_types.go to remove/update
	// +optional
	//Foo *string `json:"foo,omitempty"`

	// Nome do ConfigMap no mesmo namespace que contém a especificação swagger/openapi.
	// A chave no ConfigMap deve ser 'swagger.json' ou 'swagger.yaml'.
	// +kubebuilder:validation:Required
	//SwaggerConfigMapName string `json:"swaggerConfigMapName"`

	// Serviço de backend para onde o Kong irá rotear o tráfego.
	// +kubebuilder:validation:Required
	BackendService BackendServiceSpec `json:"backendService"`
}

// SwaggerKongStatus defines the observed state of SwaggerKong.
type SwaggerKongStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Estado atual do processamento. Ex: "Processing", "Success", "Error".
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Número de rotas do Kong que foram criadas.
	RouteCount int `json:"routeCount,omitempty"`

	// O checksum SHA256 do último conteúdo Swagger que foi aplicado com sucesso.
	// +optional
	LastAppliedSwaggerChecksum string `json:"lastAppliedSwaggerChecksum,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// SwaggerKong is the Schema for the swaggerkongs API
type SwaggerKong struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of SwaggerKong
	// +required
	Spec SwaggerKongSpec `json:"spec"`

	// status defines the observed state of SwaggerKong
	// +optional
	Status SwaggerKongStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// SwaggerKongList contains a list of SwaggerKong
type SwaggerKongList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SwaggerKong `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SwaggerKong{}, &SwaggerKongList{})
}
