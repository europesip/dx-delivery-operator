/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// WCMImportMode is how the libraries are imported.
// +kubebuilder:validation:Enum=Import;ImportCopy
type WCMImportMode string

const (
	// WCMImportKeepIDs keeps the IDs of the export (WCM data module taskType=import): importing again updates the
	// same items.
	WCMImportKeepIDs WCMImportMode = "Import"
	// WCMImportCopy gives the items new IDs (taskType=import-copy): every import is an independent copy.
	WCMImportCopy WCMImportMode = "ImportCopy"
)

// WCMImportPhase is the step an import is in.
type WCMImportPhase string

// Phases of an import.
const (
	WCMImportPending   WCMImportPhase = "Pending"
	WCMImportFetching  WCMImportPhase = "Fetching"
	WCMImportUploading WCMImportPhase = "Uploading"
	WCMImportImporting WCMImportPhase = "Importing"
	WCMImportVerifying WCMImportPhase = "Verifying"
	WCMImportSucceeded WCMImportPhase = "Succeeded"
	WCMImportFailed    WCMImportPhase = "Failed"
)

// WCMVirtualPortal is the virtual portal that receives the libraries.
type WCMVirtualPortal struct {
	// Context: URL context of the virtual portal (the part after /wps/portal/), for example "europesip".
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9][A-Za-z0-9_-]*$`
	Context string `json:"context"`
	// Create the virtual portal when it does not exist (XMLAccess, PortalVPHandler). Default false: a missing
	// virtual portal makes the import fail before anything is downloaded or uploaded.
	// +optional
	Create bool `json:"create,omitempty"`
	// Title of the virtual portal when the operator creates it (default: the context).
	// +optional
	Title string `json:"title,omitempty"`
	// Description of the virtual portal when the operator creates it.
	// +optional
	Description string `json:"description,omitempty"`
	// Realm of the virtual portal when the operator creates it (default: the realm of the first virtual portal
	// that already exists, or defaultWIMFileBasedRealm).
	// +optional
	Realm string `json:"realm,omitempty"`
}

// WCMLibrarySelection chooses the libraries of the export to import. Empty: every library of the export.
type WCMLibrarySelection struct {
	// Include: titles of the libraries to import. When set, only these (each one must be in the export).
	// +optional
	Include []string `json:"include,omitempty"`
	// Exclude: titles of the libraries to leave out (applied after include).
	// +optional
	Exclude []string `json:"exclude,omitempty"`
	// ExcludeHCLDefaults leaves out the libraries that HCL DX ships (Web Content, Portal Site, Script Portlet
	// Library, Web Content Templates 3.0, ...), so that only the own libraries of the export are imported.
	// +optional
	ExcludeHCLDefaults bool `json:"excludeHCLDefaults,omitempty"`
}

// WCMLibraryAccess grants a role on every imported library.
type WCMLibraryAccess struct {
	// DN of the user or group, for example cn=dxadmins,ou=groups,dc=example,dc=com
	// +kubebuilder:validation:MinLength=1
	DN string `json:"dn"`
	// Type of the principal.
	// +kubebuilder:validation:Enum=group;user
	// +kubebuilder:default=group
	// +optional
	Type string `json:"type,omitempty"`
	// Role in the library.
	// +kubebuilder:validation:Enum=User;Contributor;Editor;Manager;Administrator
	Role string `json:"role"`
}

// WCMUpload tunes the upload to DX.
type WCMUpload struct {
	// MaxChunkSize: the libraries are packed into zips no bigger than this, uploaded and imported one after the
	// other (DX dxFileTransfer accepts at most 500 MB per upload). Default 450Mi.
	// +optional
	MaxChunkSize *resource.Quantity `json:"maxChunkSize,omitempty"`
	// MaxChunkFiles: at most this many files per chunk (default 4000), so that each import request ends well before the
	// proxies in front of DX cut it (the HAProxy of DX Compose: 20 minutes). DX imports ~60 files/s for new items and
	// ~7 files/s when it updates existing ones (overwrite). 0 = no limit.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxChunkFiles *int32 `json:"maxChunkFiles,omitempty"`
}

// DXWCMLibraryImportSpec is what to import and where.
type DXWCMLibraryImportSpec struct {
	// ConnectionRef: the DXConnection of the DX that receives the libraries.
	ConnectionRef LocalObjectReference `json:"connectionRef"`
	// Source of the export: a .tar.bz2, .tar.gz/.tgz or .zip archive (URL), an OCI image or a ConfigMap. It holds a
	// WCM library export in JCR format (folder with <library>.node files), as written by ConfigEngine
	// export-wcm-data (traditional DX) or by the WCM data module of DX Compose (taskType=export).
	Source Source `json:"source"`
	// VirtualPortal that receives the libraries. Empty: the base portal.
	// +optional
	VirtualPortal *WCMVirtualPortal `json:"virtualPortal,omitempty"`
	// Libraries to import. Empty: every library of the export.
	// +optional
	Libraries WCMLibrarySelection `json:"libraries,omitempty"`
	// Overwrite: import also the libraries that already exist in the target (compared by title). Default false:
	// they are skipped and listed in status.skippedLibraries.
	// +optional
	Overwrite bool `json:"overwrite,omitempty"`
	// PortalSiteLibraryOverwrite: the Portal Site library (the managed pages of the portal, fixed ID
	// TWFuYWdlZCBQYWdlcyBSb290IExpYnJhcnk) is never imported, even when it is in the export or in libraries.include
	// and even with overwrite: true, unless this is true. Default false.
	// +optional
	PortalSiteLibraryOverwrite bool `json:"portalSiteLibraryOverwrite,omitempty"`
	// Mode: Import keeps the IDs of the export (importing again updates the same items); ImportCopy gives new IDs.
	// +kubebuilder:default=Import
	// +optional
	Mode WCMImportMode `json:"mode,omitempty"`
	// DryRun: only read the export and the target and report what would be imported or skipped (status), without
	// uploading or importing anything.
	// +optional
	DryRun bool `json:"dryRun,omitempty"`
	// Access: roles granted on every imported library after the import. The access control of the source refers to
	// users and groups that may not exist in the target.
	// +optional
	Access []WCMLibraryAccess `json:"access,omitempty"`
	// Upload settings.
	// +optional
	Upload WCMUpload `json:"upload,omitempty"`
	// Timeout of each import request (one per chunk). Default 2h.
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`
	// DependsOn: resources that must be Ready first.
	// +optional
	DependsOn []Dependency `json:"dependsOn,omitempty"`
	// Suspend stops the reconciliation.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// WCMLibraryResult is a library of the export and what happened to it.
