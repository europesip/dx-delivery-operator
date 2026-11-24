// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package dx

import (
	"errors"
	"net/http"
	"testing"
)

func TestExportOutcome(t *testing.T) {
	ok := `IBM Web Content Manager Utility: DataModule Log for Utility: DataModule Starting utility: DataModule ... The library "orbita" was exported successfully. Utility completed.`
	if err := exportOutcome(200, ok); err != nil {
		t.Fatal(err)
	}
	bad := `Failed to complete: com.aptrix.pluto.util.RepositoryException: Message: Could not find library named X`
	if err := exportOutcome(200, bad); err == nil {
		t.Fatal("failure accepted")
	}
	if err := exportOutcome(http.StatusGatewayTimeout, ""); !errors.Is(err, ErrCutByProxy) {
		t.Fatalf("504: %v", err)
	}
	if err := exportOutcome(200, "Please log in"); !errors.Is(err, errSignInAgain) {
		t.Fatalf("login: %v", err)
	}
}

func TestParseServerVersion(t *testing.T) {
	out := "<?xml version=\"1.0\"?>\n<!-- HCL Digital Experience Compose/9.5 build 20261103-170843 exported on Thu Nov 19 07:10:51 CET 2026 from x -->\n<request/>"
	if v := parseServerVersion(out); v != "HCL Digital Experience Compose/9.5 build 20261103-170843" {
		t.Fatalf("%q", v)
	}
}
