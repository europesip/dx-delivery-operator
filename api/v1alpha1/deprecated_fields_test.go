// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package v1alpha1

import (
	"reflect"
	"testing"
)

func TestDXConnectionSecretFallbacks(t *testing.T) {
	old := DXConnectionSpec{CredentialsSecret: &LocalObjectReference{Name: "old"}, CASecret: &LocalObjectReference{Name: "ca-old"}}
	if got := old.PortalCredentials(); got != "old" {
		t.Fatalf("PortalCredentials with the deprecated field = %q", got)
	}
	if ca := old.TLSCA(); ca == nil || ca.Name != "ca-old" {
		t.Fatalf("TLSCA with the deprecated field = %v", ca)
	}
	if got, want := old.DeprecatedFields(), []string{"credentialsSecret → portalCredentialsSecret", "caSecret → tlsCASecret"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DeprecatedFields = %v, want %v", got, want)
	}
	both := DXConnectionSpec{PortalCredentialsSecret: &LocalObjectReference{Name: "new"}, CredentialsSecret: &LocalObjectReference{Name: "new"},
		TLSCASecret: &LocalObjectReference{Name: "ca"}}
	if got := both.PortalCredentials(); got != "new" {
		t.Fatalf("PortalCredentials prefers the new field, got %q", got)
	}
	if ca := both.TLSCA(); ca == nil || ca.Name != "ca" {
		t.Fatalf("TLSCA prefers the new field, got %v", ca)
	}
	neu := DXConnectionSpec{PortalCredentialsSecret: &LocalObjectReference{Name: "new"}}
	if len(neu.DeprecatedFields()) != 0 || neu.TLSCA() != nil {
		t.Fatalf("new fields only: no deprecated fields and no CA")
	}
}

func TestDXDatabaseSecretFallbacks(t *testing.T) {
	old := DXDatabaseSpec{AdminSecret: &LocalObjectReference{Name: "adm"}, AdminSecretNamespace: "shared", SecretName: "out"}
	if n, ns := old.DBAdminSecret(); n != "adm" || ns != "shared" {
		t.Fatalf("DBAdminSecret with deprecated fields = %q %q", n, ns)
	}
	if old.Output() != "out" || len(old.DeprecatedFields()) != 3 {
		t.Fatalf("Output %q, deprecated %v", old.Output(), old.DeprecatedFields())
	}
	neu := DXDatabaseSpec{DBAdminCredentialsSecret: &LocalObjectReference{Name: "adm2"}, DBAdminCredentialsSecretNamespace: "db", OutputSecretName: "out2"}
	if n, ns := neu.DBAdminSecret(); n != "adm2" || ns != "db" || neu.Output() != "out2" || len(neu.DeprecatedFields()) != 0 {
		t.Fatalf("new fields: %q %q %q %v", n, ns, neu.Output(), neu.DeprecatedFields())
	}
	if (&DXDatabaseSpec{}).Output() != "" {
		t.Fatalf("no output field: empty means the default")
	}
}
