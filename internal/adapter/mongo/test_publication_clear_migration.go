package mongo

import "context"

const testPublicationClearMigrationID = "0029_test_publication_clear"

func (m *Migrator) applyTestPublicationClearMigration(ctx context.Context) error {
	return m.ensureValidatedCollection(ctx, applicationPublicationHistoryCollectionName, applicationPublicationHistoryValidatorWithTestClear(true))
}
