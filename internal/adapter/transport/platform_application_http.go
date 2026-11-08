package transport

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_platform_query"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/protobuf/encoding/protojson"
	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/usecase"
)

func platformApplicationHTTPOperation(r *http.Request) string {
	if r.URL.Path == platformApplicationSearchPath {
		return pb.OperationApplicationPlatformQueryServiceSearchPlatformApplications
	}
	if strings.HasPrefix(r.URL.Path, platformApplicationDetailPrefix) && !strings.Contains(strings.TrimPrefix(r.URL.Path, platformApplicationDetailPrefix), "/") {
		return pb.OperationApplicationPlatformQueryServiceGetPlatformApplicationSummary
	}
	return ""
}
func platformApplicationHTTPFilter(verifier *IdentityVerifier) khttp.FilterFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			op := platformApplicationHTTPOperation(r)
			if op == "" {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Cache-Control", "private, no-store")
			if (op == pb.OperationApplicationPlatformQueryServiceSearchPlatformApplications && r.Method != http.MethodPost) || (op == pb.OperationApplicationPlatformQueryServiceGetPlatformApplicationSummary && r.Method != http.MethodGet) {
				next.ServeHTTP(w, r)
				return
			}
			identity, err := verifyScriptHTTPIdentity(r, verifier)
			if err != nil {
				platformApplicationHTTPError(w, r, toIdentityTransportError(err, op))
				return
			}
			if err = usecase.AuthorizePlatformApplicationQuery(identity); err != nil {
				platformApplicationHTTPError(w, r, platformApplicationError(err))
				return
			}
			if !validPlatformApplicationHTTPBody(w, r) {
				platformApplicationHTTPError(w, r, platformApplicationError(domain.ErrInvalidPlatformApplicationQuery))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
func validPlatformApplicationHTTPBody(w http.ResponseWriter, r *http.Request) bool {
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
	var q pb.SearchPlatformApplicationsQuery
	if protojson.Unmarshal(raw, &q) != nil {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return true
}
func platformApplicationHTTPError(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "private, no-store")
	e := kerrors.FromError(err)
	if _, ok := pb.ErrorReason_value[e.Reason]; !ok && e.Code == 400 {
		err = platformApplicationError(domain.ErrInvalidPlatformApplicationQuery)
	}
	khttp.DefaultErrorEncoder(w, r, err)
}
