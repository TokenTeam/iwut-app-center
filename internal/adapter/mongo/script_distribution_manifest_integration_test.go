package mongo

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	pd "iwut-app-center/internal/publication/domain"
	sd "iwut-app-center/internal/script/domain"
	sp "iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

func TestScriptDistributionManifestIntegration_UCAPP047_BRSCR019021(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	base, app, script, at := publishedAppScriptFixture(t, db, "manifest-script", true)
	publications := NewApplicationPublicationRepository(db)
	stable, err := publications.LoadStablePlacementCandidate(t.Context(), base.applicationID, 1, pd.ApplicationVersionID(base.versionID), base.adminID, nil)
	if err != nil {
		t.Fatal(err)
	}
	publicationID := pd.ApplicationPublicationID(nextIntegrationApplicationReviewID(t))
	if _, err := publications.SetStable(t.Context(), stable, &publicationID, pd.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), base.adminID, publicationTestValidation(), time.Now()); err != nil {
		t.Fatal(err)
	}
	revision := int64(1)
	placePublication(t, publications, loadPublicationCandidate(t, publications, app, 1, &revision), app)
	link := createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(db), app.applicationID, app.adminID, nil, newIntegrationTesterJoinLink(t, app.applicationID, app.adminID)).JoinLink()
	member := joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(db), link, newIntegrationTesterMembership(t, link, "manifest-tester")).Membership()
	runtime, _ := sd.NewScriptRuntimeQuery(sd.ScriptRuntimePureTest, 1, []string{"camera.read.v1"})
	request := sp.ApplicationScriptRequest{Caller: member.TesterAuthID(), ApplicationID: app.applicationID, ApplicationVersionID: app.versionID.String(), Runtime: runtime, ObservedAt: time.Now()}
	reader := NewScriptDistributionRepository(db, ScriptQueryCursorKey{1})
	manifest, err := reader.ResolveApplicationScriptManifest(t.Context(), request)
	if err != nil || len(manifest.Scripts) != 1 || manifest.Scripts[0].VersionID != script.ID || manifest.Target.Channel != "TEST" {
		t.Fatal("exact manifest", err)
	}
	body, err := reader.GetApplicationScriptContent(t.Context(), request, script.ID, script.Content.Digest)
	if err != nil || body.Content.Text != script.Content.Text {
		t.Fatal("exact application content", err)
	}
	other := request
	other.Caller = "not-a-tester"
	if _, err := reader.ResolveApplicationScriptManifest(t.Context(), other); !errors.Is(err, sd.ErrScriptContentUnavailable) {
		t.Fatal("non-tester manifest", err)
	}
	if _, err := reader.GetApplicationScriptContent(t.Context(), other, script.ID, script.Content.Digest); !errors.Is(err, sd.ErrScriptContentUnavailable) {
		t.Fatal("non-tester content", err)
	}
	if _, err := reader.GetApplicationScriptContent(t.Context(), request, script.ID, strings.Repeat("0", 64)); !errors.Is(err, sd.ErrScriptContentUnavailable) {
		t.Fatal("application digest mismatch", err)
	}
	other = request
	other.ApplicationVersionID = base.versionID.String()
	if _, err := reader.ResolveApplicationScriptManifest(t.Context(), other); !errors.Is(err, sd.ErrScriptContentUnavailable) {
		t.Fatal("unselected version manifest", err)
	}

	// A different published head has different bytes. The application remains
	// pinned to its approved dependency and cannot request the other version.
	content, _ := sd.NewContent(sd.JSONFormat, "different opaque head")
	alternative, err := sd.NewDraft(sd.VersionID(nextIntegrationApplicationID(t)), script.ResourceID, "other-head", content, "other-author", at)
	if err != nil {
		t.Fatal(err)
	}
	writer := NewScriptVersionRepository(db)
	if _, err := writer.CreateDraft(t.Context(), alternative); err != nil {
		t.Fatal(err)
	}
	reviewID := sd.ReviewID(nextIntegrationApplicationID(t))
	if _, err := writer.SubmitDraft(t.Context(), sp.SubmitDraft{ResourceID: script.ResourceID, VersionID: alternative.ID, ReviewID: reviewID, ExpectedRevision: 1, Actor: "other-submitter", At: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	seedTerminalScriptDependency(t, db, string(alternative.ID), string(reviewID), "APPROVED", at.Add(2*time.Second))
	scriptPublications := NewScriptPublicationRepository(db)
	if _, err := scriptPublications.ApplyPublication(t.Context(), scriptPublicationCommand(t, script.ResourceID, alternative.ID, sd.PublishScript, 2, at.Add(3*time.Second))); err != nil {
		t.Fatal(err)
	}
	body, err = reader.GetApplicationScriptContent(t.Context(), request, script.ID, script.Content.Digest)
	if err != nil || body.Content.Text != script.Content.Text || body.Content.Descriptor.VersionID != script.ID {
		t.Fatal("head replaced reviewed bytes", err)
	}
	if _, err := reader.GetApplicationScriptContent(t.Context(), request, alternative.ID, alternative.Content.Digest); !errors.Is(err, sd.ErrScriptContentUnavailable) {
		t.Fatal("unreferenced published version in app context", err)
	}
	if got, err := reader.GetPublishedScriptContent(t.Context(), alternative.ResourceID, alternative.ID, alternative.Content.Digest, time.Now()); err != nil || got.Text != alternative.Content.Text {
		t.Fatal("independent publication incorrectly required app association", err)
	}
	if _, err := scriptPublications.ApplyPublication(t.Context(), scriptPublicationCommand(t, script.ResourceID, "", sd.ClearScriptHead, 3, at.Add(4*time.Second))); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ResolveApplicationScriptManifest(t.Context(), request); err != nil {
		t.Fatal("clear invalidated manifest", err)
	}

	exposure, _ := pd.NewExposureBasisPoints(5000)
	grey, err := publications.LoadGreyPlacementCandidate(t.Context(), app.applicationID, 1, pd.ApplicationVersionID(app.versionID), exposure, app.adminID, 2)
	if err != nil {
		t.Fatal(err)
	}
	rollout := pd.GreyRolloutID(nextIntegrationApplicationReviewID(t))
	seed, _ := pd.NewCohortSeed(make([]byte, 32))
	validation := publicationTestValidation()
	greyResult, err := publications.SetGrey(t.Context(), grey, &rollout, &seed, pd.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), app.adminID, &validation, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var matched, missed shared.AuthID
	for i := 0; i < 1000 && (matched == "" || missed == ""); i++ {
		id := shared.AuthID(fmt.Sprintf("manifest-grey-%d", i))
		if greyResult.Publication().GreyRollout().Matches(id) {
			matched = id
		} else {
			missed = id
		}
	}
	if matched == "" || missed == "" {
		t.Fatal("no controlled cohort fixtures")
	}
	other = request
	other.Caller = matched
	other.Runtime.Mode = sd.ScriptRuntimeUnified
	if m, err := reader.ResolveApplicationScriptManifest(t.Context(), other); err != nil || m.Target.Channel != "GREY" {
		t.Fatal("matched cohort manifest", err)
	}
	other.Caller = missed
	if _, err := reader.GetApplicationScriptContent(t.Context(), other, script.ID, script.Content.Digest); !errors.Is(err, sd.ErrScriptContentUnavailable) {
		t.Fatal("missed cohort acquired app content", err)
	}
	other.ApplicationVersionID = base.versionID.String()
	if m, err := reader.ResolveApplicationScriptManifest(t.Context(), other); err != nil || m.Scripts == nil || len(m.Scripts) != 0 || m.Target.Channel != "STABLE" {
		t.Fatal("explicit empty Stable manifest", err)
	}
	// The requested version remains selected for the tester after an unrelated
	// Publication revision change; a previous revision is not a content lease.
	if got, err := reader.GetApplicationScriptContent(t.Context(), request, script.ID, script.Content.Digest); err != nil || got.Target.PublicationRevision != 3 {
		t.Fatal("same exact target rejected shared revision change", err)
	}
	if _, err := scriptPublications.ApplyPublication(t.Context(), scriptPublicationCommand(t, script.ResourceID, "", sd.SuspendScript, 1, at.Add(5*time.Second))); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.GetApplicationScriptContent(t.Context(), request, script.ID, script.Content.Digest); !errors.Is(err, sd.ErrScriptContentUnavailable) {
		t.Fatal("old manifest bypassed suspension", err)
	}
	if _, err := scriptPublications.ApplyPublication(t.Context(), scriptPublicationCommand(t, script.ResourceID, "", sd.RestoreScript, 2, at.Add(6*time.Second))); err != nil {
		t.Fatal(err)
	}
	if _, err := publications.ClearTest(t.Context(), app.applicationID, 1, app.adminID, 3, func() (pd.ApplicationPublicationHistoryID, time.Time, error) {
		return pd.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), time.Now(), nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.GetApplicationScriptContent(t.Context(), request, script.ID, script.Content.Digest); !errors.Is(err, sd.ErrScriptContentUnavailable) {
		t.Fatal("old pure-Test target survived slot clear", err)
	}
	if _, err := reader.GetPublishedScriptContent(t.Context(), script.ResourceID, script.ID, script.Content.Digest, time.Now()); err != nil {
		t.Fatal("app slot changed independent qualification", err)
	}
}
