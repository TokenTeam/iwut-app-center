package transport

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	catalogv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/catalog"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/grpc/codes"
)

func isTestLaunchResolutionRequest(r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	return r.Method == http.MethodPost && len(parts) == 4 && parts[0] == "v1" && parts[1] == "applications" && parts[3] == "test-launch:resolve"
}

// Generated Kratos binders ignore unknown JSON keys and merge query values.
// Validate the body-only host context first so target/identity fields cannot be
// supplied, including duplicate JSON/proto aliases. Do not echo parser input.
func validTestLaunchHTTPInput(r *http.Request) bool {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.Body == nil {
		return false
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
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

func catalogSafeErrorEncoder(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "private, no-store")
	converted := kerrors.FromError(err)
	if _, ok := catalogv1.ErrorReason_value[converted.Reason]; !ok {
		if converted.Code == http.StatusBadRequest {
			err = invalidResolveTestLaunchRequest()
		} else {
			err = transportStatus(codes.Internal, ReasonInternal, "internal failure")
		}
		converted = kerrors.FromError(err)
	}
	// gRPC FAILED_PRECONDITION is the closest canonical counterpart to the
	// query's HTTP422 contract; Kratos normally maps it to HTTP400.
	if converted.Reason == ReasonHostCapabilitiesInsufficient {
		converted.Code = http.StatusUnprocessableEntity
	}
	khttp.DefaultErrorEncoder(w, r, converted)
}
