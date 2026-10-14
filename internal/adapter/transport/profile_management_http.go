package transport

import (
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_revision"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func isProfileManagementOperation(operation string) bool {
	return operation == pb.OperationApplicationProfileRevisionListApplicationProfileRevisions || operation == pb.OperationApplicationProfileRevisionGetApplicationProfileRevision
}
func profileManagementHTTPKind(r *http.Request) string {
	if r.Method != http.MethodGet {
		return ""
	}
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if (len(p) == 4 || len(p) == 5) && p[0] == "v1" && p[1] == "applications" && p[3] == "profile-revisions" {
		if len(p) == 4 {
			return "LIST"
		}
		return "GET"
	}
	return ""
}
func validProfileManagementHTTPInput(r *http.Request, kind string) bool {
	if r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		return false
	}
	if r.Body != nil && r.Body != http.NoBody {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1))
		if err != nil || len(raw) > 0 {
			return false
		}
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return false
	}
	allowed := map[string]bool{"reviewStatus": true, "pageSize": true, "pageToken": true}
	if kind == "GET" {
		allowed = map[string]bool{"reviewPageSize": true, "reviewPageToken": true}
	}
	for key, values := range query {
		if !allowed[key] || len(values) != 1 {
			return false
		}
		if key == "pageSize" || key == "reviewPageSize" {
			n, err := strconv.ParseInt(values[0], 10, 32)
			if err != nil || n < 1 || n > 100 {
				return false
			}
		}
	}
	return true
}
