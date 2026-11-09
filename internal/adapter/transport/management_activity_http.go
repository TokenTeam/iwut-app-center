package transport

import (
	"bytes"
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_management_activity"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/protobuf/encoding/protojson"
	"io"
	ad "iwut-app-center/internal/application/domain"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"
)

func managementActivityHTTPOperation(r *http.Request) string {
	rest, ok := strings.CutPrefix(r.URL.Path, "/v1/applications/")
	if !ok {
		return ""
	}
	_, tail, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}
	if tail == "management-activity:search" {
		return pb.OperationApplicationManagementActivityServiceListApplicationManagementActivity
	}
	if strings.HasPrefix(tail, "management-activity/") && !strings.Contains(strings.TrimPrefix(tail, "management-activity/"), "/") {
		return pb.OperationApplicationManagementActivityServiceGetApplicationManagementActivity
	}
	return ""
}
func managementActivityHTTPFilter(verifier *IdentityVerifier) khttp.FilterFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			op := managementActivityHTTPOperation(r)
			if op == "" {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Cache-Control", "private, no-store")
			if op == pb.OperationApplicationManagementActivityServiceListApplicationManagementActivity && r.Method != http.MethodPost || op == pb.OperationApplicationManagementActivityServiceGetApplicationManagementActivity && r.Method != http.MethodGet {
				next.ServeHTTP(w, r)
				return
			}
			if _, err := verifyScriptHTTPIdentity(r, verifier); err != nil {
				managementActivityHTTPError(w, r, toIdentityTransportError(err, op))
				return
			}
			if !validManagementActivityHTTPBody(w, r) {
				managementActivityHTTPError(w, r, activityTransportError(r.Context(), ad.ErrInvalidManagementActivityQuery))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
func validManagementActivityHTTPBody(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		return false
	}
	if r.Method == http.MethodGet {
		if r.Body == nil {
			return true
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1))
		return err == nil && len(raw) == 0
	}
	if r.Body == nil {
		return false
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return false
	}
	reader := http.MaxBytesReader(w, r.Body, 16*1024)
	defer reader.Close()
	raw, err := io.ReadAll(reader)
	trimmed := bytes.TrimSpace(raw)
	if err != nil || !utf8.Valid(raw) || len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	var query pb.ListApplicationManagementActivityQuery
	if protojson.Unmarshal(raw, &query) != nil {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return true
}
func managementActivityHTTPError(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "private, no-store")
	e := kerrors.FromError(err)
	if _, ok := pb.ErrorReason_value[e.Reason]; !ok {
		err = activityTransportError(r.Context(), ad.ErrManagementActivityStateInconsistent)
		if e.Code == 400 {
			err = activityTransportError(r.Context(), ad.ErrInvalidManagementActivityQuery)
		}
	}
	khttp.DefaultErrorEncoder(w, r, err)
}
