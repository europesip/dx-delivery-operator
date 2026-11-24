// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// WCMAdminTaskKind is a maintenance module of Web Content Manager that DX Compose runs over HTTP
// (/wps/wcm/myconnect?MOD=...), the same modules that traditional DX runs with ConfigEngine run-wcm-admin-task-*.
// +kubebuilder:validation:Enum=MemberFixer;WorkflowControlChecker
type WCMAdminTaskKind string

const (
	// WCMAdminMemberFixer checks the users and groups that WCM items reference (renamed, deleted, other unique ID)
	// and fixes the references (ConfigEngine run-wcm-admin-task-member-fixer; MOD=MemberFixer).
	WCMAdminMemberFixer WCMAdminTaskKind = "MemberFixer"
	// WCMAdminWorkflowControlChecker re-applies workflow stage security, reschedules pending workflow actions or
	// repairs items without a workflow (MOD=WorkflowControlChecker).
	WCMAdminWorkflowControlChecker WCMAdminTaskKind = "WorkflowControlChecker"
)

// WCMItemType is an item type of WCM, for restrictOn.
// +kubebuilder:validation:Enum=Content;Folder;Project;PresentationTemplate;AuthoringTemplate;ContentTemplate;SiteAreaTemplate;Taxonomy;Category;SiteArea;Workflow;WorkflowStage;WorkflowAction;Cmpnt
type WCMItemType string

// WCMReferenceAction is what the member fixer does with a reference it cannot keep as it is.
// +kubebuilder:validation:Enum=update;remove
type WCMReferenceAction string

// MemberFixerOptions are the documented options of the member fixer (HCL "How to use the member fixer task").
type MemberFixerOptions struct {
	// AltDn: references to users or groups that no longer exist: update replaces them with the alternate DNs of
	// MemberFixerModule.properties, remove deletes them (altDn).
	// +optional
	AltDn WCMReferenceAction `json:"altDn,omitempty"`
	// InvalidDn: references with an invalid DN: update replaces them with the DN of the portal administrator,
	// remove deletes them (invalidDn).
	// +optional
	InvalidDn WCMReferenceAction `json:"invalidDn,omitempty"`
	// MismatchedId: references to users whose unique ID changed in the user repository: update fixes the ID,
	// remove deletes them (mismatchedId). HCL warns to use it with care, mainly together with invalidDn.
	// +optional
	MismatchedId WCMReferenceAction `json:"mismatchedId,omitempty"`
	// Realm to process in a federated user registry (default: the default realm).
	// +optional
	Realm string `json:"realm,omitempty"`
	// NoRealmDn: also check references outside the base DNs of every realm (noRealmDn).
	// +optional
	NoRealmDn bool `json:"noRealmDn,omitempty"`
}

// WorkflowCheckerOptions are the documented options of the workflow checker (HCL "Managing workflows by using the
// workflow checker tool"). At least one of them must be true: DX refuses to run the module without one.
type WorkflowCheckerOptions struct {
	// UpdateDocSecurity saves every workflowed item again, applying the security of its workflow stage.
	// +optional
	UpdateDocSecurity bool `json:"updateDocSecurity,omitempty"`
	// PreserveSecurityInheritancePropagation, with updateDocSecurity: keep the inheritance and propagation of the
	// security of each item.
	// +optional
	PreserveSecurityInheritancePropagation bool `json:"preserveSecurityInheritancePropagation,omitempty"`
	// RescheduleActions schedules again the pending workflow actions (scheduled moves, expiry...).
	// +optional
	RescheduleActions bool `json:"rescheduleActions,omitempty"`
	// RepairInvalidWorkflow finds the items whose workflow and workflow stage are both empty; with fix it moves them
	// to the internal system workflow.
	// +optional
	RepairInvalidWorkflow bool `json:"repairInvalidWorkflow,omitempty"`
}

// WCMAdminTaskOutput: the report also goes to a registry (it is always kept in the ConfigMap <name>-report).
type WCMAdminTaskOutput struct {
	// OCI: push the report of every library as an OCI artifact (one file per library and REPORT-INFO.txt).
	// +optional
	OCI *XMLAccessOCIOutput `json:"oci,omitempty"`
}

