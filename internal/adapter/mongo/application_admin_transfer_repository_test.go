package mongo

import (
	"errors"
	"testing"
	"time"

	applicationdomain "iwut-app-center/internal/application/domain"
)

func validTransferDocument() applicationAdminTransferDocument {
	requested := time.Date(2026, time.October, 1, 2, 3, 4, 0, time.UTC)
	return applicationAdminTransferDocument{
		TransferID:              "01890f47-0000-7000-8000-000000000101",
		ApplicationID:           "01890f47-0000-7000-8000-000000000102",
		FromAdminID:             "auth-source",
		ToAdminID:               "auth-target",
		SourceOwnershipRevision: 1,
		Status:                  "PENDING",
		RequestedAt:             requested,
		ExpiresAt:               requested.Add(applicationdomain.ApplicationAdminTransferLifetime),
	}
}

func TestTransferFromDocument_BR_APP_013_019_StrictTerminalAudit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*applicationAdminTransferDocument)
		want   applicationdomain.ApplicationAdminTransferResolutionCause
	}{
		{
			name: "explicit cancellation preserves cause",
			mutate: func(document *applicationAdminTransferDocument) {
				resolvedAt, resolvedBy, cause := document.RequestedAt.Add(time.Hour), document.FromAdminID, "EXPLICIT"
				document.Status, document.ResolvedAt, document.ResolvedBy, document.ResolutionCause = "CANCELLED", &resolvedAt, &resolvedBy, &cause
			},
			want: applicationdomain.ApplicationAdminTransferResolutionExplicit,
		},
		{
			name: "unknown cancellation cause is corruption",
			mutate: func(document *applicationAdminTransferDocument) {
				resolvedAt, resolvedBy, cause := document.RequestedAt.Add(time.Hour), document.FromAdminID, "PRIVATE_UNKNOWN"
				document.Status, document.ResolvedAt, document.ResolvedBy, document.ResolutionCause = "CANCELLED", &resolvedAt, &resolvedBy, &cause
			},
		},
		{
			name: "rejection cannot carry cancellation cause",
			mutate: func(document *applicationAdminTransferDocument) {
				resolvedAt, resolvedBy, cause := document.RequestedAt.Add(time.Hour), document.ToAdminID, "EXPLICIT"
				document.Status, document.ResolvedAt, document.ResolvedBy, document.ResolutionCause = "REJECTED", &resolvedAt, &resolvedBy, &cause
			},
		},
		{
			name: "accepted actor must be target",
			mutate: func(document *applicationAdminTransferDocument) {
				resolvedAt, resolvedBy, handling := document.RequestedAt.Add(time.Hour), document.FromAdminID, "KEEP"
				document.Status, document.ResolvedAt, document.ResolvedBy, document.ConfidentialCredentialHandling = "ACCEPTED", &resolvedAt, &resolvedBy, &handling
			},
		},
		{
			name: "fixed lifetime is required",
			mutate: func(document *applicationAdminTransferDocument) {
				document.ExpiresAt = document.ExpiresAt.Add(time.Second)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document := validTransferDocument()
			test.mutate(&document)
			transfer, err := transferFromDocument(document)
			if test.want != "" {
				if err != nil || transfer.ResolutionCause == nil || *transfer.ResolutionCause != test.want {
					t.Fatalf("transferFromDocument() = (%#v, %v), want cause %q", transfer, err, test.want)
				}
				return
			}
			if !errors.Is(err, applicationdomain.ErrApplicationAdminTransferStateInconsistent) {
				t.Fatalf("transferFromDocument() error = %v, want state inconsistent", err)
			}
		})
	}
}
