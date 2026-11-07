package mongo

import (
	"context"
	"errors"

	dm "go.mongodb.org/mongo-driver/v2/mongo"
	cd "iwut-app-center/internal/catalog/domain"
	cp "iwut-app-center/internal/catalog/port"
	reviewdomain "iwut-app-center/internal/review/domain"
	script "iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/shared"
)

func mapRuntimeScriptError(err, inconsistent, unavailable error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, script.ErrDistributionStateInconsistent), errors.Is(err, cd.ErrMyTestApplicationStateInconsistent):
		return inconsistent
	case errors.Is(err, script.ErrScriptContentUnavailable):
		return unavailable
	default:
		return err
	}
}

func preparedApplicationScriptGate(review *reviewdomain.ApplicationReview, results map[string]error) error {
	if review == nil {
		return script.ErrDistributionStateInconsistent
	}
	if len(review.ScriptDependencies()) == 0 {
		return nil
	}
	err, checked := results[review.VersionID().String()]
	if !checked {
		return script.ErrDistributionStateInconsistent
	}
	return err
}

func resolvePureTest(app shared.ApplicationID, major int32, host []cd.CapabilityName, f *pureTestFacts) (*cd.TestLaunchDescriptor, []cd.CapabilityName, error) {
	target, missing, err := resolvePureTestCandidate(app, major, host, f)
	if err != nil || target == nil {
		return target, missing, err
	}
	review, err := applicationReviewFromDocument(f.reviews[target.VersionID()])
	if err != nil {
		return nil, nil, cd.ErrMyTestApplicationStateInconsistent
	}
	if err := preparedApplicationScriptGate(review, f.scriptResults); err != nil {
		return nil, nil, mapRuntimeScriptError(err, cd.ErrMyTestApplicationStateInconsistent, script.ErrScriptContentUnavailable)
	}
	return target, missing, nil
}

func preparePureTestScriptGates(ctx context.Context, db *dm.Database, apps []string, major int32, host []cd.CapabilityName, facts *pureTestFacts) error {
	reviews := map[string]*reviewdomain.ApplicationReview{}
	for _, app := range apps {
		target, _, err := resolvePureTestCandidate(shared.ApplicationID(app), major, host, facts)
		if err != nil {
			return err
		}
		if target == nil {
			continue
		}
		review, err := applicationReviewFromDocument(facts.reviews[target.VersionID()])
		if err != nil {
			return cd.ErrMyTestApplicationStateInconsistent
		}
		reviews[target.VersionID()] = review
	}
	results, err := prepareApplicationScriptGates(ctx, db, reviews)
	if err != nil {
		return mapRuntimeScriptError(err, cd.ErrMyTestApplicationStateInconsistent, script.ErrScriptContentUnavailable)
	}
	facts.scriptResults = results
	return nil
}

func (r *PublicCatalogRepository) prepareScriptGates(ctx context.Context, publications []applicationPublicationDocument, caller shared.AuthID, major int32, host []cd.CapabilityName, facts *catalogFacts) error {
	planning := *facts
	planning.planScripts = true
	reviews := map[string]*reviewdomain.ApplicationReview{}
	for _, publication := range publications {
		selected, eligible, err := r.selectCatalogTarget(publication, caller, major, host, &planning)
		if err != nil {
			return err
		}
		if !eligible {
			continue
		}
		for _, id := range []string{*publication.StableVersionID, selected.target.VersionID()} {
			review, err := applicationReviewFromDocument(facts.reviews[id])
			if err != nil {
				return cp.ErrApplicationCatalogStateInconsistent
			}
			reviews[id] = review
		}
	}
	results, err := prepareApplicationScriptGates(ctx, r.database, reviews)
	if err != nil {
		return mapRuntimeScriptError(err, cp.ErrApplicationCatalogStateInconsistent, script.ErrScriptContentUnavailable)
	}
	facts.scriptResults = results
	return nil
}