// DXWCMAdminTaskSpec is the module to run, on which libraries and how.
// +kubebuilder:validation:XValidation:rule="(has(self.allLibraries) && self.allLibraries) != (has(self.libraries) && size(self.libraries) > 0)",message="libraries (one or more) or allLibraries: true, not both"
type DXWCMAdminTaskSpec struct {
	// ConnectionRef: the DXConnection of the DX.
	ConnectionRef LocalObjectReference `json:"connectionRef"`
	// VirtualPortal: URL context of a virtual portal; empty for the base portal.
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_-]{0,64}$`
	// +optional
	VirtualPortal string `json:"virtualPortal,omitempty"`
	// Task: the module to run.
	Task WCMAdminTaskKind `json:"task"`
	// Libraries to process, by title. One request per library, one after the other.
	// +optional
	// +kubebuilder:validation:MaxItems=500
	Libraries []string `json:"libraries,omitempty"`
	// AllLibraries: every library of the portal (read from WCM REST when the run starts), one request per library.
	// +optional
	AllLibraries bool `json:"allLibraries,omitempty"`
	// Fix: apply the changes. Default false: report only (DX changes nothing). Run a report first: HCL warns that the
	// fixes may not be easy to undo.
	// +optional
	Fix bool `json:"fix,omitempty"`
	// RestrictOn: only these item types (default: all).
	// +optional
	RestrictOn []WCMItemType `json:"restrictOn,omitempty"`
	// PreserveDates: keep the last modified date of the items that are saved (preserve_dates).
	// +optional
	PreserveDates bool `json:"preserveDates,omitempty"`
	// MemberFixer options (task MemberFixer).
	// +optional
	MemberFixer *MemberFixerOptions `json:"memberFixer,omitempty"`
	// WorkflowChecker options (task WorkflowControlChecker).
	// +optional
	WorkflowChecker *WorkflowCheckerOptions `json:"workflowChecker,omitempty"`
	// Output: where else the report goes.
	// +optional
	Output *WCMAdminTaskOutput `json:"output,omitempty"`
	// Timeout of the request of each library. Default 1h.
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`
	// DependsOn: resources that must be Ready first.
	// +optional
	DependsOn []Dependency `json:"dependsOn,omitempty"`
	// Suspend stops the reconciliation.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// WCMAdminTaskPhase is where a run is.
// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed
type WCMAdminTaskPhase string

const (
	WCMAdminPending   WCMAdminTaskPhase = "Pending"
	WCMAdminRunning   WCMAdminTaskPhase = "Running"
	WCMAdminSucceeded WCMAdminTaskPhase = "Succeeded"
	WCMAdminFailed    WCMAdminTaskPhase = "Failed"
)

// WCMAdminLibraryResult is the summary DX gave for one library.
type WCMAdminLibraryResult struct {
	Library string `json:"library"`
	// ItemsProcessed, ItemsUpdated: "Items Processed", "Items Updated and Saved".
	// +optional
	ItemsProcessed int `json:"itemsProcessed,omitempty"`
	// +optional
	ItemsUpdated int `json:"itemsUpdated,omitempty"`
	// Member fixer: references that need removal / update, virtual members that need update.
	// +optional
	ReferencesToRemove int `json:"referencesToRemove,omitempty"`
	// +optional
	ReferencesToUpdate int `json:"referencesToUpdate,omitempty"`
	// +optional
	VirtualMembersToUpdate int `json:"virtualMembersToUpdate,omitempty"`
	// Errors DX detected.
	// +optional
	Errors int `json:"errors,omitempty"`
	// Result: ok, or the error of the request.
	Result string `json:"result"`
}

// DXWCMAdminTaskStatus is the result of the last run.
type DXWCMAdminTaskStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// AppliedHash of the spec of the last finished run (it runs again only when the spec changes, or with the
	// annotation dx.europesip.com/reconcile).
	// +optional
	AppliedHash string `json:"appliedHash,omitempty"`
	// +optional
	Phase WCMAdminTaskPhase `json:"phase,omitempty"`
	// Libraries: the summary of each library, in order.
	// +optional
	Libraries []WCMAdminLibraryResult `json:"libraries,omitempty"`
	// CurrentLibrary being processed (1-based), while it runs.
	// +optional
	CurrentLibrary int `json:"currentLibrary,omitempty"`
	// ReportConfigMap: the ConfigMap with DX's answer for every library (the per item detail is only in the log of
	// the Web Engine).
	// +optional
	ReportConfigMap string `json:"reportConfigMap,omitempty"`
	// OutputRef and OutputDigest: the artifact with the report, when spec.output.oci is set.
	// +optional
	OutputRef string `json:"outputRef,omitempty"`
	// +optional
	OutputDigest string `json:"outputDigest,omitempty"`
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
// +kubebuilder:resource:shortName=dxwcmadmin
// +kubebuilder:printcolumn:name="Task",type=string,JSONPath=`.spec.task`
// +kubebuilder:printcolumn:name="Fix",type=boolean,JSONPath=`.spec.fix`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DXWCMAdminTask runs a maintenance module of Web Content Manager on DX Compose (member fixer, workflow checker), the
// ConfigEngine run-wcm-admin-task-* of traditional DX, once per spec; report only unless spec.fix.
type DXWCMAdminTask struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              DXWCMAdminTaskSpec   `json:"spec"`
	Status            DXWCMAdminTaskStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DXWCMAdminTaskList contains a list of DXWCMAdminTask.
type DXWCMAdminTaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DXWCMAdminTask `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DXWCMAdminTask{}, &DXWCMAdminTaskList{})
		return nil
	})
}
