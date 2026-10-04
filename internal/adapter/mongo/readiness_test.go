package mongo

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestSupportsTransactions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		observation topologyObservation
		want        bool
	}{
		{name: "replica set", observation: topologyObservation{SetName: "rs0"}, want: true},
		{name: "mongos", observation: topologyObservation{Msg: "isdbgrid"}, want: true},
		{name: "standalone", observation: topologyObservation{}, want: false},
		{name: "unknown message", observation: topologyObservation{Msg: "other"}, want: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := supportsTransactions(testCase.observation); got != testCase.want {
				t.Fatalf("supportsTransactions(%#v) = %t, want %t", testCase.observation, got, testCase.want)
			}
		})
	}
}

func TestRequiredMigrationRecorded(t *testing.T) {
	t.Parallel()

	if RequiredMigrationID != "0018_application_filter" {
		t.Fatalf("RequiredMigrationID = %q, want 0018_application_filter", RequiredMigrationID)
	}
	if requiredMigrationRecorded(nil) {
		t.Fatal("empty ledger must not satisfy the required migration")
	}
	if requiredMigrationRecorded([]string{"0001_application_creation", "0003_application_version_draft_update"}) {
		t.Fatal("older ledger must not satisfy the required migration")
	}
	if !requiredMigrationRecorded([]string{"0001_application_creation", RequiredMigrationID}) {
		t.Fatal("ledger containing the required migration must pass")
	}
}

func TestVerifyDeploymentReadiness_RejectsNilDatabase(t *testing.T) {
	t.Parallel()

	if err := VerifyDeploymentReadiness(t.Context(), nil); err == nil {
		t.Fatal("VerifyDeploymentReadiness(nil) error = nil, want error")
	}
}

func TestVerifyTransactionTopology_RejectsNilClient(t *testing.T) {
	t.Parallel()

	if err := VerifyTransactionTopology(t.Context(), nil); err == nil {
		t.Fatal("VerifyTransactionTopology(nil) error = nil, want error")
	}
}

func TestVerifyDeploymentReadinessIntegration(t *testing.T) {
	client := integrationClient(t)

	t.Run("migrated database is ready", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		if err := VerifyDeploymentReadiness(t.Context(), database); err != nil {
			t.Fatalf("VerifyDeploymentReadiness() error = %v", err)
		}
	})

	t.Run("unmigrated database fails without creating schema", func(t *testing.T) {
		database := integrationDatabase(t, client)
		if err := VerifyDeploymentReadiness(t.Context(), database); err == nil {
			t.Fatal("VerifyDeploymentReadiness() error = nil, want missing-migration error")
		}
		names, err := database.ListCollectionNames(t.Context(), bson.D{})
		if err != nil {
			t.Fatalf("list collections: %v", err)
		}
		if len(names) != 0 {
			t.Fatalf("readiness check created collections: %v", names)
		}
	})
}
