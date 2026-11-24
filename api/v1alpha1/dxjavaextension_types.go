// SPDX-License-Identifier: MIT

/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Java extension types (issue #54).
const (
	JavaExtensionPortlet       = "portlet"
	JavaExtensionSharedLibrary = "sharedLibrary"
)

// Delivery modes of the Java extensions (issue #72): how the files reach the Web Engine container.
const (
	// DeliveryWebhook (default): the operator's admission webhook adds an init container to the Web Engine pod.
	DeliveryWebhook = "webhook"
	// DeliveryStartupHook: a script in the HCL customer startup folder of the Web Engine image
	// (StartupHookDir, a volume of the HCL chart value volumes.webEngine.customPVCs) downloads the extensions
	// through the operator on every container start, before the final Liberty start. No webhook.
	DeliveryStartupHook = "startupHook"
	// StartupHookDir: the folder whose *.sh scripts the entrypoint of the HCL Web Engine image runs on every
	// container start (svrcfg/bin/customer/startup, CF239).
	StartupHookDir = "/opt/openliberty/wlp/usr/svrcfg/bin/customer/startup"
	// AnnotationStartupHookSeeded: set by the operator on the startup volume (PVC) with the hash of the script it
	// wrote there.
	AnnotationStartupHookSeeded = "dx.europesip.com/java-extensions-startup-hook"
)

// Conditions of a DXJavaExtension (besides Ready).
const (
	// ConditionArtifactResolved: the artifact reference was resolved to a digest in the registry.
	ConditionArtifactResolved = "ArtifactResolved"
	// ConditionRolledOut: every Web Engine pod runs with the desired digest.
	ConditionRolledOut = "RolledOut"
	// ConditionRegistered: the portlets are registered in DX (XMLAccess predeployed).
	ConditionRegistered = "Registered"
)

// Annotations used by the Java extensions.
const (
	// AnnotationJavaExtensionsHash: set by the operator on the pod template of the Web Engine StatefulSet. Its
	// change rolls the Web Engine out (one pod at a time, waiting for Ready).
	AnnotationJavaExtensionsHash = "dx.europesip.com/java-extensions-hash"
	// AnnotationJavaExtensions: set by the webhook on every Web Engine pod it changes: name@digest of each
	// extension injected (for people and for the operator).
	AnnotationJavaExtensions = "dx.europesip.com/java-extensions"
	// LabelJavaExtensionsNamespace: the namespaces where the webhook acts (value "enabled").
	LabelJavaExtensionsNamespace = "dx.europesip.com/java-extensions"
)

// JavaArtifact is the OCI artifact with the extension.
type JavaArtifact struct {
	// Ref of the artifact, for example harbor.example.com/dx-artifacts/java-extensions/portlets/my-portlet:1.0.0 or
	// ...@sha256:... The artifact has one file: a WAR (or a tar/tar.gz of the expanded WAR) for a portlet, a JAR
	// (or a tar/tar.gz of JARs) for a shared library. A tag is resolved to its digest before any rollout, and the
	// Web Engine always loads that digest.
	// +kubebuilder:validation:MinLength=1
	Ref string `json:"ref"`
	// PullSecrets: Secrets of type kubernetes.io/dockerconfigjson in this namespace for a private registry
	// (read-only access is enough). The init container of the Web Engine uses them too.
	// +optional
	PullSecrets []LocalObjectReference `json:"pullSecrets,omitempty"`
}

// JavaExtensionTarget is the Web Engine that loads the extension.
type JavaExtensionTarget struct {
	// StatefulSet of the Web Engine in this namespace (the HCL chart names it <release>-web-engine).
	// +kubebuilder:default="dx-deployment-web-engine"
	// +optional
	StatefulSet string `json:"statefulSet,omitempty"`
	// Container of the Web Engine in that StatefulSet.
	// +kubebuilder:default="web-engine"
	// +optional
	Container string `json:"container,omitempty"`
}

// JavaExtensionPage places the portlet on a page (created or updated with XMLAccess, idempotent).
type JavaExtensionPage struct {
	// UniqueName of the page.
	// +kubebuilder:validation:MinLength=1
	UniqueName string `json:"uniqueName"`
	// Title of the page (every locale in titles takes precedence).
	// +kubebuilder:validation:MinLength=1
	Title string `json:"title"`
	// Titles per locale, for example {"es": "Escaparate"}.
	// +optional
	Titles map[string]string `json:"titles,omitempty"`
	// ParentUniqueName: the parent page (default ibm.portal.Home, the Home label).
	// +kubebuilder:default="ibm.portal.Home"
	// +optional
	ParentUniqueName string `json:"parentUniqueName,omitempty"`
	// FriendlyName: the URL segment of the page (…/wps/portal/<parent>/<friendlyName>).
	// +optional
	FriendlyName string `json:"friendlyName,omitempty"`
	// Anonymous: anonymous users can see the page and the portlet (default true).
	// +kubebuilder:default=true
	// +optional
	Anonymous *bool `json:"anonymous,omitempty"`
}

// JavaExtensionIdentifiers: identifiers of the registration in DX that are the same in every environment
// (development, staging, production), so pages and XMLAccess requests that reference a portlet keep working when
// they move between portals (issue #69). The uid of the web module and of the portlet application already come
// from portlet.xml; this adds a unique name to every portlet. Object IDs (Z1_/Z2_/Z3_…) are generated by DX in
// each environment and cannot be fixed for predeployed portlets (tested on CF239): they are shown in the status.
type JavaExtensionIdentifiers struct {
	// UniqueNames: register every portlet with the unique name <portlet-app uid>.<portlet-name>, derived from
	// portlet.xml (default true). Registrations made before get it on the next reconcile.
	// +kubebuilder:default=true
	// +optional
	UniqueNames *bool `json:"uniqueNames,omitempty"`
}

// RegisteredPortletID is a portlet as DX has it.
type RegisteredPortletID struct {
	// Name: the <portlet-name> in portlet.xml.
	Name string `json:"name"`
	// UniqueName in DX.
	// +optional
	UniqueName string `json:"uniqueName,omitempty"`
	// ObjectID in DX.
	// +optional
	ObjectID string `json:"objectID,omitempty"`
}

// DXJavaExtensionSpec declares a Java extension of the DX Compose Web Engine.
type DXJavaExtensionSpec struct {
	// ConnectionRef: the DXConnection of the portal (used to register the portlets with XMLAccess).
	ConnectionRef LocalObjectReference `json:"connectionRef"`
	// Type: portlet (a WAR with portlet.xml: loaded by Liberty and registered in DX) or sharedLibrary (JARs on
	// the global classpath of the Web Engine, libraries/customSharedLib).
	// +kubebuilder:validation:Enum=portlet;sharedLibrary
	// +kubebuilder:default=portlet
	// +optional
	Type string `json:"type,omitempty"`
	// Artifact: the OCI artifact with the WAR or the JARs.
	Artifact JavaArtifact `json:"artifact"`
	// FileName of the WAR in customApplications (default <metadata.name>.war) or of the JAR in customSharedLib
	// (default <metadata.name>.jar).
	// +optional
	FileName string `json:"fileName,omitempty"`
	// ContextRoot of the web application (portlets; default /<metadata.name>). Must start with a slash.
	// +kubebuilder:validation:Pattern=`^/[A-Za-z0-9._~/-]*$`
	// +optional
	ContextRoot string `json:"contextRoot,omitempty"`
	// Target: the Web Engine (defaults of the HCL chart: StatefulSet dx-deployment-web-engine, container web-engine).
	// +optional
	Target JavaExtensionTarget `json:"target,omitempty"`
	// Register the portlets in DX with XMLAccess predeployed (default true; portlets only). Only done when they are
	// not registered yet or when the portlets of portlet.xml change.
	// +kubebuilder:default=true
	// +optional
	Register *bool `json:"register,omitempty"`
	// PortletAppUID: uid of the portlet application in DX (default: the id attribute of <portlet-app> in
	// portlet.xml, or the annotation com.europesip.dx.java.webapp-uid of the artifact without ".webmod").
	// +optional
	PortletAppUID string `json:"portletAppUID,omitempty"`
	// Page: optional page where the first portlet is placed.
	// +optional
	Page *JavaExtensionPage `json:"page,omitempty"`
	// Identifiers: the same identifiers in every environment (portlet unique names; issue #69).
	// +optional
	Identifiers *JavaExtensionIdentifiers `json:"identifiers,omitempty"`
	// Optional: when the artifact cannot be pulled while a Web Engine pod starts, start without it (a warning in
	// the init container log) instead of failing the pod.
	// +optional
	Optional bool `json:"optional,omitempty"`
	// RolloutTimeout: how long a rollout may take before the operator goes back to the previous digest
	// (default 30m).
	// +optional
	RolloutTimeout *metav1.Duration `json:"rolloutTimeout,omitempty"`
	// Delivery: how the files reach the Web Engine (issue #72). webhook (default): the operator's admission webhook
	// adds an init container to the Web Engine pod (namespace label dx.europesip.com/java-extensions=enabled).
	// startupHook: a script that the operator writes on a volume mounted on the HCL customer startup folder
	// (chart value volumes.webEngine.customPVCs, mountPath /opt/openliberty/wlp/usr/svrcfg/bin/customer/startup)
	// downloads them through the operator on every container start; no webhook, and the namespace must NOT have
	// the webhook label. All the extensions of one Web Engine must use the same mode.
	// +kubebuilder:validation:Enum=webhook;startupHook
	// +kubebuilder:default=webhook
	// +optional
	Delivery string `json:"delivery,omitempty"`
}

// DXJavaExtensionStatus is what the operator saw and did.
type DXJavaExtensionStatus struct {
	// ResolvedDigest: the digest of artifact.ref the last time it was resolved.
	// +optional
	ResolvedDigest string `json:"resolvedDigest,omitempty"`
	// DesiredRef: repository@digest that the Web Engine pods must load (the webhook injects this one).
	// +optional
	DesiredRef string `json:"desiredRef,omitempty"`
	// PreviousRef: repository@digest before the last change (the rollback target).
	// +optional
	PreviousRef string `json:"previousRef,omitempty"`
	// RolledOutRef: repository@digest every Web Engine pod runs with after the last finished rollout.
	// +optional
	RolledOutRef string `json:"rolledOutRef,omitempty"`
	// FailedRef: repository@digest whose rollout failed and was rolled back; it is not rolled out again until the
	// spec changes.
	// +optional
	FailedRef string `json:"failedRef,omitempty"`
	// RolloutStarted: when the current rollout started.
	// +optional
	RolloutStarted *metav1.Time `json:"rolloutStarted,omitempty"`
	// PortletAppUID registered in DX.
	// +optional
	PortletAppUID string `json:"portletAppUID,omitempty"`
	// Portlets registered in DX (names from portlet.xml).
	// +optional
	Portlets []string `json:"portlets,omitempty"`
	// WebModuleObjectID: object ID of the web module in DX.
	// +optional
	WebModuleObjectID string `json:"webModuleObjectID,omitempty"`
	// PortletAppObjectID: object ID of the portlet application in DX.
	// +optional
	PortletAppObjectID string `json:"portletAppObjectID,omitempty"`
	// PortletIDs: unique name (the same in every environment) and object ID (generated by DX, different in each
	// environment) of every registered portlet.
	// +optional
	PortletIDs []RegisteredPortletID `json:"portletIDs,omitempty"`
	// StartupHookClaim: the PVC mounted on the HCL customer startup folder (delivery startupHook).
	// +optional
	StartupHookClaim string `json:"startupHookClaim,omitempty"`
	// RegisteredHash: hash of what was registered (uid, portlets, context root, page); registration runs again
	// only when it changes.
	// +optional
	RegisteredHash string `json:"registeredHash,omitempty"`
	// ObservedGeneration: the generation of the spec that the status describes.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions: Ready, ArtifactResolved, RolledOut, Registered.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=dxjx;dxjavaext
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Artifact",type=string,JSONPath=`.spec.artifact.ref`
// +kubebuilder:printcolumn:name="Delivery",type=string,JSONPath=`.spec.delivery`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DXJavaExtension is a Java extension (a portlet WAR or shared-library JARs) loaded from a registry onto the
// official DX Compose Web Engine image: no custom image (issue #54).
type DXJavaExtension struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DXJavaExtensionSpec   `json:"spec,omitempty"`
	Status DXJavaExtensionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DXJavaExtensionList contains a list of DXJavaExtension.
type DXJavaExtensionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DXJavaExtension `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DXJavaExtension{}, &DXJavaExtensionList{})
		return nil
	})
}

// Defaults applied by the operator too (the API server applies the kubebuilder defaults).

// StatefulSetName of the target Web Engine.
func (e *DXJavaExtension) StatefulSetName() string {
	if e.Spec.Target.StatefulSet != "" {
		return e.Spec.Target.StatefulSet
	}
	return "dx-deployment-web-engine"
}

// UniqueNames: whether portlets are registered with their stable unique name (default true).
func (e *DXJavaExtension) UniqueNames() bool {
	return e.Spec.Identifiers == nil || e.Spec.Identifiers.UniqueNames == nil || *e.Spec.Identifiers.UniqueNames
}

// ContainerName of the target Web Engine container.
func (e *DXJavaExtension) ContainerName() string {
	if e.Spec.Target.Container != "" {
		return e.Spec.Target.Container
	}
	return "web-engine"
}

// KindOf the extension (portlet or sharedLibrary).
func (e *DXJavaExtension) KindOf() string {
	if e.Spec.Type == "" {
		return JavaExtensionPortlet
	}
	return e.Spec.Type
}

// File name of the WAR (portlet) or JAR (shared library).
func (e *DXJavaExtension) File() string {
	if e.Spec.FileName != "" {
		return e.Spec.FileName
	}
	if e.KindOf() == JavaExtensionSharedLibrary {
		return e.Name + ".jar"
	}
	return e.Name + ".war"
}

// Context root of the web application (portlets).
func (e *DXJavaExtension) Context() string {
	if e.Spec.ContextRoot != "" {
		return e.Spec.ContextRoot
	}
	return "/" + e.Name
}

// DeliveryMode of the extension (webhook by default; issue #72).
func (e *DXJavaExtension) DeliveryMode() string {
	if e.Spec.Delivery == DeliveryStartupHook {
		return DeliveryStartupHook
	}
	return DeliveryWebhook
}

// Registers says whether the portlets are registered in DX.
func (e *DXJavaExtension) Registers() bool {
	return e.KindOf() == JavaExtensionPortlet && (e.Spec.Register == nil || *e.Spec.Register)
}
