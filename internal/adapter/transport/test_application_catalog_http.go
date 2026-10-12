package transport

import (
	"bytes"
	"encoding/json"
	v1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/test_application_catalog"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"io"
	cd "iwut-app-center/internal/catalog/domain"
	"net/http"
	"strings"
)

func isMyTestApplicationRequest(r *http.Request) bool {
	if r == nil || r.Method != http.MethodPost {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	return r.URL.Path == "/v1/test-applications:search" || (len(parts) == 3 && parts[0] == "v1" && parts[1] == "test-applications" && strings.HasSuffix(parts[2], ":resolve"))
}
func isMyTestApplicationOperation(operation string) bool {
	return operation == ListMyTestApplicationsGRPCMethod || operation == GetMyTestApplicationGRPCMethod
}
func validMyTestApplicationHTTPInput(r *http.Request) bool {
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
	if decoder.Decode(&root) != nil || decoder.Decode(&struct{}{}) != io.EOF || root == nil {
		return false
	}
	if r.URL.Path == "/v1/test-applications:search" {
		if !onlyKeys(root, "runtime", "pageSize", "page_size", "pageToken", "page_token") {
			return false
		}
		var runtime map[string]json.RawMessage
		if json.Unmarshal(root["runtime"], &runtime) != nil || runtime == nil {
			return false
		}
		return onlyKeys(runtime, "hostRpcApiMajor", "host_rpc_api_major", "hostCapabilities", "host_capabilities")
	}
	return onlyKeys(root, "hostRpcApiMajor", "host_rpc_api_major", "hostCapabilities", "host_capabilities")
}
func myTestApplicationSafeErrorEncoder(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "private, no-store")
	converted := kerrors.FromError(err)
	if _, ok := v1.ErrorReason_value[converted.Reason]; !ok {
		if converted.Code == http.StatusBadRequest {
			err = toTransportError(cd.ErrInvalidMyTestApplicationQuery)
		} else {
			err = toTransportError(cd.ErrMyTestApplicationUnavailable)
		}
	}
	khttp.DefaultErrorEncoder(w, r, err)
}