type WCMLibraryResult struct {
	Title string `json:"title"`
	// Reason when it was skipped or failed.
	// +optional
	Reason string `json:"reason,omitempty"`
}

// DXWCMLibraryImportStatus is the result of the import.
type DXWCMLibraryImportStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Phase WCMImportPhase `json:"phase,omitempty"`
	// AppliedHash of the spec and the source revision of the last finished run: the import runs again only when it
	// changes.
	// +optional
	AppliedHash string `json:"appliedHash,omitempty"`
	// SourceRevision of the export.
	// +optional
	SourceRevision string `json:"sourceRevision,omitempty"`
	// LibrariesInExport: number of libraries found in the export.
	// +optional
	LibrariesInExport int `json:"librariesInExport,omitempty"`
	// +optional
	ImportedLibraries []string `json:"importedLibraries,omitempty"`
	// +optional
	SkippedLibraries []WCMLibraryResult `json:"skippedLibraries,omitempty"`
	// +optional
	FailedLibraries []WCMLibraryResult `json:"failedLibraries,omitempty"`
	// Chunks: number of zips the libraries were packed into.
	// +optional
	Chunks int `json:"chunks,omitempty"`
	// CurrentChunk being uploaded or imported (1-based).
	// +optional
	CurrentChunk int `json:"currentChunk,omitempty"`
	// DryRun: true when the status describes a dry run.
	// +optional
	DryRun bool `json:"dryRun,omitempty"`
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
// +kubebuilder:resource:shortName=dxwcmimport
// +kubebuilder:printcolumn:name="Virtual portal",type=string,JSONPath=`.spec.virtualPortal.context`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Chunks",type=integer,JSONPath=`.status.chunks`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DXWCMLibraryImport imports Web Content Manager libraries into HCL DX Compose (base or virtual portal) from a
// JCR export, once per source revision.
type DXWCMLibraryImport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              DXWCMLibraryImportSpec   `json:"spec"`
	Status            DXWCMLibraryImportStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DXWCMLibraryImportList contains a list of DXWCMLibraryImport.
type DXWCMLibraryImportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DXWCMLibraryImport `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DXWCMLibraryImport{}, &DXWCMLibraryImportList{})
		return nil
	})
}
