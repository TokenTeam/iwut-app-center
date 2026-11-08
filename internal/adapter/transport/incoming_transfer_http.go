package transport

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"unicode/utf8"

	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_admin_transfer"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/protobuf/encoding/protojson"

	"iwut-app-center/internal/application/domain"
)

func incomingTransferHTTPFilter(verifier *IdentityVerifier) khttp.FilterFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != incomingTransferPath {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Cache-Control", "private, no-store")
			if r.Method != http.MethodPost {
				next.ServeHTTP(w, r)
				return
			}
			if _, err := verifyScriptHTTPIdentity(r, verifier); err != nil {
				incomingTransferHTTPError(w, r, toIdentityTransportError(err, incomingTransferOperation))
				return
			}
			if !validIncomingTransferHTTPBody(w, r) {
				incomingTransferHTTPError(w, r, incomingTransferError(domain.ErrInvalidIncomingTransferQuery))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
func validIncomingTransferHTTPBody(w http.ResponseWriter, r *http.Request) bool {
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
	var query pb.IncomingApplicationAdminTransferQuery
	if protojson.Unmarshal(raw, &query) != nil {
		return false
	}
	// Proto JSON null is not the declared object request.
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return true
}
func incomingTransferHTTPError(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "private, no-store")
	converted := kerrors.FromError(err)
	if _, ok := pb.ErrorReason_value[converted.Reason]; !ok && converted.Code == http.StatusBadRequest {
		err = incomingTransferError(domain.ErrInvalidIncomingTransferQuery)
	}
	khttp.DefaultErrorEncoder(w, r, err)
}
