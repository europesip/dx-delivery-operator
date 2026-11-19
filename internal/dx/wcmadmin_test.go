package dx

import "testing"

// Answers of CF239 (demorbita, 19-nov-2026, report mode).
const memberFixerAnswer = `IBM Web Content Manager Utility: Member Fixer Module Log for Utility: Member Fixer Module Starting utility: Member Fixer Module Check PortalServer log files for the detailed log of the module from 11/19/26, 1:55 PM Library being processed: orbita Preserve dates option: False Report only mode Force unlock option: True Restrict on option: none Alternate DN option: UNSPECIFIED Invalid DN option: UNSPECIFIED Mismatched User Repository Unique Id Option: UNSPECIFIED No realm DN option: False SUMMARY INFORMATION Library processed: orbita Items Processed: 9 Items Updated and Saved: 0 Member References requiring Removal: 2 Member References requiring Update: 1 Virtual Members requiring Update: 0 Errors Detected: 0 Utility completed.`

const workflowNoParamAnswer = `IBM Web Content Manager Utility: Workflow Control Checker Module Log for Utility: Workflow Control Checker Module Starting utility: Workflow Control Checker Module Check PortalServer log files for the detailed log of the module from 11/19/26, 1:55 PM Library being processed: orbita Preserve dates option: False Report only mode Force unlock option: True Restrict on option: none No parameter is specified, please use 'rescheduleActions' to reschedule action and/or 'updateDocSecurity' to update the workflow security. Utility completed.`

func TestParseAdminSummary(t *testing.T) {
	s := ParseAdminSummary(memberFixerAnswer)
	if s.ItemsProcessed != 9 || s.ItemsUpdated != 0 || s.ReferencesToRemove != 2 || s.ReferencesToUpdate != 1 || s.Errors != 0 || s.Problem != "" {
		t.Errorf("member fixer summary: %+v", s)
	}
	w := ParseAdminSummary(workflowNoParamAnswer)
	if w.Problem == "" || w.ItemsProcessed != 0 {
		t.Errorf("workflow checker without action: %+v", w)
	}
}

func TestAdminOutcome(t *testing.T) {
	if err := adminOutcome(200, memberFixerAnswer); err != nil {
		t.Errorf("completed answer: %v", err)
	}
	if err := adminOutcome(504, "Gateway Timeout"); err == nil {
		t.Error("504 must be an error (cut by a proxy)")
	}
	if err := adminOutcome(200, "Failed to complete: com.aptrix.pluto.util.RepositoryException: x"); err == nil {
		t.Error("a RepositoryException must be an error")
	}
	if err := adminOutcome(200, "Please log in"); err != errSignInAgain {
		t.Errorf("login page: %v", err)
	}
}
