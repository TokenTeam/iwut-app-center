package transport

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	runtimev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/runtime_resolution"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/grpc/codes"
)

func isRuntimeResolutionRequest(request *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/"), "/")
	return request.Method == http.MethodPost && len(parts) == 4 && parts[0] == "v1" && parts[1] == "applications" && parts[3] == "launch-target:resolve"
}

func validRuntimeResolutionHTTPInput(request *http.Request) bool {
	if request.URL.RawQuery != "" || request.URL.ForceQuery || request.Body == nil {
		return false
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return false
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	decoder := json.NewDecoder(bytes.NewReader(body))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return false
		}
		var canonical string
		switch key {
		case "hostRpcApiMajor", "host_rpc_api_major":
			canonical = "major"
		case "hostCapabilities", "host_capabilities":
			canonical = "capabilities"
		default:
			return false
		}
		if seen[canonical] {
			return false
		}
		seen[canonical] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return false
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return false
	}
	_, err = decoder.Token()
	return err == io.EOF
}

func invalidResolveLaunchTargetRequest() error {
	return transportStatus(codes.InvalidArgument, ReasonInvalidResolveLaunchTargetRequest, "launch target resolution accepts only application path ID and host context body")
}

func runtimeResolutionSafeErrorEncoder(writer http.ResponseWriter, request *http.Request, err error) {
	writer.Header().Set("Cache-Control", "private, no-store")
	converted := kerrors.FromError(err)
	if _, ok := runtimev1.ErrorReason_value[converted.Reason]; !ok {
		if converted.Code == http.StatusBadRequest {
			err = invalidResolveLaunchTargetRequest()
		} else {
			err = transportStatus(codes.Internal, ReasonInternal, "internal failure")
		}
		converted = kerrors.FromError(err)
	}
	khttp.DefaultErrorEncoder(writer, request, converted)
}
