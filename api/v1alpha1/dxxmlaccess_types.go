/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// XMLAccessPhase is the step a run is in.
type XMLAccessPhase string

// Phases of a run.
const (
	XMLAccessPending   XMLAccessPhase = "Pending"
	XMLAccessRunning   XMLAccessPhase = "Running"
	XMLAccessSucceeded XMLAccessPhase = "Succeeded"
	XMLAccessFailed    XMLAccessPhase = "Failed"
)

// XMLAccessVirtualPortal sends the requests to a virtual portal (/wps/config/<context>).
type XMLAccessVirtualPortal struct {
	// Context: URL context of the virtual portal (the part after /wps/portal/), for example "europesip".
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9][A-Za-z0-9_-]*$`
	Context string `json:"context"`
}

// XMLAccessOutput keeps the result XML of every request (for export requests).
type XMLAccessOutput struct {
	// ConfigMap that receives one key per request: <file name> (or "request.xml" for spec.xml) with DX's answer.
	// The operator creates it and owns it (deleted with the resource). A ConfigMap holds at most 1 MiB: bigger
	// results are cut and the status says so.
	ConfigMap LocalObjectReference `json:"configMap"`
}

// XMLAccessCleanup is a request run when the resource is deleted.
type XMLAccessCleanup struct {
	// XML of the request (inline).
	// +optional
	XML string `json:"xml,omitempty"`
	// File of spec.source with the request.
	// +optional
	File string `json:"file,omitempty"`
}

// DXXMLAccessSpec says which XMLAccess requests to run and where.
// +kubebuilder:validation:XValidation:rule="has(self.xml) != has(self.source)",message="set exactly one of xml or source"
type DXXMLAccessSpec struct {
	// ConnectionRef: the DXConnection of the DX (its user must be a DX administrator).
	ConnectionRef LocalObjectReference `json:"connectionRef"`
	// VirtualPortal: send the requests to this virtual portal. Empty: the base portal.
	// +optional
	VirtualPortal *XMLAccessVirtualPortal `json:"virtualPortal,omitempty"`
	// XML: one request, inline.
	// +optional
	XML string `json:"xml,omitempty"`
	// Source of the files with the requests: an OCI image or ORAS artifact (Harbor), an archive URL (for example
	// the archive of a folder of a Git repository: https://gitlab.example.com/g/p/-/archive/main/p-main.tar.gz with
	// path p-main/xmlaccess, or an S3 presigned URL) or a ConfigMap.
	// +optional
	Source *Source `json:"source,omitempty"`
	// Files of the source to run, in this order. Empty: every *.xml file of the source, sorted by path.
	// +optional
	Files []string `json:"files,omitempty"`
	// Variables: ${NAME} in the XML is replaced by the value (not for secrets: the spec is plain text).
	// +optional
	Variables map[string]string `json:"variables,omitempty"`
	// Output: keep DX's answer to every request in a ConfigMap (handy for export requests).
	// +optional
	Output *XMLAccessOutput `json:"output,omitempty"`
	// ContinueOnError: run the next requests after a failed one. Default false: stop at the first failure.
	// +optional
	ContinueOnError bool `json:"continueOnError,omitempty"`
	// Cleanup: a request run when the resource is deleted. Without it, deleting the resource changes nothing in
	// DX. A failed cleanup is reported as an event; the resource is removed anyway.
	// +optional
	Cleanup *XMLAccessCleanup `json:"cleanup,omitempty"`
	// DryRun: only fetch the files, check that each one is an XMLAccess request and report them, without calling DX.
	// +optional
	DryRun bool `json:"dryRun,omitempty"`
	// Timeout of each request. Default 10m.
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`
	// DependsOn: resources that must be Ready first.
	// +optional
	DependsOn []Dependency `json:"dependsOn,omitempty"`
	// Suspend stops the reconciliation.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// XMLAccessFileResult is one request of a run and what DX answered.
type XMLAccessFileResult struct {
	// File of the source, or "request.xml" for spec.xml.
	File string `json:"file"`
	// Type of the request (update, export...).
	// +optional
	Type string `json:"type,omitempty"`
	// Result: ok, error, skipped (after an earlier failure) or checked (dry run).
	Result string `json:"result"`
	// Message: the first message of DX when it failed.
	// +optional
	Message string `json:"message,omitempty"`
}

// DXXMLAccessStatus is the result of the last run.
type DXXMLAccessStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Phase XMLAccessPhase `json:"phase,omitempty"`
	// AppliedHash of the spec, the source revision and the reconcile annotation of the last finished run: it runs
	// again only when it changes.
	// +optional
	AppliedHash string `json:"appliedHash,omitempty"`
	// SourceRevision of the files.
	// +optional
	SourceRevision string `json:"sourceRevision,omitempty"`
	// Results: one entry per request, in the order they ran.
	// +optional
	Results []XMLAccessFileResult `json:"results,omitempty"`
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
// +kubebuilder:resource:shortName=dxxml
// +kubebuilder:printcolumn:name="Virtual portal",type=string,JSONPath=`.spec.virtualPortal.context`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DXXMLAccess runs XMLAccess requests against HCL DX (base or virtual portal), once per change of the spec or of
// its source, like a Job: it does not watch for drift.
type DXXMLAccess struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              DXXMLAccessSpec   `json:"spec"`
	Status            DXXMLAccessStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DXXMLAccessList contains a list of DXXMLAccess.
type DXXMLAccessList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DXXMLAccess `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DXXMLAccess{}, &DXXMLAccessList{})
		return nil
	})
}
