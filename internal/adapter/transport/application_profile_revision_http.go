package transport

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	profilev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_revision"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/grpc/codes"
)

func isCreateApplicationProfileRevisionRequest(r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	return r.Method == http.MethodPost && len(parts) == 4 && parts[0] == "v1" && parts[1] == "applications" && parts[3] == "profile-revisions"
}

// Validate before generated Proto binding: it otherwise ignores unknown fields,
// loses missing/null distinctions and merges URL query parameters into commands.
func validCreateApplicationProfileRevisionHTTPInput(r *http.Request) bool {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.Body == nil {
		return false
	}
	body, err := io.ReadAll(r.Body)
	if err != nil || !utf8.Valid(body) {
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
		canonical := ""
		switch key {
		case "displayName", "display_name":
			canonical = "displayName"
		case "description":
			canonical = "description"
		case "icon":
			canonical = "icon"
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
		if bytes.Equal(value, []byte("null")) {
			if canonical == "displayName" {
				return false
			}
			continue
		}
		var text string
		if json.Unmarshal(value, &text) != nil {
			return false
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return false
	}
	_, err = decoder.Token()
	return err == io.EOF && len(seen) == 3
}

func profileSafeErrorEncoder(w http.ResponseWriter, r *http.Request, err error) {
	converted := kerrors.FromError(err)
	if _, ok := profilev1.ErrorReason_value[converted.Reason]; !ok {
		if converted.Code == http.StatusBadRequest {
			err = invalidCreateApplicationProfileRevisionRequest()
		} else {
			err = transportStatus(codes.Internal, ReasonInternal, "internal failure")
		}
	}
	khttp.DefaultErrorEncoder(w, r, err)
}
