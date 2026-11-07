package transport

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	v1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_distribution"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"iwut-app-center/internal/script/domain"
)

func scriptDistributionHTTPRoute(r *http.Request) (operation, method, allowedQuery string) {
	parts := strings.Split(r.URL.Path, "/")
	if r.URL.Path == "/v1/scripts" {
		return v1.OperationScriptDistributionServiceListPublishedScripts, http.MethodGet, "limit cursor"
	}
	if len(parts) == 7 && parts[1] == "v1" && parts[2] == "scripts" && parts[3] != "" && parts[4] == "versions" && parts[5] != "" && parts[6] == "content" {
		return v1.OperationScriptDistributionServiceGetPublishedScriptContent, http.MethodGet, "expectedDigest"
	}
	if len(parts) >= 7 && parts[1] == "v1" && parts[2] == "applications" && parts[3] != "" && parts[4] == "versions" && parts[5] != "" {
		if len(parts) == 7 && parts[6] == "scripts:resolve" {
			return v1.OperationScriptDistributionServiceResolveApplicationScriptManifest, http.MethodPost, ""
		}
		if len(parts) == 8 && parts[6] == "scripts" && strings.HasSuffix(parts[7], ":getContent") && strings.TrimSuffix(parts[7], ":getContent") != "" {
			return v1.OperationScriptDistributionServiceGetApplicationScriptContent, http.MethodPost, ""
		}
	}
	return "", "", ""
}

func scriptDistributionHTTPFilter(verifier *IdentityVerifier) khttp.FilterFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			operation, method, allowed := scriptDistributionHTTPRoute(r)
			if operation == "" {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Cache-Control", "private, no-store")
			if r.Method != method {
				next.ServeHTTP(w, r)
				return
			}
			if _, err := verifyScriptHTTPIdentity(r, verifier); err != nil {
				khttp.DefaultErrorEncoder(w, r, toIdentityTransportError(err, operation))
				return
			}
			valid := true
			if method == http.MethodGet {
				values, err := url.ParseQuery(r.URL.RawQuery)
				valid = err == nil
				for key, items := range values {
					if !slices.Contains(strings.Fields(allowed), key) || len(items) != 1 {
						valid = false
					}
				}
				if r.Body != nil && r.Body != http.NoBody {
					raw, err := io.ReadAll(io.LimitReader(r.Body, 1))
					_ = r.Body.Close()
					if err != nil || len(raw) != 0 {
						valid = false
					}
					r.Body = http.NoBody
				}
			} else {
				valid = validScriptDistributionBody(w, r, operation)
			}
			if !valid {
				khttp.DefaultErrorEncoder(w, r, scriptDistributionError(domain.ErrInvalidDistributionRequest))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func validScriptDistributionBody(w http.ResponseWriter, r *http.Request, operation string) bool {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.Body == nil {
		return false
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return false
	}
	reader := http.MaxBytesReader(w, r.Body, 16*1024)
	defer reader.Close()
	raw, err := io.ReadAll(reader)
	if err != nil || !utf8.Valid(raw) {
		return false
	}
	var input proto.Message
	switch operation {
	case v1.OperationScriptDistributionServiceResolveApplicationScriptManifest:
		input = &v1.ResolveApplicationScriptManifestInput{}
	case v1.OperationScriptDistributionServiceGetApplicationScriptContent:
		input = &v1.GetApplicationScriptContentInput{}
	default:
		return false
	}
	if protojson.Unmarshal(raw, input) != nil {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return true
}

func scriptDistributionHTTPError(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "private, no-store")
	converted := kerrors.FromError(err)
	if _, ok := v1.ErrorReason_value[converted.Reason]; !ok && converted.Code == http.StatusBadRequest {
		err = scriptDistributionError(domain.ErrInvalidDistributionRequest)
	}
	khttp.DefaultErrorEncoder(w, r, err)
}
