/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// WCMExportPhase is the step an export is in.
type WCMExportPhase string

// Phases of an export.
const (
	WCMExportPending     WCMExportPhase = "Pending"
	WCMExportExporting   WCMExportPhase = "Exporting"
	WCMExportDownloading WCMExportPhase = "Downloading"
	WCMExportPushing     WCMExportPhase = "Pushing"
	WCMExportSucceeded   WCMExportPhase = "Succeeded"
	WCMExportFailed      WCMExportPhase = "Failed"
)

// WCMExportDestination is where the export goes: an OCI artifact in a registry.
type WCMExportDestination struct {
	// Image: reference of the artifact, for example harbor.example.com/project/wcm/orbita-20261119:1. It holds the
	// zip of the export (wcm-export-<name>.zip) and EXPORT-INFO.txt, and DXWCMLibraryImport reads it as an image
	// source.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`
	// PushSecrets: Secrets of type kubernetes.io/dockerconfigjson with push rights on the repository.
	// +optional
	PushSecrets []LocalObjectReference `json:"pushSecrets,omitempty"`
	// Attributes: free key/value labels of the artifact, written as manifest annotations com.europesip.dx.attr.<key>
	// next to the standard ones (docs/artifact-annotations.md), so artifacts can be listed and filtered by them later.
	// Keys: lowercase letters, digits, dots and dashes (at most 63); values at most 256 characters.
	// +optional
	// +kubebuilder:validation:MaxProperties=32
	Attributes map[string]string `json:"attributes,omitempty"`
}

// DXWCMLibraryExportSpec is what to export and where to.
type DXWCMLibraryExportSpec struct {
	// ConnectionRef: the DXConnection of the DX the libraries are exported from.
	ConnectionRef LocalObjectReference `json:"connectionRef"`
	// VirtualPortal: URL context of the virtual portal the libraries are in. Empty: the base portal.
	// +kubebuilder:validation:Pattern=`^([A-Za-z0-9][A-Za-z0-9_-]*)?$`
	// +optional
	VirtualPortal string `json:"virtualPortal,omitempty"`
	// Libraries: titles of the libraries to export (each one must exist).
	// +kubebuilder:validation:MinItems=1
	Libraries []string `json:"libraries"`
	// Destination of the export.
	Destination WCMExportDestination `json:"destination"`
	// Timeout of the export request of each library. Default 1h.
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`
	// DependsOn: resources that must be Ready first.
	// +optional
	DependsOn []Dependency `json:"dependsOn,omitempty"`
	// Suspend stops the reconciliation.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// WCMExportedLibrary is a library of the export with its size.
type WCMExportedLibrary struct {
	Title string `json:"title"`
	// Bytes and Files: its size in the export, before compression.
	// +optional
	Bytes int64 `json:"bytes,omitempty"`
	// +optional
	Files int `json:"files,omitempty"`
}

// DXWCMLibraryExportStatus is the result of the export.
type DXWCMLibraryExportStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Phase WCMExportPhase `json:"phase,omitempty"`
	// AppliedHash of the spec of the last finished run: the export runs again only when it changes (or with the
	// reconcile annotation).
	// +optional
	AppliedHash string `json:"appliedHash,omitempty"`
	// Image: the artifact pushed, as repository@digest.
	// +optional
	Image string `json:"image,omitempty"`
	// Digest of the manifest of the artifact.
	// +optional
	Digest string `json:"digest,omitempty"`
	// ExportedLibraries: the libraries in the artifact, with their size.
	// +optional
	ExportedLibraries []WCMExportedLibrary `json:"exportedLibraries,omitempty"`
	// CurrentLibrary being exported (1-based), while it runs.
	// +optional
	CurrentLibrary int `json:"currentLibrary,omitempty"`
	// TotalBytes: size of the zip of the export.
	// +optional
	TotalBytes int64 `json:"totalBytes,omitempty"`
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
	// Message: last result in words.
	// +optional
	Message string `json:"message,omitempty"`
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=dxwcmexport
// +kubebuilder:printcolumn:name="Virtual portal",type=string,JSONPath=`.spec.virtualPortal`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.destination.image`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DXWCMLibraryExport exports Web Content Manager libraries of HCL DX Compose (base or virtual portal) to an OCI
// artifact (the zip of the export and EXPORT-INFO.txt), once per spec. DXWCMLibraryImport imports it elsewhere.
type DXWCMLibraryExport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              DXWCMLibraryExportSpec   `json:"spec"`
	Status            DXWCMLibraryExportStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DXWCMLibraryExportList contains a list of DXWCMLibraryExport.
type DXWCMLibraryExportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DXWCMLibraryExport `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DXWCMLibraryExport{}, &DXWCMLibraryExportList{})
		return nil
	})
}
