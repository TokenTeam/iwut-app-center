package transport

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	filterv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_filter"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/protobuf/encoding/protojson"
)

const maxApplicationFilterBodyBytes = 1 << 20

func isApplicationFilterRequest(r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	return len(parts) == 4 && parts[0] == "v1" && parts[1] == "applications" && parts[3] == "filter" && (r.Method == http.MethodGet || r.Method == http.MethodPut || r.Method == http.MethodDelete)
}

func validApplicationFilterHTTPInput(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet:
		return (r.URL.RawQuery == "" && !r.URL.ForceQuery) && emptyHTTPBody(r)
	case http.MethodDelete:
		query := r.URL.Query()
		return len(query) == 1 && len(query["expected_revision"]) == 1 && emptyHTTPBody(r)
	case http.MethodPut:
		if r.URL.RawQuery != "" || r.URL.ForceQuery || r.Body == nil {
			return false
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxApplicationFilterBodyBytes+1))
		if err != nil || len(body) > maxApplicationFilterBodyBytes || !utf8.Valid(body) {
			return false
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if !hasExactFilterCommandFields(body) {
			return false
		}
		var command filterv1.SetApplicationFilterCommand
		return protojson.UnmarshalOptions{DiscardUnknown: false}.Unmarshal(body, &command) == nil && command.GetRule() != nil
	default:
		return false
	}
}

func emptyHTTPBody(r *http.Request) bool {
	if r.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1025))
	return err == nil && len(body) <= 1024 && strings.TrimSpace(string(body)) == ""
}

func hasExactFilterCommandFields(body []byte) bool {
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
		case "expectedRevision", "expected_revision":
			canonical = "expectedRevision"
		case "rule":
			canonical = "rule"
		default:
			return false
		}
		if seen[canonical] {
			return false
		}
		seen[canonical] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || !seen["expectedRevision"] || !seen["rule"] {
		return false
	}
	_, err = decoder.Token()
	return err == io.EOF
}

func applicationFilterSafeErrorEncoder(w http.ResponseWriter, r *http.Request, err error) {
	converted := kerrors.FromError(err)
	if _, ok := filterv1.ErrorReason_value[converted.Reason]; !ok {
		err = toTransportError(filterInvalidRequestError(converted.Code))
	}
	khttp.DefaultErrorEncoder(w, r, err)
}

func filterInvalidRequestError(code int32) error {
	if code == int32(http.StatusBadRequest) {
		return filterInvalidApplicationFilter()
	}
	return filterInternalError()
}
