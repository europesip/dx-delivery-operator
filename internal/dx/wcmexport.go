package dx

// WCM library export on DX Compose (HCL documents it for CF238+): create a dxFileTransfer folder, run the WCM data
// module with taskType=export and output.dir in that folder (one library per call), then download the folder as one
// zip with dxFileTransfer action=download and an empty file parameter.

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DataModuleExport exports one library (by title) of the base portal (vp "") or of a virtual portal into dir (absolute,
// in the DX container, under FileTransferRoot) with the WCM data module. It returns the log of the module as text. Like
// the import, it holds the XMLAccess lock of this DX while it runs.
func (c *Client) DataModuleExport(ctx context.Context, vp, library, dir string, timeout time.Duration) (string, error) {
	if c.long != nil {
		if v, err := c.long.DataModuleExport(ctx, vp, library, dir, timeout); !unreachable(err) { // in-cluster URL: no proxy timeout
			return v, err
		}
	}
	mu, _ := xmlAccessLocks.LoadOrStore(c.lockID(), &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	p := c.wpsRoot() + "/wcm/myconnect"
	if vp != "" {
		p += "/" + url.PathEscape(vp)
	}
	q := url.Values{"MOD": {"data"}, "processLibraries": {"false"}, "taskType": {"export"}, "exportLibrary": {library}, "output.dir": {dir}}
	full := p + "?" + q.Encode()
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
			return "", fmt.Errorf("WCM data module export: %w", err)
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		_ = resp.Body.Close()
		cancel()
		text := strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(string(data), " "))), " ")
		if err := exportOutcome(resp.StatusCode, text); err != nil {
			if attempt == 0 && errors.Is(err, errSignInAgain) {
				continue
			}
			return text, err
		}
		return text, nil
	}
	return "", errors.New("DX session could not be renewed")
}

var errSignInAgain = errors.New("the DX session expired")

// exportOutcome reads the answer of the data module to an export.
func exportOutcome(status int, text string) error {
	low := strings.ToLower(text)
	switch {
	case strings.Contains(text, "Failed to complete") || strings.Contains(text, "RepositoryException"):
		return fmt.Errorf("DX export failed: %s", shortLog(text))
	case strings.Contains(low, "was exported successfully") || strings.Contains(low, "exported successfully") ||
		strings.Contains(low, "export completed") || strings.Contains(low, "completed successfully"):
		return nil
	case status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout:
		return fmt.Errorf("%w (HTTP %d)", ErrCutByProxy, status)
	case status == http.StatusUnauthorized || strings.Contains(low, "log in"):
		return errSignInAgain
	default:
		return fmt.Errorf("unexpected answer of the WCM data module (HTTP %d): %s", status, shortLog(text))
	}
}

// FileTransferDownload downloads the whole folder sub of the dxFileTransfer root as one zip into the file dest
// (streamed). It returns the bytes written.
func (c *Client) FileTransferDownload(ctx context.Context, sub, dest string) (int64, error) {
	if c.long != nil {
		if v, err := c.long.FileTransferDownload(ctx, sub, dest); !unreachable(err) { // in-cluster URL: no proxy timeout
			return v, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+dftPath(c, url.Values{"action": {"download"}, "subDirectory": {sub}, "file": {""}}), nil)
	if err != nil {
		return 0, err
	}
	req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	hc := *c.http
	hc.Jar = nil
	hc.Timeout = 0
	resp, err := hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("download of %s: %w", sub, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("dxFileTransfer download of %s: HTTP %d: %s", sub, resp.StatusCode, shortLog(string(data)))
	}
	f, err := os.Create(dest)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, fmt.Errorf("download of %s: %w", sub, err)
	}
	return n, nil
}

var productRe = regexp.MustCompile(`<!--\s*(.+?)\s+exported on `)

const locateRequest = `<?xml version="1.0" encoding="UTF-8"?>
<request xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="PortalConfig_8.5.0.xsd" type="export">
<portal action="locate"/>
</request>`

// ServerVersion is the product and build that DX writes in the header of an XMLAccess answer, for example "HCL Digital
// Experience Compose/9.5 build 20261103-170843". Empty when it cannot be read.
func (c *Client) ServerVersion(ctx context.Context) string {
	out, err := c.XMLAccess(ctx, locateRequest)
	if err != nil {
		return ""
	}
	return parseServerVersion(out)
}

func parseServerVersion(out string) string {
	if m := productRe.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}
