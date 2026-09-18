package domain

import (
	"errors"
	"testing"
)

func TestDeveloperApplicationQuota_BR_APP_005_InitialLimitAndCapacity(t *testing.T) {
	t.Parallel()

	if InitialDeveloperApplicationQuotaLimit != 10 {
		t.Fatalf("InitialDeveloperApplicationQuotaLimit = %d, want 10", InitialDeveloperApplicationQuotaLimit)
	}

	adminID := AuthID("admin-1")
	quota, err := NewDeveloperApplicationQuota(adminID, InitialDeveloperApplicationQuotaLimit, 9)
	if err != nil {
		t.Fatalf("NewDeveloperApplicationQuota() error = %v", err)
	}
	if !quota.HasCapacity() {
		t.Fatal("quota with 9 of 10 used should have capacity")
	}

	consumed, err := quota.Consume()
	if err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if consumed.UsedCount() != 10 || consumed.HasCapacity() {
		t.Fatalf("consumed quota = %d/%d, want full 10/10", consumed.UsedCount(), consumed.Limit())
	}
	if quota.UsedCount() != 9 {
		t.Fatalf("original quota was mutated to %d", quota.UsedCount())
	}

	if _, err := consumed.Consume(); !errors.Is(err, ErrApplicationQuotaExceeded) {
		t.Fatalf("Consume() error = %v, want ApplicationQuotaExceeded", err)
	}
}

func TestDeveloperApplicationQuota_BR_APP_005_RejectsInvalidState(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		adminID   AuthID
		limit     int32
		usedCount int32
	}{
		{name: "missing admin", limit: 10},
		{name: "negative limit", adminID: "admin-1", limit: -1},
		{name: "negative usage", adminID: "admin-1", limit: 10, usedCount: -1},
		{name: "usage above limit", adminID: "admin-1", limit: 10, usedCount: 11},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewDeveloperApplicationQuota(testCase.adminID, testCase.limit, testCase.usedCount)
			if !errors.Is(err, ErrInternal) {
				t.Fatalf("NewDeveloperApplicationQuota() error = %v, want internal error", err)
			}
		})
	}
}
