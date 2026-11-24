// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package controller

import (
	"strings"
	"testing"

	"github.com/europesip/dx-delivery-operator/internal/dx"
)

// The realm of a new virtual portal (#42): the spec, else an existing virtual portal, else FederatedRealm (DX Compose
// with LDAP) and then defaultWIMFileBasedRealm.
func TestVPRealms(t *testing.T) {
	cases := []struct {
		spec     string
		existing []dx.VirtualPortal
		want     string
	}{
		{"myRealm", []dx.VirtualPortal{{Realm: "other"}}, "myRealm"},
		{"", []dx.VirtualPortal{{Context: "a"}, {Context: "b", Realm: "LdapRealm"}}, "LdapRealm"},
		{"", nil, "FederatedRealm,defaultWIMFileBasedRealm"},
	}
	for _, c := range cases {
		if got := strings.Join(vpRealms(c.spec, c.existing), ","); got != c.want {
			t.Errorf("vpRealms(%q, %v) = %s, want %s", c.spec, c.existing, got, c.want)
		}
	}
}
