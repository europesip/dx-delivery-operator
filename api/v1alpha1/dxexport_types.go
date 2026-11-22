/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// DXExportPage is a page to export: by unique name, with or without the pages under it.
type DXExportPage struct {
	// UniqueName of the page in DX (for example orbita-site).
	// +kubebuilder:validation:MinLength=1
	UniqueName string `json:"uniqueName"`
	// Descendants: also every page under it (export-descendants). Default true.
	// +optional
	// +kubebuilder:default=true
	Descendants *bool `json:"descendants,omitempty"`
}

// DXExportedTheme is a theme in the bundle.
type DXExportedTheme struct {
	UniqueName string `json:"uniqueName"`
	// Folder of the theme in the WebDAV store fs-type1/themes (empty: no static files there).
	// +optional
	Folder string `json:"folder,omitempty"`
	// +optional
	Skins []string `json:"skins,omitempty"`
	// Files of the WebDAV folder.
	// +optional
	Files int `json:"files,omitempty"`
}

// DXExportParts is what goes into the bundle. At least one part.
type DXExportParts struct {
	// Pages: XMLAccess export of these pages (layout, portlets, properties, access, friendly URLs, the WCM content
	// mapping of managed pages), as one request importable into another portal (xmlaccess/01-pages.xml).
	// +optional
	Pages []DXExportPage `json:"pages,omitempty"`
	// PortalSite: also export the Portal Site library (the content of the managed pages), with the WCM data module.
	// Imported after the pages.
	// +optional
	PortalSite bool `json:"portalSite,omitempty"`
	// Libraries: titles of WCM libraries to export with the WCM data module (wcm/wcm-export-<name>.zip, with Portal
	// Site when portalSite is set).
	// +optional
	Libraries []string `json:"libraries,omitempty"`
	// Themes: unique names of themes to export: for each one its registration with its skins (XMLAccess, an update
	// request: themes/theme-<name>.xml) and its WebDAV folder (themes/theme-<name>-webdav.zip). Imported first: the
	// pages refer to their themes and skins.
	// +optional
	Themes []string `json:"themes,omitempty"`
}

// DXExportSpec is what to export and where to.
type DXExportSpec struct {
	// ConnectionRef: the DXConnection of the DX to export from.
	ConnectionRef LocalObjectReference `json:"connectionRef"`
	// VirtualPortal: URL context of a virtual portal; empty for the base portal.
	// +optional
	VirtualPortal string `json:"virtualPortal,omitempty"`
	// Parts of the bundle.
	Parts DXExportParts `json:"parts"`
	// Destination: the OCI artifact (the "DX bundle"), its push secrets and attributes.
	Destination WCMExportDestination `json:"destination"`
	// Timeout of each export request to DX. Default 1h.
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`
	// DependsOn: resources that must be Ready first.
	// +optional
	DependsOn []Dependency `json:"dependsOn,omitempty"`
	// Suspend stops the reconciliation.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// DXExportedPage is a page of the bundle.
type DXExportedPage struct {
	UniqueName string `json:"uniqueName"`
	// Nodes: content nodes exported for it (the page and, with descendants, the pages under it).
	// +optional
	Nodes int `json:"nodes,omitempty"`
}

// DXBundleFile is a file of the bundle, as listed in bundle.json.
type DXBundleFile struct {
	Path string `json:"path"`
	// +optional
	Bytes int64 `json:"bytes,omitempty"`
	// +optional
	SHA256 string `json:"sha256,omitempty"`
}

// DXExportStatus is the result of the export.
type DXExportStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Phase: Pending, Exporting, Downloading, Pushing, Succeeded or Failed.
	// +optional
	Phase WCMExportPhase `json:"phase,omitempty"`
	// AppliedHash of the spec of the last finished run.
	// +optional
	AppliedHash string `json:"appliedHash,omitempty"`
	// Image: the artifact pushed, as repository@digest.
	// +optional
	Image string `json:"image,omitempty"`
	// +optional
	Digest string `json:"digest,omitempty"`
	// Pages exported, with the content nodes of each.
	// +optional
	Pages []DXExportedPage `json:"pages,omitempty"`
	// PagesExported: content nodes in xmlaccess/01-pages.xml.
	// +optional
	PagesExported int `json:"pagesExported,omitempty"`
	// Themes exported: unique name, WebDAV folder, skins and number of files.
	// +optional
	Themes []DXExportedTheme `json:"themes,omitempty"`
	// ExportedLibraries: the WCM libraries in the bundle (Portal Site included), with their size.
	// +optional
	ExportedLibraries []WCMExportedLibrary `json:"exportedLibraries,omitempty"`
	// Files of the bundle with their size and sha256.
	// +optional
	Files []DXBundleFile `json:"files,omitempty"`
	// Warnings: what the target must already have (themes, skins, portlets) or may not import (see EXPORT-INFO.txt).
	// +optional
	Warnings []string `json:"warnings,omitempty"`
	// TotalBytes: size of all the files of the bundle.
	// +optional
	TotalBytes int64 `json:"totalBytes,omitempty"`
	// CurrentStep while it runs, in words.
	// +optional
	CurrentStep string `json:"currentStep,omitempty"`
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=dxexport
// +kubebuilder:printcolumn:name="Virtual portal",type=string,JSONPath=`.spec.virtualPortal`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.destination.image`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DXExport exports pages, the Portal Site library and WCM libraries of HCL DX Compose (base or virtual portal) into
// one portable OCI artifact, a "DX bundle" (EXPORT-INFO.txt, bundle.json, xmlaccess/, wcm/), once per spec. Every
// file is in the native format of DX (XMLAccess, the JCR export of the WCM data module), so the bundle can be imported
// into another DX Compose (DXXMLAccess, DXWCMLibraryImport) or into a traditional DX on WebSphere (xmlaccess.sh,
// ConfigEngine import-wcm-data).
type DXExport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              DXExportSpec   `json:"spec"`
	Status            DXExportStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DXExportList contains a list of DXExport.
type DXExportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DXExport `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DXExport{}, &DXExportList{})
		return nil
	})
}
