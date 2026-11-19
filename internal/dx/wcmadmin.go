package dx

// WCM maintenance modules of DX Compose over HTTP (/wps/wcm/myconnect[/<vp>]?MOD=MemberFixer|WorkflowControlChecker),
// the run-wcm-admin-task-* of ConfigEngine on traditional DX. Verified on CF239 (19-nov-2026): both modules answer
// with an HTML page "Log for Utility: ..." with the options echoed, a SUMMARY INFORMATION block (member fixer) and
// "Utility completed."; the per item detail is only written to the log of the Web Engine. Without fix=true they run in
// report mode and change nothing.

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AdminTaskModule runs one WCM module with the query q (MOD is set from mod) on the base portal (vp "") or a virtual
// portal and returns DX's answer as text. It holds the XMLAccess lock of this DX, so it never overlaps a data module
// import or an XMLAccess request.
func (c *Client) AdminTaskModule(ctx context.Context, vp, mod string, q url.Values, timeout time.Duration) (string, error) {
	mu, _ := xmlAccessLocks.LoadOrStore(c.cfg.BaseURL, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	p := c.wpsRoot() + "/wcm/myconnect"
	if vp != "" {
		p += "/" + url.PathEscape(vp)
	}
	qq := url.Values{}
	for k, v := range q {
		qq[k] = v
	}
	qq.Set("MOD", mod)
	full := p + "?" + qq.Encode()
	for attempt := 0; attempt < 2; attempt++ {
		c.mu.Lock()
		err := c.loginLocked(ctx, attempt > 0)
		c.mu.Unlock()
		if err != nil {
			return "", err
		}
		rctx, cancel := context.WithTimeout(ctx, timeout)
		req, _ := http.NewRequestWithContext(rctx, http.MethodGet, c.cfg.BaseURL+full, nil)
		hc := *c.http
		hc.Timeout = 0
		resp, err := hc.Do(req)
		if err != nil {
			cancel()
			if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "EOF") {
				return "", fmt.Errorf("%w (%v)", ErrCutByProxy, err)
			}
			return "", fmt.Errorf("WCM module %s: %w", mod, err)
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		_ = resp.Body.Close()
		cancel()
		text := strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(string(data), " "))), " ")
		if err := adminOutcome(resp.StatusCode, text); err != nil {
			if attempt == 0 && errors.Is(err, errSignInAgain) {
				continue
			}
			return text, err
		}
		return text, nil
	}
	return "", errors.New("DX session could not be renewed")
}

func adminOutcome(status int, text string) error {
	low := strings.ToLower(text)
	switch {
	case strings.Contains(text, "Failed to complete") || strings.Contains(text, "RepositoryException"):
		return fmt.Errorf("DX: %s", shortLog(text))
	case strings.Contains(low, "utility completed"):
		return nil
	case status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout:
		return fmt.Errorf("%w (HTTP %d)", ErrCutByProxy, status)
	case status == http.StatusUnauthorized || strings.Contains(low, "log in"):
		return errSignInAgain
	default:
		return fmt.Errorf("unexpected answer of the WCM module (HTTP %d): %s", status, shortLog(text))
	}
}

// AdminSummary is the SUMMARY INFORMATION of a module answer (zero when DX did not print it).
type AdminSummary struct {
	ItemsProcessed, ItemsUpdated, ReferencesToRemove, ReferencesToUpdate, VirtualMembersToUpdate, Errors int
	// Problem: a line of the answer that says the module did nothing useful ("No parameter is specified ...").
	Problem string
}

var (
	sumRe = map[string]*regexp.Regexp{
		"processed": regexp.MustCompile(`Items Processed: (\d+)`),
		"updated":   regexp.MustCompile(`Items Updated and Saved: (\d+)`),
		"remove":    regexp.MustCompile(`Member References requiring Removal: (\d+)`),
		"update":    regexp.MustCompile(`Member References requiring Update: (\d+)`),
		"virtual":   regexp.MustCompile(`Virtual Members requiring Update: (\d+)`),
		"errors":    regexp.MustCompile(`Errors Detected: (\d+)`),
	}
	noParamRe = regexp.MustCompile(`No parameter is specified[^.]*\.?`)
)

// ParseAdminSummary reads the counters of a module answer.
func ParseAdminSummary(text string) AdminSummary {
	n := func(k string) int {
		if m := sumRe[k].FindStringSubmatch(text); m != nil {
			v, _ := strconv.Atoi(m[1])
			return v
		}
		return 0
	}
	s := AdminSummary{ItemsProcessed: n("processed"), ItemsUpdated: n("updated"), ReferencesToRemove: n("remove"),
		ReferencesToUpdate: n("update"), VirtualMembersToUpdate: n("virtual"), Errors: n("errors")}
	if m := noParamRe.FindString(text); m != "" {
		s.Problem = m
	}
	return s
}
