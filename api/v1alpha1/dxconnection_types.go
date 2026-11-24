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

// DXConnectionSpec is how the operator reaches an HCL DX server.
// +kubebuilder:validation:XValidation:rule="has(self.portalCredentialsSecret) || has(self.credentialsSecret)",message="set portalCredentialsSecret (credentialsSecret is its deprecated name)"
// +kubebuilder:validation:XValidation:rule="!has(self.portalCredentialsSecret) || !has(self.credentialsSecret) || self.portalCredentialsSecret.name == self.credentialsSecret.name",message="portalCredentialsSecret and the deprecated credentialsSecret name different Secrets: set only portalCredentialsSecret"
// +kubebuilder:validation:XValidation:rule="!has(self.tlsCASecret) || !has(self.caSecret) || self.tlsCASecret.name == self.caSecret.name",message="tlsCASecret and the deprecated caSecret name different Secrets: set only tlsCASecret"
type DXConnectionSpec struct {
	// URL of DX as the operator reaches it, for example https://dx.example.com (normally the public host).
	// +kubebuilder:validation:Pattern=`^https?://[^/]+/?$`
	URL string `json:"url"`
	// SourceName: short name of this DX in the artifacts it exports (annotation com.europesip.dx.source.server and
	// EXPORT-INFO.txt), for example wps01 or cf230; the URL goes to com.europesip.dx.source.url. Default: the name of
	// the DXConnection. A name, not a host: internal IPs and host names are not published as the "server".
	// +optional
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`
	SourceName string `json:"sourceName,omitempty"`
	// InternalURL: in-cluster URL of the Web Engine, without Traefik or HAProxy in between, for example
	// http://dx-deployment-web-engine.<namespace>.svc:9080 when the operator runs in the same cluster. The long requests
	// (WCM data module import and export, dxFileTransfer uploads and downloads) go there, so no proxy timeout cuts them
	// (a cut import goes on inside DX and the next one would run at the same time); everything else uses url.
	// +kubebuilder:validation:Pattern=`^https?://[^/]+/?$`
	// +optional
	InternalURL string `json:"internalURL,omitempty"`
	// PortalCredentialsSecret: Secret with the keys "username" and "password" of the DX portal user the operator
	// signs in with (XMLAccess, WCM, DAM, DXClient). Script Applications need Editor access to the Script Application
	// Library; pages (XMLAccess) and DAM need a DX administrator. Required (or the deprecated credentialsSecret).
	// +optional
	PortalCredentialsSecret *LocalObjectReference `json:"portalCredentialsSecret,omitempty"`
	// CredentialsSecret: deprecated, use portalCredentialsSecret (same meaning; still accepted, with a warning).
	// +optional
	CredentialsSecret *LocalObjectReference `json:"credentialsSecret,omitempty"`
	// TLSCASecret: Secret with the key "ca.crt" to verify the TLS certificate of DX (a self-signed certificate or a
	// private CA).
	// +optional
	TLSCASecret *LocalObjectReference `json:"tlsCASecret,omitempty"`
	// CASecret: deprecated, use tlsCASecret (same meaning; still accepted, with a warning).
	// +optional
	CASecret *LocalObjectReference `json:"caSecret,omitempty"`
	// InsecureSkipTLSVerify: do not verify the certificate at all (labs only).
	// +optional
	InsecureSkipTLSVerify bool `json:"insecureSkipTLSVerify,omitempty"`
	// Runtime of the DX server: compose (the Web Engine of DX Compose, default) or core (DX Core, the traditional
	// DX 9.5 on WebSphere Application Server). On core the WCM libraries go through DXConnect (spec.dxConnect), because
	// DX Core has no dxFileTransfer.
	// +kubebuilder:validation:Enum=compose;core
	// +kubebuilder:default=compose
	// +optional
	Runtime string `json:"runtime,omitempty"`
	// DXConnect of DX Core (runtime core): the servlet of the Config Wizard profile that DXClient uses, for the WCM
	// library exports and imports. Default: url, the credentials of credentialsSecret and profile wp_profile.
	// +optional
	DXConnect *DXConnectSpec `json:"dxConnect,omitempty"`
	// ContentHandlerPath of DX (default /wps/mycontenthandler). It also sets the context root: /mycontenthandler for a
	// traditional DX installed with an empty context root (XMLAccess at /config, the login at /j_security_check).
	// +kubebuilder:default="/wps/mycontenthandler"
	// +optional
	ContentHandlerPath string `json:"contentHandlerPath,omitempty"`
	// VirtualPortalContext: push Script Applications into this virtual portal.
	// +optional
	VirtualPortalContext string `json:"virtualPortalContext,omitempty"`
	// ResyncInterval: how often every resource that uses this connection is checked against DX, to detect
	// and correct drift (default 10m, minimum 1m).
	// +kubebuilder:default="10m"
	// +optional
	ResyncInterval *metav1.Duration `json:"resyncInterval,omitempty"`
}

