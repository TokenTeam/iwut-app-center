package transport

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func isTesterManagementRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "v1" || parts[1] != "applications" {
		return false
	}
	return r.Method == http.MethodGet && parts[3] == "tester-management" || r.Method == http.MethodPost && (parts[3] == "tester-memberships:search" || parts[3] == "tester-join-links:search")
}
func validTesterManagementHTTPInput(r *http.Request) bool {
	if r.URL.RawQuery != "" {
		return false
	}
	if r.Method == http.MethodGet {
		return r.ContentLength <= 0 && r.Body == http.NoBody || r.ContentLength == 0 && r.Body == nil
	}
	if r.Body == nil || r.Body == http.NoBody {
		return true
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 16385))
	if err != nil || len(raw) > 16384 {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	for key := range fields {
		switch key {
		case "status", "pageSize", "page_size", "pageToken", "page_token":
		default:
			return false
		}
	}
	// Both protobuf spellings are valid individually; ambiguous duplicates are not.
	if _, ok := fields["pageSize"]; ok {
		if _, duplicate := fields["page_size"]; duplicate {
			return false
		}
	}
	if _, ok := fields["pageToken"]; ok {
		if _, duplicate := fields["page_token"]; duplicate {
			return false
		}
	}
	return true
}
