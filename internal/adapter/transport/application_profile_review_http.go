package transport

import (
	"bytes"
	"encoding/json"
	reviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_review"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/grpc/codes"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

func isSubmitApplicationProfileReviewRequest(r *http.Request) bool {
	p := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	return r.Method == http.MethodPost && len(p) == 6 && p[0] == "v1" && p[1] == "applications" && p[3] == "profile-revisions" && p[5] == "reviews"
}
func validProfileSubmissionHTTPInput(r *http.Request) bool {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.Body == nil {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1025))
	if err != nil || len(body) > 1024 || !utf8.Valid(body) {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	d := json.NewDecoder(bytes.NewReader(body))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') || !d.More() {
		return false
	}
	key, err := d.Token()
	if err != nil || key != "expectedRevision" && key != "expected_revision" {
		return false
	}
	var value json.RawMessage
	if d.Decode(&value) != nil || bytes.Equal(value, []byte("null")) || d.More() {
		return false
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return false
	}
	_, err = d.Token()
	return err == io.EOF
}
func profileSubmissionErrorEncoder(w http.ResponseWriter, r *http.Request, err error) {
	e := kerrors.FromError(err)
	if _, ok := reviewv1.ErrorReason_value[e.Reason]; !ok {
		if e.Code == 400 {
			err = invalidProfileSubmission()
		} else {
			err = transportStatus(codes.Internal, ReasonInternal, "internal failure")
		}
	}
	khttp.DefaultErrorEncoder(w, r, err)
}

func isDecideApplicationProfileReviewRequest(r *http.Request) bool {
	p := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	return r.Method == http.MethodPost && len(p) == 8 && p[0] == "v1" && p[1] == "applications" && p[3] == "profile-revisions" && p[5] == "reviews" && p[7] == "decision"
}
func validProfileDecisionHTTPInput(r *http.Request) bool {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.Body == nil {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32769))
	if err != nil || len(body) > 32768 || !utf8.Valid(body) {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	aliases := map[string]string{
		"expectedProfileRevisionRevision": "revision", "expected_profile_revision_revision": "revision",
		"expectedCurrentPublishedProfileRevisionId": "published", "expected_current_published_profile_revision_id": "published",
		"expectedPolicyVersion": "policy", "expected_policy_version": "policy",
		"confirmedCheckIds": "checks", "confirmed_check_ids": "checks", "outcome": "outcome", "reason": "reason",
	}
	d := json.NewDecoder(bytes.NewReader(body))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return false
		}
		name, ok := key.(string)
		if !ok {
			return false
		}
		canonical, ok := aliases[name]
		if !ok || seen[canonical] {
			return false
		}
		seen[canonical] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || canonical != "published" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return false
	}
	_, err = d.Token()
	return err == io.EOF && seen["revision"] && seen["policy"] && seen["outcome"]
}
func profileDecisionErrorEncoder(w http.ResponseWriter, r *http.Request, err error) {
	e := kerrors.FromError(err)
	if _, ok := reviewv1.ErrorReason_value[e.Reason]; !ok {
		if e.Code == 400 {
			err = invalidProfileDecision()
		} else {
			err = transportStatus(codes.Internal, ReasonInternal, "internal failure")
		}
	}
	khttp.DefaultErrorEncoder(w, r, err)
}