// DXConnectSpec is how the operator reaches DXConnect of a DX Core.
type DXConnectSpec struct {
	// URL of DXConnect: https://<host> in Kubernetes (HAProxy routes /hcl/dxconnect to the core pod) or
	// https://<host>:10202 on a VM. Default: spec.url.
	// +kubebuilder:validation:Pattern=`^https?://[^/]+/?$`
	// +optional
	URL string `json:"url,omitempty"`
	// CWCredentialsSecret of the Config Wizard (cw_profile) administrator ("username", "password"), named like
	// wasCredentialsSecret so that each Secret says whose it is. Default: the credentials of the connection.
	// +optional
	CWCredentialsSecret *LocalObjectReference `json:"cwCredentialsSecret,omitempty"`
	// WASCredentialsSecret of the WebSphere administrator ("username", "password"). Default: the credentials of the
	// connection.
	// +optional
	WASCredentialsSecret *LocalObjectReference `json:"wasCredentialsSecret,omitempty"`
	// ProfileName of the DX profile on the server (default wp_profile).
	// +kubebuilder:default=wp_profile
	// +optional
	ProfileName string `json:"profileName,omitempty"`
}

// DXConnectionStatus is the last check of the connection.
type DXConnectionStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// LastCheckTime of the connection.
	// +optional
	LastCheckTime *metav1.Time `json:"lastCheckTime,omitempty"`
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=dxconn
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.spec.url`
// +kubebuilder:printcolumn:name="Runtime",type=string,JSONPath=`.spec.runtime`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Message",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].message`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DXConnection is a connection to an HCL DX server, shared by the resources of a namespace.
type DXConnection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              DXConnectionSpec   `json:"spec"`
	Status            DXConnectionStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DXConnectionList contains a list of DXConnection.
type DXConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DXConnection `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DXConnection{}, &DXConnectionList{})
		return nil
	})
}

// PortalCredentials is the Secret of the portal user: portalCredentialsSecret, or the deprecated credentialsSecret.
func (s *DXConnectionSpec) PortalCredentials() string {
	if s.PortalCredentialsSecret != nil && s.PortalCredentialsSecret.Name != "" {
		return s.PortalCredentialsSecret.Name
	}
	if s.CredentialsSecret != nil {
		return s.CredentialsSecret.Name
	}
	return ""
}

// TLSCA is the CA Secret: tlsCASecret, or the deprecated caSecret; nil when none.
func (s *DXConnectionSpec) TLSCA() *LocalObjectReference {
	if s.TLSCASecret != nil && s.TLSCASecret.Name != "" {
		return s.TLSCASecret
	}
	return s.CASecret
}

// DeprecatedFields lists the deprecated fields in use, as "old → new".
func (s *DXConnectionSpec) DeprecatedFields() []string {
	var d []string
	if s.CredentialsSecret != nil {
		d = append(d, "credentialsSecret → portalCredentialsSecret")
	}
	if s.CASecret != nil {
		d = append(d, "caSecret → tlsCASecret")
	}
	return d
}
