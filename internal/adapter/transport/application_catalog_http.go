package transport

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	applicationcatalogv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_catalog"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/grpc/codes"
)

func isPublicCatalogRequest(r *http.Request) bool {
	if r == nil || r.Method != http.MethodPost {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	return (len(parts) == 3 && parts[0] == "v1" && parts[1] == "catalog" && parts[2] == "applications:search") || (len(parts) == 4 && parts[0] == "v1" && parts[1] == "catalog" && parts[2] == "applications" && strings.HasSuffix(parts[3], ":get"))
}
func isPublicCatalogListRequest(r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	return r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "v1" && parts[1] == "catalog" && parts[2] == "applications:search"
}
func validApplicationCatalogHTTPInput(r *http.Request) bool {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.Body == nil {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024+1))
	if err != nil || len(body) > 4*1024*1024 {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var root map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(body))
	if decoder.Decode(&root) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return false
	}
	if isPublicCatalogListRequest(r) {
		if !onlyKeys(root, "runtime", "pageSize", "page_size", "pageToken", "page_token") {
			return false
		}
		var runtime map[string]json.RawMessage
		if json.Unmarshal(root["runtime"], &runtime) != nil || !onlyKeys(runtime, "hostRpcApiMajor", "host_rpc_api_major", "hostCapabilities", "host_capabilities") {
			return false
		}
		return true
	}
	return onlyKeys(root, "hostRpcApiMajor", "host_rpc_api_major", "hostCapabilities", "host_capabilities")
}
func onlyKeys(values map[string]json.RawMessage, allowed ...string) bool {
	set := map[string]bool{}
	for _, key := range allowed {
		set[key] = true
	}
	for key := range values {
		if !set[key] {
			return false
		}
	}
	return true
}
func invalidApplicationCatalogRequest() error {
	return transportStatus(codes.InvalidArgument, ReasonInvalidApplicationCatalogRequest, "application catalog request is invalid")
}
func applicationCatalogSafeErrorEncoder(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "private, no-store")
	converted := kerrors.FromError(err)
	if _, ok := applicationcatalogv1.ErrorReason_value[converted.Reason]; !ok {
		if converted.Code == http.StatusBadRequest {
			err = invalidApplicationCatalogRequest()
		} else {
			err = transportStatus(codes.Internal, ReasonInternal, "internal failure")
		}
		converted = kerrors.FromError(err)
	}
	khttp.DefaultErrorEncoder(w, r, converted)
}
