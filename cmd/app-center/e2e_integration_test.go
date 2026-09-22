package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	applicationv1 "iwut-app-center/api/gen/go/app_center/v1/application"
	applicationreviewv1 "iwut-app-center/api/gen/go/app_center/v1/application_review"
	applicationversionv1 "iwut-app-center/api/gen/go/app_center/v1/application_version"
	developerstatusv1 "iwut-app-center/api/gen/go/auth_center/v1/developer_status"
	scopecatalogv1 "iwut-app-center/api/gen/go/auth_center/v1/scope_catalog"
	systemprincipalv1 "iwut-app-center/api/gen/go/auth_center/v1/system_principal"
	mongoadapter "iwut-app-center/internal/adapter/mongo"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
)

// mongoIntegrationURIEnv is the same switch the MongoDB adapter integration
// tests use. Without it these end-to-end tests skip so plain `go test ./...`
// never needs Docker.
const mongoIntegrationURIEnv = "MONGODB_INTEGRATION_URI"

const (
	authCenterIntegrationTargetEnv   = "AUTH_CENTER_INTEGRATION_TARGET"
	authCenterIntegrationDatabaseEnv = "AUTH_CENTER_INTEGRATION_DATABASE"
)

const (
	e2eIssuer           = "https://auth.e2e.test"
	e2eAudience         = "iwut-app-center"
	e2eKeyID            = "e2e-primary"
	e2eHTTPName         = "Course_Table_E2E"
	e2eGRPCName         = "Course_Table_GRPC"
	e2eHTTPAdminID      = "auth-e2e-http"
	e2eGRPCAdminID      = "auth-e2e-grpc"
	e2eUnusedAuthTarget = "127.0.0.1:1"
)

var (
	e2eServiceKeyOnce sync.Once
	e2eServiceKeyB64  string
	e2eServiceKeyErr  error
)

func e2eServicePrivateKeyB64() string {
	e2eServiceKeyOnce.Do(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			e2eServiceKeyErr = err
			return
		}
		encoded := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
		e2eServiceKeyB64 = base64.StdEncoding.EncodeToString(encoded)
	})
	if e2eServiceKeyErr != nil {
		panic(e2eServiceKeyErr)
	}
	return e2eServiceKeyB64
}

// e2eApplicationDocument mirrors only the persisted business/ownership fields
// the assertions need. The test reads them directly from the isolated database
// to prove the HTTP and gRPC calls crossed the real transaction-capable
// repository instead of an in-process fake.
type e2eApplicationDocument struct {
	ID        string    `bson:"id"`
	Name      string    `bson:"name"`
	NameKey   string    `bson:"nameKey"`
	AdminID   string    `bson:"adminId"`
	CreatedAt time.Time `bson:"createdAt"`
}

type e2eQuotaDocument struct {
	AdminID   string `bson:"adminId"`
	Limit     int32  `bson:"limit"`
	UsedCount int32  `bson:"usedCount"`
}

type e2eApplicationVersionDocument struct {
	VersionID                 string    `bson:"versionId"`
	ApplicationID             string    `bson:"applicationId"`
	Sequence                  int32     `bson:"sequence"`
	VersionLabel              string    `bson:"versionLabel"`
	LaunchURL                 string    `bson:"launchUrl"`
	RPCApiMinVersion          int32     `bson:"rpcApiMinVersion"`
	RPCApiMaxVersionExclusive int32     `bson:"rpcApiMaxVersionExclusive"`
	RequiredCapabilities      []string  `bson:"requiredCapabilities"`
	RequiredScopes            []string  `bson:"requiredScopes"`
	OptionalScopes            []string  `bson:"optionalScopes"`
	ReviewStatus              string    `bson:"reviewStatus"`
	CreatedBy                 string    `bson:"createdBy"`
	CreatedAt                 time.Time `bson:"createdAt"`
	Revision                  int64     `bson:"revision"`
	UpdatedBy                 string    `bson:"updatedBy"`
	UpdatedAt                 time.Time `bson:"updatedAt"`
}

type e2eApplicationReviewDocument struct {
	ReviewID               string                       `bson:"reviewId"`
	ApplicationID          string                       `bson:"applicationId"`
	VersionID              string                       `bson:"versionId"`
	Attempt                int32                        `bson:"attempt"`
	SourceVersionRevision  int64                        `bson:"sourceVersionRevision"`
	Status                 string                       `bson:"status"`
	ScopeCatalogRevision   int64                        `bson:"scopeCatalogRevision"`
	PreflightPolicyVersion string                       `bson:"preflightPolicyVersion"`
	SubmittedBy            string                       `bson:"submittedBy"`
	DraftRestoration       *e2eDraftRestorationDocument `bson:"draftRestoration"`
	Decision               *e2eReviewDecisionDocument   `bson:"decision"`
}

type e2eReviewDecisionDocument struct {
	Outcome             string                               `bson:"outcome"`
	ReviewPolicyVersion string                               `bson:"reviewPolicyVersion"`
	ConfirmedCheckIDs   []string                             `bson:"confirmedCheckIds"`
	Reason              *string                              `bson:"reason"`
	DecidedBy           string                               `bson:"decidedBy"`
	DecidedAt           time.Time                            `bson:"decidedAt"`
	ApprovalValidation  *e2eReviewApprovalValidationDocument `bson:"approvalValidation"`
}

type e2eReviewApprovalValidationDocument struct {
	ScopeCatalogRevision   int64  `bson:"scopeCatalogRevision"`
	PreflightPolicyVersion string `bson:"preflightPolicyVersion"`
}

type e2eDraftRestorationDocument struct {
	RestoredBy            string    `bson:"restoredBy"`
	RestoredAt            time.Time `bson:"restoredAt"`
	ResultVersionRevision int64     `bson:"resultVersionRevision"`
}

type e2eDNSResolver struct {
	addresses map[string][]netip.Addr
	errors    map[string]error
}

func (resolver *e2eDNSResolver) LookupNetIP(_ context.Context, network, host string) ([]netip.Addr, error) {
	if network != "ip" {
		return nil, fmt.Errorf("unexpected network %q", network)
	}
	if err := resolver.errors[host]; err != nil {
		return nil, err
	}
	return append([]netip.Addr{}, resolver.addresses[host]...), nil
}

type e2eScopeCatalogServer struct {
	scopecatalogv1.UnimplementedScopeCatalogServer
	calls       atomic.Int32
	revision    atomic.Int64
	unavailable atomic.Bool
	denyProfile atomic.Bool
}

type e2eDeveloperStatusServer struct {
	developerstatusv1.UnimplementedDeveloperStatusDirectoryServer
	systemprincipalv1.UnimplementedSystemPrincipalDirectoryServer
	mu           sync.RWMutex
	statuses     map[string]developerstatusv1.DeveloperStatus
	systemAuthID string
	calls        atomic.Int32
	unavailable  atomic.Bool
}

func (server *e2eDeveloperStatusServer) ResolveSystemPrincipal(
	_ context.Context,
	request *systemprincipalv1.ResolveSystemPrincipalRequest,
) (*systemprincipalv1.ResolveSystemPrincipalResponse, error) {
	if request.GetPurpose() != systemprincipalv1.SystemPrincipalPurpose_SYSTEM_PRINCIPAL_PURPOSE_APP_CENTER_REVIEW_AUTO_REJECTION || server.systemAuthID == "" {
		return nil, status.Error(codes.NotFound, "system principal not found")
	}
	return &systemprincipalv1.ResolveSystemPrincipalResponse{AuthId: server.systemAuthID, Purpose: request.GetPurpose()}, nil
}

func (server *e2eDeveloperStatusServer) SetStatus(authID string, current developerstatusv1.DeveloperStatus) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.statuses[authID] = current
}

func (server *e2eDeveloperStatusServer) BatchGetDeveloperStatuses(
	_ context.Context,
	request *developerstatusv1.BatchGetDeveloperStatusesRequest,
) (*developerstatusv1.BatchGetDeveloperStatusesResponse, error) {
	server.calls.Add(1)
	if server.unavailable.Load() {
		return nil, status.Error(codes.Unavailable, "developer status unavailable")
	}
	server.mu.RLock()
	defer server.mu.RUnlock()
	entries := make([]*developerstatusv1.DeveloperStatusEntry, 0, len(request.GetAuthIds()))
	for _, authID := range request.GetAuthIds() {
		current, exists := server.statuses[authID]
		if !exists {
			return nil, status.Error(codes.NotFound, "developer status not found")
		}
		entries = append(entries, &developerstatusv1.DeveloperStatusEntry{AuthId: authID, DeveloperStatus: current})
	}
	return &developerstatusv1.BatchGetDeveloperStatusesResponse{Entries: entries}, nil
}

func (server *e2eScopeCatalogServer) GetScopeCatalogSnapshot(
	context.Context,
	*scopecatalogv1.GetScopeCatalogSnapshotRequest,
) (*scopecatalogv1.GetScopeCatalogSnapshotResponse, error) {
	server.calls.Add(1)
	if server.unavailable.Load() {
		return nil, status.Error(codes.Unavailable, "scope catalog unavailable")
	}
	return &scopecatalogv1.GetScopeCatalogSnapshotResponse{
		Revision:    server.revision.Load(),
		GeneratedAt: timestamppb.New(time.Date(2026, time.September, 21, 0, 0, 0, 0, time.UTC)),
		Scopes: []*scopecatalogv1.ScopeDefinition{
			{Name: "calendar.read", Requestable: true},
			{Name: "legacy.profile", Requestable: false},
			{Name: "profile.basic", Requestable: !server.denyProfile.Load()},
			{Name: "schedule.read", Requestable: true},
		},
	}, nil
}

// TestE2E_UCAPP001_ServeRequiresExplicitMigration proves the composed server
// never migrates on its own: wireApp against a fresh database must fail closed
// and leave the schema untouched.
func TestE2E_UCAPP001_ServeRequiresExplicitMigration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, database := e2eIsolatedDatabase(t, ctx, false)
	_, publicKeyPath := e2eIdentity(t)

	addresses := e2eReserveAddresses(t, 2)
	configuration, err := config.Load(e2eEnvironment(map[string]string{
		config.MongoURIEnv:               os.Getenv(mongoIntegrationURIEnv),
		config.MongoDatabaseEnv:          database.Name(),
		config.HTTPAddrEnv:               addresses[0],
		config.GRPCAddrEnv:               addresses[1],
		config.IdentityIssuerEnv:         e2eIssuer,
		config.IdentityAudienceEnv:       e2eAudience,
		config.IdentityPublicKeysEnv:     e2eKeyID + "=" + publicKeyPath,
		config.AuthScopeCatalogTargetEnv: e2eUnusedAuthTarget,
	}))
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}

	app, cleanup, err := wireApp(configuration)
	if err == nil {
		if cleanup != nil {
			cleanup()
		}
		if app != nil {
			_ = app.Stop()
		}
		t.Fatal("wireApp() error = nil, want startup refusal for an un-migrated database")
	}
	if app != nil {
		t.Fatalf("wireApp() app = %v, want nil on failure", app)
	}
	if !strings.Contains(err.Error(), "mongo deployment not ready") {
		t.Fatalf("wireApp() error = %v, want a read-only readiness refusal", err)
	}

	names, listErr := database.ListCollectionNames(ctx, bson.D{})
	if listErr != nil {
		t.Fatalf("list collections after refused serve: %v", listErr)
	}
	if len(names) != 0 {
		t.Fatalf("refused serve created collections: %v", names)
	}
}

// TestE2E_UCAPP001_CreateApplicationOverRealHTTPAndGRPC is the full UC-APP-001
// path: real RS256 compact JWS, the generated Kratos HTTP and native gRPC
// listeners, the wireApp composition root, the CreateApplication use case and a
// real transaction-capable MongoDB.
func TestE2E_UCAPP001_CreateApplicationOverRealHTTPAndGRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Explicit migration is a deployment step; only then may serve start.
	_, database := e2eIsolatedDatabase(t, ctx, true)

	privateKey, publicKeyPath := e2eIdentity(t)
	httpToken := e2eSignIdentity(t, privateKey, e2eHTTPAdminID, "APPROVED")
	grpcToken := e2eSignIdentity(t, privateKey, e2eGRPCAdminID, "APPROVED")

	addresses := e2eReserveAddresses(t, 2)
	httpAddress, grpcAddress := addresses[0], addresses[1]
	configuration, err := config.Load(e2eEnvironment(map[string]string{
		config.MongoURIEnv:               os.Getenv(mongoIntegrationURIEnv),
		config.MongoDatabaseEnv:          database.Name(),
		config.HTTPAddrEnv:               httpAddress,
		config.GRPCAddrEnv:               grpcAddress,
		config.IdentityIssuerEnv:         e2eIssuer,
		config.IdentityAudienceEnv:       e2eAudience,
		config.IdentityPublicKeysEnv:     e2eKeyID + "=" + publicKeyPath,
		config.AuthScopeCatalogTargetEnv: e2eUnusedAuthTarget,
	}))
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}

	// The real composition root. Nothing below constructs or injects an
	// adapter, repository, verifier or server by hand.
	app, appCleanup, err := wireApp(configuration)
	if err != nil {
		t.Fatalf("wireApp() error = %v", err)
	}
	if appCleanup == nil {
		t.Fatal("wireApp() cleanup = nil, want a Mongo disconnect cleanup")
	}
	runDone := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run()
		close(runDone)
	}()
	t.Cleanup(func() {
		if stopErr := app.Stop(); stopErr != nil {
			t.Errorf("app.Stop() error = %v", stopErr)
		}
		select {
		case <-runDone:
		case <-time.After(15 * time.Second):
			t.Errorf("app.Run() did not stop within 15s")
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Errorf("app.Run() error = %v", runErr)
		}
		appCleanup()
	})

	e2eWaitForHTTPListener(t, ctx, httpAddress, runDone, &runErr)
	connection := e2eDialGRPC(t, ctx, grpcAddress, runDone, &runErr)
	t.Cleanup(func() { _ = connection.Close() })

	// (1) HTTP POST /v1/applications with x-iwut-identity -> 201 + four fields.
	status, httpBody := e2eHTTPCreate(t, httpAddress, httpToken, e2eHTTPName)
	if status != http.StatusCreated {
		t.Fatalf("HTTP status = %d, want 201; body = %s", status, httpBody)
	}
	var httpResponse applicationv1.CreateApplicationResponse
	if err := protojson.Unmarshal(httpBody, &httpResponse); err != nil {
		t.Fatalf("decode HTTP response: %v; body = %s", err, httpBody)
	}
	e2eAssertResponse(t, "HTTP", &httpResponse, e2eHTTPName, e2eHTTPAdminID)

	// (2) Native gRPC CreateApplication with metadata x-iwut-identity.
	client := applicationv1.NewApplicationClient(connection)
	grpcCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, grpcToken))
	grpcResponse, err := client.CreateApplication(grpcCtx, &applicationv1.CreateApplicationRequest{Name: e2eGRPCName})
	if err != nil {
		t.Fatalf("gRPC CreateApplication() error = %v", err)
	}
	e2eAssertResponse(t, "gRPC", grpcResponse, e2eGRPCName, e2eGRPCAdminID)

	if httpResponse.GetId() == grpcResponse.GetId() {
		t.Fatalf("HTTP and gRPC returned the same application id %q", httpResponse.GetId())
	}

	// (3) Both requests are actually persisted under the token's administrator.
	persistedHTTP := e2eFindApplication(t, database, httpResponse.GetId())
	persistedGRPC := e2eFindApplication(t, database, grpcResponse.GetId())
	e2eAssertPersisted(t, persistedHTTP, e2eHTTPName, e2eHTTPAdminID)
	e2eAssertPersisted(t, persistedGRPC, e2eGRPCName, e2eGRPCAdminID)
	e2eAssertCollectionCount(t, database, "applications", 2)
	e2eAssertQuota(t, database, e2eHTTPAdminID, 10, 1)
	e2eAssertQuota(t, database, e2eGRPCAdminID, 10, 1)

	// (5) Nothing in the database carries the JWS, and an invalid identity is
	// rejected without echoing the token or writing a partial result.
	for _, collectionName := range []string{"applications", "application_creation_quotas", "app_center_schema_migrations"} {
		e2eAssertNoSecret(t, database, collectionName, httpToken, grpcToken)
	}
	const invalidToken = "not-a-compact-jws-token"
	invalidStatus, invalidBody := e2eHTTPCreate(t, httpAddress, invalidToken, "Should_Not_Persist")
	if invalidStatus != http.StatusUnauthorized {
		t.Fatalf("invalid identity status = %d, want 401; body = %s", invalidStatus, invalidBody)
	}
	if bytes.Contains(invalidBody, []byte(invalidToken)) {
		t.Fatalf("authentication failure leaked the token: %s", invalidBody)
	}
	ordinaryToken := e2eSignReviewerIdentity(t, privateKey, "auth-e2e-ordinary")
	ordinaryStatus, ordinaryBody := e2eHTTPCreate(t, httpAddress, ordinaryToken, "Ordinary_User_Cannot_Create")
	if ordinaryStatus != http.StatusForbidden || !bytes.Contains(ordinaryBody, []byte(transport.ReasonDeveloperApprovalRequired)) {
		t.Fatalf("ordinary user response = status:%d body:%s, want 403 developer approval required", ordinaryStatus, ordinaryBody)
	}
	e2eAssertCollectionCount(t, database, "applications", 2)
}

// TestE2E_UCAPP002_CreateApplicationVersionOverRealHTTP crosses the complete
// consumer path: generated HTTP route, trusted JWS, Wire composition, generated
// Auth gRPC client, fail-closed cache and the transaction-capable MongoDB
// repository. The test Auth server implements the shared generated interface;
// it is not a second hand-written wire model.
func TestE2E_UCAPP002_CreateApplicationVersionOverRealHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	_, database := e2eIsolatedDatabase(t, ctx, true)
	privateKey, publicKeyPath := e2eIdentity(t)
	const adminID = "auth-e2e-version"
	token := e2eSignIdentity(t, privateKey, adminID, "APPROVED")

	addresses := e2eReserveAddresses(t, 3)
	httpAddress, grpcAddress, authAddress := addresses[0], addresses[1], addresses[2]
	authServer := e2eStartScopeCatalogServer(t, authAddress)
	configuration, err := config.Load(e2eEnvironment(map[string]string{
		config.MongoURIEnv:               os.Getenv(mongoIntegrationURIEnv),
		config.MongoDatabaseEnv:          database.Name(),
		config.HTTPAddrEnv:               httpAddress,
		config.GRPCAddrEnv:               grpcAddress,
		config.IdentityIssuerEnv:         e2eIssuer,
		config.IdentityAudienceEnv:       e2eAudience,
		config.IdentityPublicKeysEnv:     e2eKeyID + "=" + publicKeyPath,
		config.AuthScopeCatalogTargetEnv: authAddress,
		config.ScopeCatalogCacheTTLEnv:   "1ns",
	}))
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}

	app, appCleanup, err := wireApp(configuration)
	if err != nil {
		t.Fatalf("wireApp() error = %v", err)
	}
	runDone := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run()
		close(runDone)
	}()
	t.Cleanup(func() {
		if stopErr := app.Stop(); stopErr != nil {
			t.Errorf("app.Stop() error = %v", stopErr)
		}
		select {
		case <-runDone:
		case <-time.After(15 * time.Second):
			t.Error("app.Run() did not stop within 15s")
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Errorf("app.Run() error = %v", runErr)
		}
		appCleanup()
	})
	e2eWaitForHTTPListener(t, ctx, httpAddress, runDone, &runErr)

	// Create the owning Application through UC-APP-001 rather than seeding a
	// persistence document, so UC-APP-002 begins from a real public predecessor.
	statusCode, applicationBody := e2eHTTPCreate(t, httpAddress, token, "Version_E2E_App")
	if statusCode != http.StatusCreated {
		t.Fatalf("create predecessor application status = %d; body = %s", statusCode, applicationBody)
	}
	var applicationResponse applicationv1.CreateApplicationResponse
	if err := protojson.Unmarshal(applicationBody, &applicationResponse); err != nil {
		t.Fatalf("decode predecessor application: %v", err)
	}

	versionStatus, versionHeader, versionBody := e2eHTTPCreateVersion(
		t,
		httpAddress,
		token,
		applicationResponse.GetId(),
		"v1.0.0",
		[]string{"user.profile.v1", "camera.read.v1"},
		[]string{"schedule.read", "calendar.read"},
		[]string{"profile.basic"},
	)
	if versionStatus != http.StatusCreated || versionHeader.Get("ETag") != `"1"` {
		t.Fatalf("create version response = status:%d ETag:%q body:%s", versionStatus, versionHeader.Get("ETag"), versionBody)
	}
	var versionResponse applicationversionv1.CreateApplicationVersionResponse
	if err := protojson.Unmarshal(versionBody, &versionResponse); err != nil {
		t.Fatalf("decode version response: %v; body = %s", err, versionBody)
	}
	e2eAssertApplicationVersionResponse(t, &versionResponse, applicationResponse.GetId(), adminID)
	persisted := e2eFindApplicationVersion(t, database, versionResponse.GetVersionId())
	e2eAssertPersistedApplicationVersion(t, persisted, &versionResponse)
	e2eAssertCollectionCount(t, database, "application_versions", 1)
	e2eAssertNextVersionSequence(t, database, applicationResponse.GetId(), 2)

	// Unknown/non-requestable Scope fails before MongoDB. With the 1ns test TTL
	// this also proves a fresh generated-interface snapshot can be reloaded.
	invalidStatus, _, invalidBody := e2eHTTPCreateVersion(
		t, httpAddress, token, applicationResponse.GetId(), "v1.0.1", nil, []string{"legacy.profile"}, nil,
	)
	if invalidStatus != http.StatusBadRequest || !bytes.Contains(invalidBody, []byte(transport.ReasonInvalidApplicationScope)) {
		t.Fatalf("invalid Scope response = status:%d body:%s", invalidStatus, invalidBody)
	}
	e2eAssertCollectionCount(t, database, "application_versions", 1)
	e2eAssertNextVersionSequence(t, database, applicationResponse.GetId(), 2)

	// A failed refresh is fail-closed: no stale snapshot is used and neither a
	// version nor the Application sequence allocation is partially committed.
	authServer.unavailable.Store(true)
	unavailableStatus, _, unavailableBody := e2eHTTPCreateVersion(
		t, httpAddress, token, applicationResponse.GetId(), "v1.0.2", nil, []string{"profile.basic"}, nil,
	)
	if unavailableStatus != http.StatusServiceUnavailable || !bytes.Contains(unavailableBody, []byte(transport.ReasonScopeCatalogUnavailable)) {
		t.Fatalf("unavailable Auth response = status:%d body:%s", unavailableStatus, unavailableBody)
	}
	e2eAssertCollectionCount(t, database, "application_versions", 1)
	e2eAssertNextVersionSequence(t, database, applicationResponse.GetId(), 2)
	if calls := authServer.calls.Load(); calls < 3 {
		t.Fatalf("Auth Scope Catalog calls = %d, want at least 3 refresh attempts", calls)
	}
}

// TestE2E_UCAPP003_UpdateDraftApplicationVersionOverRealHTTP crosses the real
// PUT route, JWS verifier, Wire graph, generated Auth client/cache and MongoDB
// transaction. It also proves no-op and failed preconditions do not mutate the
// persisted revision or audit fields.
func TestE2E_UCAPP003_UpdateDraftApplicationVersionOverRealHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	_, database := e2eIsolatedDatabase(t, ctx, true)
	privateKey, publicKeyPath := e2eIdentity(t)
	const adminID = "auth-e2e-update-version"
	token := e2eSignIdentity(t, privateKey, adminID, "APPROVED")

	addresses := e2eReserveAddresses(t, 3)
	httpAddress, grpcAddress, authAddress := addresses[0], addresses[1], addresses[2]
	authServer := e2eStartScopeCatalogServer(t, authAddress)
	configuration, err := config.Load(e2eEnvironment(map[string]string{
		config.MongoURIEnv:               os.Getenv(mongoIntegrationURIEnv),
		config.MongoDatabaseEnv:          database.Name(),
		config.HTTPAddrEnv:               httpAddress,
		config.GRPCAddrEnv:               grpcAddress,
		config.IdentityIssuerEnv:         e2eIssuer,
		config.IdentityAudienceEnv:       e2eAudience,
		config.IdentityPublicKeysEnv:     e2eKeyID + "=" + publicKeyPath,
		config.AuthScopeCatalogTargetEnv: authAddress,
		config.ScopeCatalogCacheTTLEnv:   "1ns",
	}))
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}

	app, appCleanup, err := wireApp(configuration)
	if err != nil {
		t.Fatalf("wireApp() error = %v", err)
	}
	runDone := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run()
		close(runDone)
	}()
	t.Cleanup(func() {
		if stopErr := app.Stop(); stopErr != nil {
			t.Errorf("app.Stop() error = %v", stopErr)
		}
		select {
		case <-runDone:
		case <-time.After(15 * time.Second):
			t.Error("app.Run() did not stop within 15s")
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Errorf("app.Run() error = %v", runErr)
		}
		appCleanup()
	})
	e2eWaitForHTTPListener(t, ctx, httpAddress, runDone, &runErr)
	connection := e2eDialGRPC(t, ctx, grpcAddress, runDone, &runErr)
	t.Cleanup(func() { _ = connection.Close() })

	statusCode, applicationBody := e2eHTTPCreate(t, httpAddress, token, "Update_Version_E2E_App")
	if statusCode != http.StatusCreated {
		t.Fatalf("create predecessor application status = %d; body = %s", statusCode, applicationBody)
	}
	var application applicationv1.CreateApplicationResponse
	if err := protojson.Unmarshal(applicationBody, &application); err != nil {
		t.Fatalf("decode predecessor application: %v", err)
	}
	versionStatus, _, versionBody := e2eHTTPCreateVersion(
		t, httpAddress, token, application.GetId(), "v1.0.0", []string{"user.profile.v1"}, []string{"profile.basic"}, nil,
	)
	if versionStatus != http.StatusCreated {
		t.Fatalf("create predecessor version status = %d; body = %s", versionStatus, versionBody)
	}
	var created applicationversionv1.CreateApplicationVersionResponse
	if err := protojson.Unmarshal(versionBody, &created); err != nil {
		t.Fatalf("decode predecessor version: %v", err)
	}

	updateStatus, updateHeader, updateBody := e2eHTTPUpdateVersion(
		t, httpAddress, token, application.GetId(), created.GetVersionId(), `"1"`,
		"v1.1.0-beta", "http://192.168.1.20:8081/", 3, 6,
		[]string{"user.profile.v1", "camera.read.v1"}, []string{"schedule.read", "profile.basic"}, []string{"calendar.read"},
	)
	if updateStatus != http.StatusOK || updateHeader.Get("ETag") != `"2"` {
		t.Fatalf("update response = status:%d ETag:%q body:%s", updateStatus, updateHeader.Get("ETag"), updateBody)
	}
	var updated applicationversionv1.UpdateApplicationVersionResponse
	if err := protojson.Unmarshal(updateBody, &updated); err != nil {
		t.Fatalf("decode update response: %v; body = %s", err, updateBody)
	}
	if updated.GetRevision() != 2 || updated.GetVersionLabel() != "v1.1.0-beta" || updated.GetLaunchUrl() != "http://192.168.1.20:8081/" ||
		strings.Join(updated.GetRequiredCapabilities(), ",") != "camera.read.v1,user.profile.v1" ||
		strings.Join(updated.GetRequiredScopes(), ",") != "profile.basic,schedule.read" ||
		strings.Join(updated.GetOptionalScopes(), ",") != "calendar.read" {
		t.Fatalf("updated response = %v", &updated)
	}
	persisted := e2eFindApplicationVersion(t, database, created.GetVersionId())
	if persisted.Revision != 2 || persisted.VersionLabel != updated.GetVersionLabel() || persisted.UpdatedBy != adminID {
		t.Fatalf("persisted update = %#v", persisted)
	}
	updatedAt := persisted.UpdatedAt

	// Same normalized sets are a no-op: the ETag and audit timestamp remain 2.
	noOpStatus, noOpHeader, noOpBody := e2eHTTPUpdateVersion(
		t, httpAddress, token, application.GetId(), created.GetVersionId(), `"2"`,
		"v1.1.0-beta", "http://192.168.1.20:8081/", 3, 6,
		[]string{"camera.read.v1", "user.profile.v1"}, []string{"profile.basic", "schedule.read"}, []string{"calendar.read"},
	)
	if noOpStatus != http.StatusOK || noOpHeader.Get("ETag") != `"2"` {
		t.Fatalf("no-op response = status:%d ETag:%q body:%s", noOpStatus, noOpHeader.Get("ETag"), noOpBody)
	}
	noOpPersisted := e2eFindApplicationVersion(t, database, created.GetVersionId())
	if noOpPersisted.Revision != 2 || !noOpPersisted.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("no-op changed revision/audit: before=%s after=%#v", updatedAt, noOpPersisted)
	}

	// Native gRPC carries the same precondition in expected_revision rather
	// than an HTTP header and reaches the same composed handler/repository.
	grpcClient := applicationversionv1.NewApplicationVersionClient(connection)
	grpcCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, token))
	grpcUpdated, err := grpcClient.UpdateApplicationVersion(grpcCtx, &applicationversionv1.UpdateApplicationVersionRequest{
		ApplicationId:    application.GetId(),
		VersionId:        created.GetVersionId(),
		ExpectedRevision: 2,
		Replacement: &applicationversionv1.DraftApplicationVersionReplacement{
			VersionLabel:              "v1.1.1",
			LaunchUrl:                 "https://example.edu/apps/course-table/v1.1.1/",
			RpcApiMinVersion:          3,
			RpcApiMaxVersionExclusive: 6,
			RequiredCapabilities:      []string{"camera.read.v1"},
			RequiredScopes:            []string{"profile.basic"},
			OptionalScopes:            []string{"calendar.read"},
		},
	})
	if err != nil {
		t.Fatalf("gRPC UpdateApplicationVersion() error = %v", err)
	}
	if grpcUpdated.GetRevision() != 3 || grpcUpdated.GetVersionLabel() != "v1.1.1" {
		t.Fatalf("gRPC updated response = %v", grpcUpdated)
	}
	grpcPersisted := e2eFindApplicationVersion(t, database, created.GetVersionId())
	if grpcPersisted.Revision != 3 || grpcPersisted.VersionLabel != "v1.1.1" {
		t.Fatalf("gRPC persisted update = %#v", grpcPersisted)
	}
	updatedAt = grpcPersisted.UpdatedAt

	staleStatus, _, staleBody := e2eHTTPUpdateVersion(
		t, httpAddress, token, application.GetId(), created.GetVersionId(), `"2"`,
		"v1.2.0", "https://example.edu/v1.2", 3, 6, nil, []string{"profile.basic"}, nil,
	)
	if staleStatus != http.StatusPreconditionFailed || !bytes.Contains(staleBody, []byte(transport.ReasonApplicationVersionRevisionConflict)) {
		t.Fatalf("stale revision response = status:%d body:%s", staleStatus, staleBody)
	}
	e2eAssertVersionUnchanged(t, database, created.GetVersionId(), "v1.1.1", 3, updatedAt)

	authServer.unavailable.Store(true)
	unavailableStatus, _, unavailableBody := e2eHTTPUpdateVersion(
		t, httpAddress, token, application.GetId(), created.GetVersionId(), `"3"`,
		"v1.2.0", "https://example.edu/v1.2", 3, 6, nil, []string{"profile.basic"}, nil,
	)
	if unavailableStatus != http.StatusServiceUnavailable || !bytes.Contains(unavailableBody, []byte(transport.ReasonScopeCatalogUnavailable)) {
		t.Fatalf("unavailable Auth response = status:%d body:%s", unavailableStatus, unavailableBody)
	}
	e2eAssertVersionUnchanged(t, database, created.GetVersionId(), "v1.1.1", 3, updatedAt)
}

// TestE2E_UCAPP004_SubmitApplicationVersionReview crosses both generated
// transports, a real JWS verifier, generated Auth gRPC consumer/cache, the real
// DNS-only preflight policy (only its resolver is deterministic), Wire and the
// transaction-capable MongoDB repository.
func TestE2E_UCAPP004_SubmitApplicationVersionReview(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	_, database := e2eIsolatedDatabase(t, ctx, true)
	privateKey, publicKeyPath := e2eIdentity(t)
	const adminID = "auth-e2e-submit-review"
	token := e2eSignIdentity(t, privateKey, adminID, "APPROVED")

	addresses := e2eReserveAddresses(t, 3)
	httpAddress, grpcAddress, authAddress := addresses[0], addresses[1], addresses[2]
	authServer := e2eStartScopeCatalogServer(t, authAddress)
	resolver := &e2eDNSResolver{
		addresses: map[string][]netip.Addr{
			"reviewable.e2e.edu": {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("2606:4700:4700::1111")},
			"private.e2e.edu":    {netip.MustParseAddr("10.0.0.8")},
		},
		errors: map[string]error{"dns-down.e2e.edu": errors.New("test DNS unavailable")},
	}
	configuration, err := config.Load(e2eEnvironment(map[string]string{
		config.MongoURIEnv:               os.Getenv(mongoIntegrationURIEnv),
		config.MongoDatabaseEnv:          database.Name(),
		config.HTTPAddrEnv:               httpAddress,
		config.GRPCAddrEnv:               grpcAddress,
		config.IdentityIssuerEnv:         e2eIssuer,
		config.IdentityAudienceEnv:       e2eAudience,
		config.IdentityPublicKeysEnv:     e2eKeyID + "=" + publicKeyPath,
		config.AuthScopeCatalogTargetEnv: authAddress,
		config.ScopeCatalogCacheTTLEnv:   "1ns",
	}))
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}

	app, appCleanup, err := wireAppWithResolver(configuration, resolver)
	if err != nil {
		t.Fatalf("wireAppWithResolver() error = %v", err)
	}
	runDone := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run()
		close(runDone)
	}()
	t.Cleanup(func() {
		if stopErr := app.Stop(); stopErr != nil {
			t.Errorf("app.Stop() error = %v", stopErr)
		}
		select {
		case <-runDone:
		case <-time.After(15 * time.Second):
			t.Error("app.Run() did not stop within 15s")
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Errorf("app.Run() error = %v", runErr)
		}
		appCleanup()
	})
	e2eWaitForHTTPListener(t, ctx, httpAddress, runDone, &runErr)
	connection := e2eDialGRPC(t, ctx, grpcAddress, runDone, &runErr)
	t.Cleanup(func() { _ = connection.Close() })

	statusCode, applicationBody := e2eHTTPCreate(t, httpAddress, token, "Submit_Review_E2E_App")
	if statusCode != http.StatusCreated {
		t.Fatalf("create predecessor application status = %d; body = %s", statusCode, applicationBody)
	}
	var application applicationv1.CreateApplicationResponse
	if err := protojson.Unmarshal(applicationBody, &application); err != nil {
		t.Fatalf("decode predecessor application: %v", err)
	}

	createDraft := func(t *testing.T, label, launchURL string) *applicationversionv1.UpdateApplicationVersionResponse {
		t.Helper()
		createStatus, _, createBody := e2eHTTPCreateVersion(
			t, httpAddress, token, application.GetId(), label, []string{"user.profile.v1"}, []string{"profile.basic"}, nil,
		)
		if createStatus != http.StatusCreated {
			t.Fatalf("create predecessor version status = %d; body = %s", createStatus, createBody)
		}
		var created applicationversionv1.CreateApplicationVersionResponse
		if err := protojson.Unmarshal(createBody, &created); err != nil {
			t.Fatalf("decode predecessor version: %v", err)
		}
		updateStatus, _, updateBody := e2eHTTPUpdateVersion(
			t, httpAddress, token, application.GetId(), created.GetVersionId(), `"1"`, label, launchURL,
			3, 5, []string{"user.profile.v1"}, []string{"profile.basic"}, nil,
		)
		if updateStatus != http.StatusOK {
			t.Fatalf("prepare predecessor version status = %d; body = %s", updateStatus, updateBody)
		}
		var updated applicationversionv1.UpdateApplicationVersionResponse
		if err := protojson.Unmarshal(updateBody, &updated); err != nil {
			t.Fatalf("decode prepared version: %v", err)
		}
		return &updated
	}

	// HTTP success proves 201, Location, complete snapshot/result and persisted
	// all-or-nothing lifecycle transition.
	httpDraft := createDraft(t, "v4.0.0-http", "https://reviewable.e2e.edu/http")
	submitStatus, submitHeader, submitBody := e2eHTTPSubmitReview(
		t, httpAddress, token, application.GetId(), httpDraft.GetVersionId(), 2,
	)
	if submitStatus != http.StatusCreated {
		t.Fatalf("submit review response = status:%d body:%s", submitStatus, submitBody)
	}
	var submitted applicationreviewv1.SubmitApplicationVersionReviewResponse
	if err := protojson.Unmarshal(submitBody, &submitted); err != nil {
		t.Fatalf("decode submit response: %v; body = %s", err, submitBody)
	}
	review := submitted.GetReview()
	version := submitted.GetVersion()
	wantLocation := "/v1/applications/" + application.GetId() + "/versions/" + httpDraft.GetVersionId() + "/reviews/" + review.GetReviewId()
	if submitHeader.Get("Location") != wantLocation || !e2eIsUUIDv7(review.GetReviewId()) {
		t.Fatalf("Location/review ID = %q / %q", submitHeader.Get("Location"), review.GetReviewId())
	}
	if review.GetStatus() != "PENDING" || review.GetAttempt() != 1 || review.GetSourceVersionRevision() != 2 ||
		review.GetScopeCatalogRevision() != 11 || review.GetPreflightPolicyVersion() != "submit-v1" ||
		review.GetSubmittedBy() != adminID || review.GetSnapshot().GetLaunchUrl() != "https://reviewable.e2e.edu/http" ||
		version.GetReviewStatus() != "SUBMITTED" || version.GetRevision() != 3 || version.GetUpdatedBy() != adminID {
		t.Fatalf("submitted response = %v", &submitted)
	}
	e2eAssertReviewPersisted(t, database, review)
	e2eAssertCollectionCount(t, database, "application_reviews", 1)
	persistedHTTPVersion := e2eFindApplicationVersion(t, database, httpDraft.GetVersionId())
	if persistedHTTPVersion.ReviewStatus != "SUBMITTED" || persistedHTTPVersion.Revision != 3 {
		t.Fatalf("persisted submitted version = %#v", persistedHTTPVersion)
	}

	// Native gRPC carries expected_revision in the generated command and reaches
	// the same real policy/repository graph.
	grpcDraft := createDraft(t, "v4.0.0-grpc", "https://reviewable.e2e.edu/grpc")
	grpcClient := applicationreviewv1.NewApplicationReviewClient(connection)
	grpcCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, token))
	grpcSubmitted, err := grpcClient.SubmitApplicationVersionReview(grpcCtx, &applicationreviewv1.SubmitApplicationVersionReviewRequest{
		ApplicationId: application.GetId(),
		VersionId:     grpcDraft.GetVersionId(),
		Command:       &applicationreviewv1.SubmitApplicationVersionReviewCommand{ExpectedRevision: 2},
	})
	if err != nil || grpcSubmitted.GetVersion().GetRevision() != 3 || grpcSubmitted.GetReview().GetStatus() != "PENDING" {
		t.Fatalf("gRPC SubmitApplicationVersionReview() = (%v, %v)", grpcSubmitted, err)
	}
	e2eAssertCollectionCount(t, database, "application_reviews", 2)

	// A private answer is a policy failure (422), DNS failure is dependency
	// unavailable (503), and neither may create a Review or mutate the draft.
	privateDraft := createDraft(t, "v4.0.0-private", "https://private.e2e.edu/app")
	privateBefore := e2eFindApplicationVersion(t, database, privateDraft.GetVersionId())
	privateStatus, _, privateBody := e2eHTTPSubmitReview(t, httpAddress, token, application.GetId(), privateDraft.GetVersionId(), 2)
	if privateStatus != http.StatusUnprocessableEntity || !bytes.Contains(privateBody, []byte(transport.ReasonApplicationLaunchURLNotReviewable)) {
		t.Fatalf("private URL response = status:%d body:%s", privateStatus, privateBody)
	}
	e2eAssertReviewFailureUnchanged(t, database, privateBefore, 2)

	dnsDraft := createDraft(t, "v4.0.0-dns", "https://dns-down.e2e.edu/app")
	dnsBefore := e2eFindApplicationVersion(t, database, dnsDraft.GetVersionId())
	dnsStatus, _, dnsBody := e2eHTTPSubmitReview(t, httpAddress, token, application.GetId(), dnsDraft.GetVersionId(), 2)
	if dnsStatus != http.StatusServiceUnavailable || !bytes.Contains(dnsBody, []byte(transport.ReasonLaunchURLInspectionUnavailable)) {
		t.Fatalf("DNS unavailable response = status:%d body:%s", dnsStatus, dnsBody)
	}
	e2eAssertReviewFailureUnchanged(t, database, dnsBefore, 2)

	// A stale revision fails before external preflight and preserves both sides.
	staleStatus, _, staleBody := e2eHTTPSubmitReview(t, httpAddress, token, application.GetId(), privateDraft.GetVersionId(), 1)
	if staleStatus != http.StatusConflict || !bytes.Contains(staleBody, []byte(transport.ReasonApplicationVersionRevisionConflict)) {
		t.Fatalf("stale revision response = status:%d body:%s", staleStatus, staleBody)
	}
	e2eAssertReviewFailureUnchanged(t, database, privateBefore, 2)

	// A scope that became non-requestable after draft editing is revalidated at
	// submission and fails with 422 without a Review or lifecycle mutation.
	scopeInvalidDraft := createDraft(t, "v4.0.0-invalid-scope", "https://reviewable.e2e.edu/invalid-scope")
	scopeInvalidBefore := e2eFindApplicationVersion(t, database, scopeInvalidDraft.GetVersionId())
	authServer.revision.Store(12)
	authServer.denyProfile.Store(true)
	invalidScopeStatus, _, invalidScopeBody := e2eHTTPSubmitReview(
		t, httpAddress, token, application.GetId(), scopeInvalidDraft.GetVersionId(), 2,
	)
	authServer.denyProfile.Store(false)
	authServer.revision.Store(13)
	if invalidScopeStatus != http.StatusUnprocessableEntity || !bytes.Contains(invalidScopeBody, []byte(transport.ReasonInvalidApplicationScope)) {
		t.Fatalf("invalid Scope response = status:%d body:%s", invalidScopeStatus, invalidScopeBody)
	}
	e2eAssertReviewFailureUnchanged(t, database, scopeInvalidBefore, 2)

	// Expired Scope Catalog cache fails closed when Auth is unavailable; the
	// DNS policy and MongoDB Submit cannot turn it into a partial success.
	scopeDraft := createDraft(t, "v4.0.0-scope", "https://reviewable.e2e.edu/scope")
	scopeBefore := e2eFindApplicationVersion(t, database, scopeDraft.GetVersionId())
	authServer.unavailable.Store(true)
	scopeStatus, _, scopeBody := e2eHTTPSubmitReview(t, httpAddress, token, application.GetId(), scopeDraft.GetVersionId(), 2)
	if scopeStatus != http.StatusServiceUnavailable || !bytes.Contains(scopeBody, []byte(transport.ReasonScopeCatalogUnavailable)) {
		t.Fatalf("Scope Catalog unavailable response = status:%d body:%s", scopeStatus, scopeBody)
	}
	e2eAssertReviewFailureUnchanged(t, database, scopeBefore, 2)
}

// TestE2E_UCAPP005_DecideApplicationVersionReview crosses a pure Reviewer JWS,
// real HTTP and gRPC listeners, the generated Auth Developer Status client, a
// generated-interface test Auth server, the Wire graph and real MongoDB.
func TestE2E_UCAPP005_DecideApplicationVersionReview(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	_, database := e2eIsolatedDatabase(t, ctx, true)
	privateKey, publicKeyPath := e2eIdentity(t)
	const (
		adminID    = "auth-e2e-review-admin"
		reviewerID = "auth-e2e-reviewer"
		systemID   = "auth-e2e-system"
	)
	adminToken := e2eSignIdentity(t, privateKey, adminID, "APPROVED")
	reviewerToken := e2eSignReviewerIdentity(t, privateKey, reviewerID, "app.version.review")
	noPermissionToken := e2eSignReviewerIdentity(t, privateKey, "auth-e2e-no-review-permission")

	addresses := e2eReserveAddresses(t, 3)
	httpAddress, grpcAddress, authAddress := addresses[0], addresses[1], addresses[2]
	_, developerStatusServer := e2eStartAuthServer(t, authAddress)
	developerStatusServer.systemAuthID = systemID
	developerStatusServer.SetStatus(adminID, developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_APPROVED)
	resolver := &e2eDNSResolver{addresses: map[string][]netip.Addr{
		"example.edu": {netip.MustParseAddr("8.8.8.8")},
	}}
	configuration, err := config.Load(e2eEnvironment(map[string]string{
		config.MongoURIEnv:               os.Getenv(mongoIntegrationURIEnv),
		config.MongoDatabaseEnv:          database.Name(),
		config.HTTPAddrEnv:               httpAddress,
		config.GRPCAddrEnv:               grpcAddress,
		config.IdentityIssuerEnv:         e2eIssuer,
		config.IdentityAudienceEnv:       e2eAudience,
		config.IdentityPublicKeysEnv:     e2eKeyID + "=" + publicKeyPath,
		config.AuthScopeCatalogTargetEnv: authAddress,
		config.ScopeCatalogCacheTTLEnv:   "1ns",
	}))
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	app, appCleanup, err := wireAppWithResolver(configuration, resolver)
	if err != nil {
		t.Fatalf("wireAppWithResolver() error = %v", err)
	}
	runDone := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run()
		close(runDone)
	}()
	t.Cleanup(func() {
		if stopErr := app.Stop(); stopErr != nil {
			t.Errorf("app.Stop() error = %v", stopErr)
		}
		select {
		case <-runDone:
		case <-time.After(15 * time.Second):
			t.Error("app.Run() did not stop within 15s")
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Errorf("app.Run() error = %v", runErr)
		}
		appCleanup()
	})
	e2eWaitForHTTPListener(t, ctx, httpAddress, runDone, &runErr)
	connection := e2eDialGRPC(t, ctx, grpcAddress, runDone, &runErr)
	t.Cleanup(func() { _ = connection.Close() })

	createStatus, applicationBody := e2eHTTPCreate(t, httpAddress, adminToken, "Review_Decision_E2E_App")
	if createStatus != http.StatusCreated {
		t.Fatalf("create predecessor application status = %d; body = %s", createStatus, applicationBody)
	}
	var application applicationv1.CreateApplicationResponse
	if err := protojson.Unmarshal(applicationBody, &application); err != nil {
		t.Fatalf("decode predecessor application: %v", err)
	}

	createAndSubmit := func(label string) (*applicationversionv1.CreateApplicationVersionResponse, *applicationreviewv1.SubmitApplicationVersionReviewResponse) {
		t.Helper()
		versionStatus, _, versionBody := e2eHTTPCreateVersion(
			t, httpAddress, adminToken, application.GetId(), label,
			[]string{"user.profile.v1"}, []string{"profile.basic"}, nil,
		)
		if versionStatus != http.StatusCreated {
			t.Fatalf("create predecessor version status = %d; body = %s", versionStatus, versionBody)
		}
		var version applicationversionv1.CreateApplicationVersionResponse
		if err := protojson.Unmarshal(versionBody, &version); err != nil {
			t.Fatalf("decode predecessor version: %v", err)
		}
		submitStatus, _, submitBody := e2eHTTPSubmitReview(t, httpAddress, adminToken, application.GetId(), version.GetVersionId(), 1)
		if submitStatus != http.StatusCreated {
			t.Fatalf("submit predecessor review status = %d; body = %s", submitStatus, submitBody)
		}
		var submitted applicationreviewv1.SubmitApplicationVersionReviewResponse
		if err := protojson.Unmarshal(submitBody, &submitted); err != nil {
			t.Fatalf("decode predecessor review: %v", err)
		}
		return &version, &submitted
	}

	approvedVersion, approvedReview := createAndSubmit("v5.0.0")
	deniedStatus, deniedBody := e2eHTTPDecideReview(
		t, httpAddress, noPermissionToken, application.GetId(), approvedVersion.GetVersionId(), approvedReview.GetReview().GetReviewId(),
		"APPROVE", "app-version-review-v1", []string{"content-policy-reviewed", "launch-url-content-reviewed", "requested-access-reviewed"}, "",
	)
	if deniedStatus != http.StatusForbidden || !bytes.Contains(deniedBody, []byte(transport.ReasonApplicationReviewPermissionRequired)) {
		t.Fatalf("permission response = status:%d body:%s", deniedStatus, deniedBody)
	}
	approveStatus, approveBody := e2eHTTPDecideReview(
		t, httpAddress, reviewerToken, application.GetId(), approvedVersion.GetVersionId(), approvedReview.GetReview().GetReviewId(),
		"APPROVE", "app-version-review-v1", []string{"requested-access-reviewed", "content-policy-reviewed", "launch-url-content-reviewed"}, "",
	)
	if approveStatus != http.StatusOK {
		t.Fatalf("approve response = status:%d body:%s", approveStatus, approveBody)
	}
	var approved applicationreviewv1.DecideApplicationVersionReviewResponse
	if err := protojson.Unmarshal(approveBody, &approved); err != nil {
		t.Fatalf("decode approval response: %v; body=%s", err, approveBody)
	}
	if approved.GetReview().GetStatus() != "APPROVED" || approved.GetReview().GetDecision().GetDecidedBy() != reviewerID ||
		approved.GetReview().GetDecision().GetApprovalValidation().GetScopeCatalogRevision() != 11 ||
		approved.GetVersion().GetReviewStatus() != "APPROVED" || approved.GetVersion().GetRevision() != 3 {
		t.Fatalf("approval response = %v", &approved)
	}

	_, suspendedReview := createAndSubmit("v5.0.1")
	developerStatusServer.SetStatus(adminID, developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_SUSPENDED)
	grpcClient := applicationreviewv1.NewApplicationReviewClient(connection)
	grpcCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, reviewerToken))
	autoRejected, err := grpcClient.DecideApplicationVersionReview(grpcCtx, &applicationreviewv1.DecideApplicationVersionReviewRequest{
		ApplicationId: application.GetId(),
		VersionId:     suspendedReview.GetReview().GetVersionId(),
		ReviewId:      suspendedReview.GetReview().GetReviewId(),
		Command: &applicationreviewv1.DecideApplicationVersionReviewCommand{
			Outcome:               applicationreviewv1.ReviewDecisionAction_APPROVE,
			ExpectedPolicyVersion: "app-version-review-v1",
			ConfirmedCheckIds:     []string{"content-policy-reviewed", "launch-url-content-reviewed", "requested-access-reviewed"},
		},
	})
	if err != nil {
		t.Fatalf("gRPC auto-reject decision: %v", err)
	}
	if autoRejected.GetReview().GetStatus() != "REJECTED" || autoRejected.GetReview().GetDecision().GetDecidedBy() != systemID ||
		autoRejected.GetReview().GetDecision().GetApprovalValidation() != nil ||
		autoRejected.GetVersion().GetUpdatedBy() != systemID || autoRejected.GetVersion().GetReviewStatus() != "REJECTED" {
		t.Fatalf("auto-rejection response = %v", autoRejected)
	}
	var stored e2eApplicationReviewDocument
	if err := database.Collection("application_reviews").FindOne(ctx, bson.D{{Key: "reviewId", Value: suspendedReview.GetReview().GetReviewId()}}).Decode(&stored); err != nil {
		t.Fatalf("read auto-rejected review: %v", err)
	}
	if stored.Decision == nil || stored.Decision.DecidedBy != systemID || stored.Decision.Outcome != "REJECTED" ||
		stored.Decision.ApprovalValidation != nil || stored.Decision.Reason == nil {
		t.Fatalf("persisted auto-rejection = %#v", stored)
	}
	if developerStatusServer.calls.Load() < 2 {
		t.Fatalf("Auth Developer Status calls = %d, want approval and auto-rejection checks", developerStatusServer.calls.Load())
	}
}

// TestE2E_UCAPP005_RealAuthProcessServiceIdentity is run by
// scripts/test-auth-app-integration.sh. It composes the real App server in this
// process, but all three Auth dependencies cross a TCP connection to a separate
// real Auth Center process protected by its production service-JWS interceptor.
func TestE2E_UCAPP005_RealAuthProcessServiceIdentity(t *testing.T) {
	authTarget := os.Getenv(authCenterIntegrationTargetEnv)
	authDatabaseName := os.Getenv(authCenterIntegrationDatabaseEnv)
	serviceID := os.Getenv(config.ServiceIdentityIDEnv)
	serviceKID := os.Getenv(config.ServiceIdentityKIDEnv)
	servicePrivateKey := os.Getenv(config.ServiceIdentityPrivateKeyEnv)
	if authTarget == "" || authDatabaseName == "" || serviceID == "" || serviceKID == "" || servicePrivateKey == "" {
		t.Skipf("run scripts/test-auth-app-integration.sh for the real Auth+App E2E")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	client, database := e2eIsolatedDatabase(t, ctx, true)
	privateKey, publicKeyPath := e2eIdentity(t)
	const (
		adminID    = "auth-dual-service-admin"
		reviewerID = "auth-dual-service-reviewer"
	)
	adminToken := e2eSignIdentity(t, privateKey, adminID, "APPROVED")
	reviewerToken := e2eSignReviewerIdentity(t, privateKey, reviewerID, "app.version.review")

	addresses := e2eReserveAddresses(t, 2)
	httpAddress, grpcAddress := addresses[0], addresses[1]
	resolver := &e2eDNSResolver{addresses: map[string][]netip.Addr{
		"example.edu": {netip.MustParseAddr("8.8.8.8")},
	}}
	configuration, err := config.Load(e2eEnvironment(map[string]string{
		config.MongoURIEnv:                  os.Getenv(mongoIntegrationURIEnv),
		config.MongoDatabaseEnv:             database.Name(),
		config.HTTPAddrEnv:                  httpAddress,
		config.GRPCAddrEnv:                  grpcAddress,
		config.IdentityIssuerEnv:            e2eIssuer,
		config.IdentityAudienceEnv:          e2eAudience,
		config.IdentityPublicKeysEnv:        e2eKeyID + "=" + publicKeyPath,
		config.AuthScopeCatalogTargetEnv:    authTarget,
		config.ScopeCatalogCacheTTLEnv:      "1ns",
		config.ServiceIdentityIDEnv:         serviceID,
		config.ServiceIdentityKIDEnv:        serviceKID,
		config.ServiceIdentityPrivateKeyEnv: servicePrivateKey,
	}))
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	app, appCleanup, err := wireAppWithResolver(configuration, resolver)
	if err != nil {
		t.Fatalf("wireAppWithResolver() error = %v", err)
	}
	runDone := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run()
		close(runDone)
	}()
	t.Cleanup(func() {
		if stopErr := app.Stop(); stopErr != nil {
			t.Errorf("app.Stop() error = %v", stopErr)
		}
		select {
		case <-runDone:
		case <-time.After(15 * time.Second):
			t.Error("app.Run() did not stop within 15s")
		}
		appCleanup()
	})
	e2eWaitForHTTPListener(t, ctx, httpAddress, runDone, &runErr)
	connection := e2eDialGRPC(t, ctx, grpcAddress, runDone, &runErr)
	t.Cleanup(func() { _ = connection.Close() })

	createStatus, applicationBody := e2eHTTPCreate(t, httpAddress, adminToken, "Dual_Service_Review_App")
	if createStatus != http.StatusCreated {
		t.Fatalf("create application status = %d; body = %s", createStatus, applicationBody)
	}
	var application applicationv1.CreateApplicationResponse
	if err := protojson.Unmarshal(applicationBody, &application); err != nil {
		t.Fatalf("decode application: %v", err)
	}
	versionStatus, _, versionBody := e2eHTTPCreateVersion(
		t, httpAddress, adminToken, application.GetId(), "v1.0.0",
		[]string{"user.profile.v1"}, []string{"profile.basic"}, nil,
	)
	if versionStatus != http.StatusCreated {
		t.Fatalf("create version status = %d; body = %s", versionStatus, versionBody)
	}
	var version applicationversionv1.CreateApplicationVersionResponse
	if err := protojson.Unmarshal(versionBody, &version); err != nil {
		t.Fatalf("decode version: %v", err)
	}
	submitStatus, _, submitBody := e2eHTTPSubmitReview(t, httpAddress, adminToken, application.GetId(), version.GetVersionId(), 1)
	if submitStatus != http.StatusCreated {
		t.Fatalf("submit review status = %d; body = %s", submitStatus, submitBody)
	}
	var submitted applicationreviewv1.SubmitApplicationVersionReviewResponse
	if err := protojson.Unmarshal(submitBody, &submitted); err != nil {
		t.Fatalf("decode submitted review: %v", err)
	}

	grpcClient := applicationreviewv1.NewApplicationReviewClient(connection)
	grpcCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, reviewerToken))
	result, err := grpcClient.DecideApplicationVersionReview(grpcCtx, &applicationreviewv1.DecideApplicationVersionReviewRequest{
		ApplicationId: application.GetId(), VersionId: version.GetVersionId(), ReviewId: submitted.GetReview().GetReviewId(),
		Command: &applicationreviewv1.DecideApplicationVersionReviewCommand{
			Outcome: applicationreviewv1.ReviewDecisionAction_APPROVE, ExpectedPolicyVersion: "app-version-review-v1",
			ConfirmedCheckIds: []string{"content-policy-reviewed", "launch-url-content-reviewed", "requested-access-reviewed"},
		},
	})
	if err != nil {
		t.Fatalf("DecideApplicationVersionReview() error = %v", err)
	}
	var systemPrincipal struct {
		AuthID string `bson:"authId"`
	}
	if err := client.Database(authDatabaseName).Collection("auth_principals").FindOne(ctx, bson.M{
		"principalType": "SYSTEM", "systemPurpose": "app-center.review-auto-rejection",
	}).Decode(&systemPrincipal); err != nil {
		t.Fatalf("read Auth SYSTEM principal: %v", err)
	}
	if result.GetReview().GetStatus() != "REJECTED" || systemPrincipal.AuthID == "" ||
		result.GetReview().GetDecision().GetDecidedBy() != systemPrincipal.AuthID ||
		result.GetVersion().GetUpdatedBy() != systemPrincipal.AuthID {
		t.Fatalf("real Auth auto-rejection = result:%v system:%q", result, systemPrincipal.AuthID)
	}
}

// TestE2E_UCAPP006_RestoreRejectedApplicationVersionToDraft starts from the
// rejected state owned by UC-APP-005, then crosses the generated HTTP and gRPC
// contracts, trusted JWS middleware, Wire graph and real MongoDB transaction.
func TestE2E_UCAPP006_RestoreRejectedApplicationVersionToDraft(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	_, database := e2eIsolatedDatabase(t, ctx, true)
	privateKey, publicKeyPath := e2eIdentity(t)
	const adminID = "auth-e2e-restore-review"
	adminToken := e2eSignIdentity(t, privateKey, adminID, "APPROVED")
	otherToken := e2eSignIdentity(t, privateKey, "auth-e2e-other-admin", "APPROVED")

	addresses := e2eReserveAddresses(t, 3)
	httpAddress, grpcAddress, authAddress := addresses[0], addresses[1], addresses[2]
	e2eStartScopeCatalogServer(t, authAddress)
	resolver := &e2eDNSResolver{addresses: map[string][]netip.Addr{
		"example.edu": {netip.MustParseAddr("8.8.8.8")},
	}}
	configuration, err := config.Load(e2eEnvironment(map[string]string{
		config.MongoURIEnv:               os.Getenv(mongoIntegrationURIEnv),
		config.MongoDatabaseEnv:          database.Name(),
		config.HTTPAddrEnv:               httpAddress,
		config.GRPCAddrEnv:               grpcAddress,
		config.IdentityIssuerEnv:         e2eIssuer,
		config.IdentityAudienceEnv:       e2eAudience,
		config.IdentityPublicKeysEnv:     e2eKeyID + "=" + publicKeyPath,
		config.AuthScopeCatalogTargetEnv: authAddress,
	}))
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	app, appCleanup, err := wireAppWithResolver(configuration, resolver)
	if err != nil {
		t.Fatalf("wireAppWithResolver() error = %v", err)
	}
	runDone := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run()
		close(runDone)
	}()
	t.Cleanup(func() {
		if stopErr := app.Stop(); stopErr != nil {
			t.Errorf("app.Stop() error = %v", stopErr)
		}
		select {
		case <-runDone:
		case <-time.After(15 * time.Second):
			t.Error("app.Run() did not stop within 15s")
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Errorf("app.Run() error = %v", runErr)
		}
		appCleanup()
	})
	e2eWaitForHTTPListener(t, ctx, httpAddress, runDone, &runErr)
	connection := e2eDialGRPC(t, ctx, grpcAddress, runDone, &runErr)
	t.Cleanup(func() { _ = connection.Close() })

	statusCode, applicationBody := e2eHTTPCreate(t, httpAddress, adminToken, "Restore_Review_E2E_App")
	if statusCode != http.StatusCreated {
		t.Fatalf("create predecessor application status = %d; body = %s", statusCode, applicationBody)
	}
	var application applicationv1.CreateApplicationResponse
	if err := protojson.Unmarshal(applicationBody, &application); err != nil {
		t.Fatalf("decode predecessor application: %v", err)
	}
	createStatus, _, createBody := e2eHTTPCreateVersion(
		t, httpAddress, adminToken, application.GetId(), "v6.0.0", []string{"user.profile.v1"}, []string{"profile.basic"}, nil,
	)
	if createStatus != http.StatusCreated {
		t.Fatalf("create predecessor version status = %d; body = %s", createStatus, createBody)
	}
	var created applicationversionv1.CreateApplicationVersionResponse
	if err := protojson.Unmarshal(createBody, &created); err != nil {
		t.Fatalf("decode predecessor version: %v", err)
	}
	submitStatus, _, submitBody := e2eHTTPSubmitReview(t, httpAddress, adminToken, application.GetId(), created.GetVersionId(), 1)
	if submitStatus != http.StatusCreated {
		t.Fatalf("submit predecessor review status = %d; body = %s", submitStatus, submitBody)
	}
	var submitted applicationreviewv1.SubmitApplicationVersionReviewResponse
	if err := protojson.Unmarshal(submitBody, &submitted); err != nil {
		t.Fatalf("decode predecessor review: %v", err)
	}
	reviewID := submitted.GetReview().GetReviewId()
	decidedAt := time.Now().UTC().Truncate(time.Millisecond)
	reason := "E2E rejection precondition"
	if result, err := database.Collection("application_reviews").UpdateOne(
		ctx,
		bson.D{{Key: "reviewId", Value: reviewID}, {Key: "status", Value: "PENDING"}},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "status", Value: "REJECTED"},
			{Key: "decision", Value: bson.D{
				{Key: "outcome", Value: "REJECTED"},
				{Key: "reviewPolicyVersion", Value: "review.v1"},
				{Key: "confirmedCheckIds", Value: bson.A{}},
				{Key: "reason", Value: reason},
				{Key: "decidedBy", Value: "auth-e2e-reviewer"},
				{Key: "decidedAt", Value: decidedAt},
				{Key: "approvalValidation", Value: nil},
			}},
		}}},
	); err != nil || result.ModifiedCount != 1 {
		t.Fatalf("seed rejected review: result=%#v error=%v", result, err)
	}
	if result, err := database.Collection("application_versions").UpdateOne(
		ctx,
		bson.D{{Key: "versionId", Value: created.GetVersionId()}, {Key: "reviewStatus", Value: "SUBMITTED"}, {Key: "revision", Value: int64(2)}},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "reviewStatus", Value: "REJECTED"}, {Key: "revision", Value: int64(3)},
			{Key: "updatedBy", Value: "auth-e2e-reviewer"}, {Key: "updatedAt", Value: decidedAt},
		}}},
	); err != nil || result.ModifiedCount != 1 {
		t.Fatalf("seed rejected version: result=%#v error=%v", result, err)
	}

	// Authorization and optimistic concurrency fail without changing either
	// document before the successful command.
	otherStatus, otherBody := e2eHTTPRestoreReview(t, httpAddress, otherToken, application.GetId(), created.GetVersionId(), reviewID, 3)
	if otherStatus != http.StatusForbidden || !bytes.Contains(otherBody, []byte(transport.ReasonApplicationAdminRequired)) {
		t.Fatalf("non-admin response = status:%d body:%s", otherStatus, otherBody)
	}
	staleStatus, staleBody := e2eHTTPRestoreReview(t, httpAddress, adminToken, application.GetId(), created.GetVersionId(), reviewID, 2)
	if staleStatus != http.StatusConflict || !bytes.Contains(staleBody, []byte(transport.ReasonApplicationVersionRevisionConflict)) {
		t.Fatalf("stale response = status:%d body:%s", staleStatus, staleBody)
	}
	if version := e2eFindApplicationVersion(t, database, created.GetVersionId()); version.ReviewStatus != "REJECTED" || version.Revision != 3 {
		t.Fatalf("failed restore changed version: %#v", version)
	}

	restoreStatus, restoreBody := e2eHTTPRestoreReview(t, httpAddress, adminToken, application.GetId(), created.GetVersionId(), reviewID, 3)
	if restoreStatus != http.StatusOK {
		t.Fatalf("restore response = status:%d body:%s", restoreStatus, restoreBody)
	}
	var restored applicationreviewv1.RestoreRejectedApplicationVersionToDraftResponse
	if err := protojson.Unmarshal(restoreBody, &restored); err != nil {
		t.Fatalf("decode restore response: %v; body=%s", err, restoreBody)
	}
	if restored.GetReview().GetStatus() != "REJECTED" || restored.GetReview().GetDraftRestoration().GetRestoredBy() != adminID ||
		restored.GetReview().GetDraftRestoration().GetResultVersionRevision() != 4 ||
		restored.GetVersion().GetReviewStatus() != "DRAFT" || restored.GetVersion().GetRevision() != 4 || restored.GetVersion().GetUpdatedBy() != adminID {
		t.Fatalf("restored response = %v", &restored)
	}
	var storedReview e2eApplicationReviewDocument
	if err := database.Collection("application_reviews").FindOne(ctx, bson.D{{Key: "reviewId", Value: reviewID}}).Decode(&storedReview); err != nil {
		t.Fatalf("read restored review: %v", err)
	}
	storedVersion := e2eFindApplicationVersion(t, database, created.GetVersionId())
	if storedReview.Status != "REJECTED" || storedReview.DraftRestoration == nil || storedReview.DraftRestoration.RestoredBy != adminID ||
		storedReview.DraftRestoration.ResultVersionRevision != 4 || storedVersion.ReviewStatus != "DRAFT" || storedVersion.Revision != 4 ||
		!storedVersion.UpdatedAt.Equal(storedReview.DraftRestoration.RestoredAt) {
		t.Fatalf("persisted restoration = review:%#v version:%#v", storedReview, storedVersion)
	}

	// Native gRPC observes the same one-time invariant and stable error reason.
	grpcClient := applicationreviewv1.NewApplicationReviewClient(connection)
	grpcCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, adminToken))
	_, err = grpcClient.RestoreRejectedApplicationVersionToDraft(grpcCtx, &applicationreviewv1.RestoreRejectedApplicationVersionToDraftRequest{
		ApplicationId: application.GetId(), VersionId: created.GetVersionId(), ReviewId: reviewID,
		Command: &applicationreviewv1.RestoreRejectedApplicationVersionToDraftCommand{ExpectedVersionRevision: 4},
	})
	current := status.Convert(err)
	if current.Code() != codes.Aborted || e2eErrorReason(current) != transport.ReasonApplicationReviewAlreadyRestored {
		t.Fatalf("repeated gRPC restore = code:%v reason:%q error:%v", current.Code(), e2eErrorReason(current), err)
	}
}

// e2eIsolatedDatabase connects to the integration MongoDB and returns a unique
// database. migrate controls whether migrations are applied explicitly.
func e2eIsolatedDatabase(t *testing.T, ctx context.Context, migrate bool) (*drivermongo.Client, *drivermongo.Database) {
	t.Helper()
	uri := os.Getenv(mongoIntegrationURIEnv)
	if uri == "" {
		t.Skipf("set %s or run scripts/test-mongo-integration.sh", mongoIntegrationURIEnv)
	}
	client, err := mongoadapter.NewClient(uri)
	if err != nil {
		t.Fatalf("connect integration MongoDB: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.Disconnect(shutdownCtx); err != nil {
			t.Errorf("disconnect integration MongoDB: %v", err)
		}
	})
	if err := mongoadapter.VerifyTransactionTopology(ctx, client); err != nil {
		t.Fatalf("integration MongoDB: %v", err)
	}

	database, err := mongoadapter.NewDatabase(client, fmt.Sprintf("iwut_app_center_e2e_%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("select integration database: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := database.Drop(dropCtx); err != nil {
			t.Errorf("drop integration database %s: %v", database.Name(), err)
		}
	})

	if migrate {
		if err := mongoadapter.NewMigrator(database).Migrate(ctx); err != nil {
			t.Fatalf("explicit migration: %v", err)
		}
	}
	return client, database
}

// e2eIdentity generates an ephemeral RS256 key pair and writes only the public
// key to the test temporary directory. No private key or token is committed.
func e2eIdentity(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "identity-public.pem")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	return privateKey, path
}

// e2eSignIdentity produces a compact RS256 JWS matching trusted-identity-v1.
func e2eSignIdentity(t *testing.T, privateKey *rsa.PrivateKey, subject, status string) string {
	t.Helper()
	now := time.Now().UTC()
	claims := map[string]any{
		"iss":              e2eIssuer,
		"sub":              subject,
		"aud":              []string{"another-service", e2eAudience},
		"iat":              now.Unix(),
		"nbf":              now.Add(-30 * time.Second).Unix(),
		"exp":              now.Add(2 * time.Minute).Unix(),
		"jti":              "e2e-" + subject,
		"developer_status": status,
	}
	return e2eSignIdentityClaims(t, privateKey, claims)
}

func e2eSignReviewerIdentity(t *testing.T, privateKey *rsa.PrivateKey, subject string, permissions ...string) string {
	t.Helper()
	now := time.Now().UTC()
	return e2eSignIdentityClaims(t, privateKey, map[string]any{
		"iss":         e2eIssuer,
		"sub":         subject,
		"aud":         []string{"another-service", e2eAudience},
		"iat":         now.Unix(),
		"nbf":         now.Add(-30 * time.Second).Unix(),
		"exp":         now.Add(2 * time.Minute).Unix(),
		"jti":         "e2e-reviewer-" + subject,
		"permissions": permissions,
	})
}

func e2eSignIdentityClaims(t *testing.T, privateKey *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	headerJSON, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": e2eKeyID})
	if err != nil {
		t.Fatalf("marshal JOSE header: %v", err)
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(payloadJSON)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign identity: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func e2eEnvironment(values map[string]string) config.LookupEnv {
	return func(key string) (string, bool) {
		value, found := values[key]
		if !found {
			switch key {
			case config.ServiceIdentityIDEnv:
				return "iwut-app-center", true
			case config.ServiceIdentityKIDEnv:
				return "app-center-e2e-service", true
			case config.ServiceIdentityPrivateKeyEnv:
				return e2eServicePrivateKeyB64(), true
			}
		}
		return value, found
	}
}

// e2eReserveAddresses holds count listeners open simultaneously so the
// returned ports are distinct, then releases them for Kratos to bind.
func e2eReserveAddresses(t *testing.T, count int) []string {
	t.Helper()
	listeners := make([]net.Listener, 0, count)
	addresses := make([]string, 0, count)
	for index := 0; index < count; index++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("reserve listen address: %v", err)
		}
		listeners = append(listeners, listener)
		addresses = append(addresses, listener.Addr().String())
	}
	for _, listener := range listeners {
		if err := listener.Close(); err != nil {
			t.Fatalf("release reserved address: %v", err)
		}
	}
	return addresses
}

func e2eStartScopeCatalogServer(t *testing.T, address string) *e2eScopeCatalogServer {
	t.Helper()
	scope, _ := e2eStartAuthServer(t, address)
	return scope
}

func e2eStartAuthServer(t *testing.T, address string) (*e2eScopeCatalogServer, *e2eDeveloperStatusServer) {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listen for test Auth Scope Catalog: %v", err)
	}
	scopeService := &e2eScopeCatalogServer{}
	scopeService.revision.Store(11)
	developerService := &e2eDeveloperStatusServer{statuses: make(map[string]developerstatusv1.DeveloperStatus)}
	developerService.systemAuthID = "auth-e2e-system"
	server := grpc.NewServer()
	scopecatalogv1.RegisterScopeCatalogServer(server, scopeService)
	developerstatusv1.RegisterDeveloperStatusDirectoryServer(server, developerService)
	systemprincipalv1.RegisterSystemPrincipalDirectoryServer(server, developerService)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				t.Errorf("test Auth Scope Catalog server: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("test Auth Scope Catalog server did not stop")
		}
	})
	return scopeService, developerService
}

func e2eWaitForHTTPListener(t *testing.T, ctx context.Context, address string, runDone <-chan struct{}, runErr *error) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case <-runDone:
			t.Fatalf("app.Run() exited before HTTP listener was ready: %v", *runErr)
		default:
		}
		connection, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			t.Fatalf("HTTP listener %s not ready: %v", address, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func e2eDialGRPC(t *testing.T, ctx context.Context, address string, runDone <-chan struct{}, runErr *error) *grpc.ClientConn {
	t.Helper()
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("create gRPC client for %s: %v", address, err)
	}
	connection.Connect()
	deadline := time.Now().Add(30 * time.Second)
	for {
		state := connection.GetState()
		if state == connectivity.Ready {
			return connection
		}
		select {
		case <-runDone:
			_ = connection.Close()
			t.Fatalf("app.Run() exited before gRPC listener was ready: %v", *runErr)
		default:
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			_ = connection.Close()
			t.Fatalf("gRPC listener %s not ready; state = %v", address, state)
		}
		waitCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		connection.WaitForStateChange(waitCtx, state)
		cancel()
	}
}

func e2eHTTPCreate(t *testing.T, address, token, name string) (int, []byte) {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q}`, name)
	request, err := http.NewRequest(
		http.MethodPost,
		"http://"+address+transport.CreateApplicationInternalPath,
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("build HTTP request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(transport.IdentityHeader, token)
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", transport.CreateApplicationInternalPath, err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read HTTP response: %v", err)
	}
	return response.StatusCode, payload
}

func e2eHTTPCreateVersion(
	t *testing.T,
	address string,
	token string,
	applicationID string,
	versionLabel string,
	requiredCapabilities []string,
	requiredScopes []string,
	optionalScopes []string,
) (int, http.Header, []byte) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"versionLabel":              versionLabel,
		"launchUrl":                 "https://example.edu/apps/course-table/v1/",
		"rpcApiMinVersion":          3,
		"rpcApiMaxVersionExclusive": 5,
		"requiredCapabilities":      requiredCapabilities,
		"requiredScopes":            requiredScopes,
		"optionalScopes":            optionalScopes,
	})
	if err != nil {
		t.Fatalf("encode create-version request: %v", err)
	}
	path := strings.Replace(transport.CreateApplicationVersionInternalPath, "{application_id}", applicationID, 1)
	request, err := http.NewRequest(http.MethodPost, "http://"+address+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build create-version request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(transport.IdentityHeader, token)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read create-version response: %v", err)
	}
	return response.StatusCode, response.Header.Clone(), body
}

func e2eHTTPUpdateVersion(
	t *testing.T,
	address, token, applicationID, versionID, ifMatch, versionLabel, launchURL string,
	minVersion, maxVersion int32,
	requiredCapabilities, requiredScopes, optionalScopes []string,
) (int, http.Header, []byte) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"versionLabel":              versionLabel,
		"launchUrl":                 launchURL,
		"rpcApiMinVersion":          minVersion,
		"rpcApiMaxVersionExclusive": maxVersion,
		"requiredCapabilities":      requiredCapabilities,
		"requiredScopes":            requiredScopes,
		"optionalScopes":            optionalScopes,
	})
	if err != nil {
		t.Fatalf("encode update-version request: %v", err)
	}
	path := strings.Replace(transport.UpdateApplicationVersionInternalPath, "{application_id}", applicationID, 1)
	path = strings.Replace(path, "{version_id}", versionID, 1)
	request, err := http.NewRequest(http.MethodPut, "http://"+address+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build update-version request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(transport.IdentityHeader, token)
	request.Header.Set("If-Match", ifMatch)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read update-version response: %v", err)
	}
	return response.StatusCode, response.Header.Clone(), body
}

func e2eHTTPSubmitReview(
	t *testing.T,
	address, token, applicationID, versionID string,
	expectedRevision int64,
) (int, http.Header, []byte) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"expectedRevision": expectedRevision})
	if err != nil {
		t.Fatalf("encode submit-review request: %v", err)
	}
	path := strings.Replace(transport.SubmitApplicationVersionReviewInternalPath, "{application_id}", applicationID, 1)
	path = strings.Replace(path, "{version_id}", versionID, 1)
	request, err := http.NewRequest(http.MethodPost, "http://"+address+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build submit-review request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(transport.IdentityHeader, token)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read submit-review response: %v", err)
	}
	return response.StatusCode, response.Header.Clone(), body
}

func e2eHTTPDecideReview(
	t *testing.T,
	address, token, applicationID, versionID, reviewID string,
	outcome, expectedPolicyVersion string,
	confirmedCheckIDs []string,
	reason string,
) (int, []byte) {
	t.Helper()
	command := map[string]any{
		"outcome":               outcome,
		"expectedPolicyVersion": expectedPolicyVersion,
		"confirmedCheckIds":     confirmedCheckIDs,
	}
	if reason != "" {
		command["reason"] = reason
	}
	payload, err := json.Marshal(command)
	if err != nil {
		t.Fatalf("encode decide-review request: %v", err)
	}
	path := strings.Replace(transport.DecideApplicationVersionReviewInternalPath, "{application_id}", applicationID, 1)
	path = strings.Replace(path, "{version_id}", versionID, 1)
	path = strings.Replace(path, "{review_id}", reviewID, 1)
	request, err := http.NewRequest(http.MethodPost, "http://"+address+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build decide-review request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(transport.IdentityHeader, token)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read decide-review response: %v", err)
	}
	return response.StatusCode, body
}

func e2eHTTPRestoreReview(
	t *testing.T,
	address, token, applicationID, versionID, reviewID string,
	expectedVersionRevision int64,
) (int, []byte) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"expectedVersionRevision": expectedVersionRevision})
	if err != nil {
		t.Fatalf("encode restore-review request: %v", err)
	}
	path := strings.Replace(transport.RestoreApplicationVersionInternalPath, "{application_id}", applicationID, 1)
	path = strings.Replace(path, "{version_id}", versionID, 1)
	path = strings.Replace(path, "{review_id}", reviewID, 1)
	request, err := http.NewRequest(http.MethodPost, "http://"+address+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build restore-review request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(transport.IdentityHeader, token)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read restore-review response: %v", err)
	}
	return response.StatusCode, body
}

func e2eErrorReason(current *status.Status) string {
	for _, detail := range current.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok {
			return info.GetReason()
		}
	}
	return ""
}

func e2eAssertReviewPersisted(
	t *testing.T,
	database *drivermongo.Database,
	review *applicationreviewv1.ApplicationReviewResource,
) {
	t.Helper()
	var document e2eApplicationReviewDocument
	if err := database.Collection("application_reviews").FindOne(
		t.Context(), bson.D{{Key: "reviewId", Value: review.GetReviewId()}},
	).Decode(&document); err != nil {
		t.Fatalf("read persisted review %s: %v", review.GetReviewId(), err)
	}
	if document.ReviewID != review.GetReviewId() || document.ApplicationID != review.GetApplicationId() ||
		document.VersionID != review.GetVersionId() || document.Attempt != review.GetAttempt() ||
		document.SourceVersionRevision != review.GetSourceVersionRevision() || document.Status != "PENDING" ||
		document.ScopeCatalogRevision != review.GetScopeCatalogRevision() ||
		document.PreflightPolicyVersion != review.GetPreflightPolicyVersion() || document.SubmittedBy != review.GetSubmittedBy() {
		t.Fatalf("persisted review = %#v; response = %v", document, review)
	}
}

func e2eAssertReviewFailureUnchanged(
	t *testing.T,
	database *drivermongo.Database,
	before e2eApplicationVersionDocument,
	wantReviewCount int,
) {
	t.Helper()
	after := e2eFindApplicationVersion(t, database, before.VersionID)
	if after.ReviewStatus != before.ReviewStatus || after.Revision != before.Revision ||
		after.UpdatedBy != before.UpdatedBy || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("failed review submission mutated version: before=%#v after=%#v", before, after)
	}
	e2eAssertCollectionCount(t, database, "application_reviews", wantReviewCount)
}

func e2eAssertVersionUnchanged(
	t *testing.T,
	database *drivermongo.Database,
	versionID, versionLabel string,
	revision int64,
	updatedAt time.Time,
) {
	t.Helper()
	document := e2eFindApplicationVersion(t, database, versionID)
	if document.VersionLabel != versionLabel || document.Revision != revision || !document.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("version changed after failed update: %#v", document)
	}
}

func e2eAssertApplicationVersionResponse(
	t *testing.T,
	response *applicationversionv1.CreateApplicationVersionResponse,
	applicationID string,
	adminID string,
) {
	t.Helper()
	if !e2eIsUUIDv7(response.GetVersionId()) || response.GetApplicationId() != applicationID || response.GetSequence() != 1 {
		t.Fatalf("version identity = %v", response)
	}
	if response.GetVersionLabel() != "v1.0.0" || response.GetLaunchUrl() != "https://example.edu/apps/course-table/v1/" ||
		response.GetRpcApiMinVersion() != 3 || response.GetRpcApiMaxVersionExclusive() != 5 {
		t.Fatalf("version content = %v", response)
	}
	if strings.Join(response.GetRequiredCapabilities(), ",") != "camera.read.v1,user.profile.v1" ||
		strings.Join(response.GetRequiredScopes(), ",") != "calendar.read,schedule.read" ||
		strings.Join(response.GetOptionalScopes(), ",") != "profile.basic" {
		t.Fatalf("sorted version lists = %v", response)
	}
	if response.GetReviewStatus() != "DRAFT" || response.GetRevision() != 1 ||
		response.GetCreatedBy() != adminID || response.GetUpdatedBy() != adminID ||
		!response.GetCreatedAt().AsTime().Equal(response.GetUpdatedAt().AsTime()) {
		t.Fatalf("version lifecycle/audit = %v", response)
	}
}

func e2eFindApplicationVersion(t *testing.T, database *drivermongo.Database, versionID string) e2eApplicationVersionDocument {
	t.Helper()
	var document e2eApplicationVersionDocument
	if err := database.Collection("application_versions").FindOne(
		t.Context(), bson.D{{Key: "versionId", Value: versionID}},
	).Decode(&document); err != nil {
		t.Fatalf("read persisted application version %s: %v", versionID, err)
	}
	return document
}

func e2eAssertPersistedApplicationVersion(
	t *testing.T,
	document e2eApplicationVersionDocument,
	response *applicationversionv1.CreateApplicationVersionResponse,
) {
	t.Helper()
	if document.VersionID != response.GetVersionId() || document.ApplicationID != response.GetApplicationId() ||
		document.Sequence != response.GetSequence() || document.VersionLabel != response.GetVersionLabel() ||
		document.ReviewStatus != "DRAFT" || document.Revision != 1 {
		t.Fatalf("persisted version identity/state = %#v; response = %v", document, response)
	}
	if strings.Join(document.RequiredCapabilities, ",") != strings.Join(response.GetRequiredCapabilities(), ",") ||
		strings.Join(document.RequiredScopes, ",") != strings.Join(response.GetRequiredScopes(), ",") ||
		strings.Join(document.OptionalScopes, ",") != strings.Join(response.GetOptionalScopes(), ",") {
		t.Fatalf("persisted version lists = %#v; response = %v", document, response)
	}
	if document.CreatedBy != response.GetCreatedBy() || document.UpdatedBy != response.GetUpdatedBy() ||
		!document.CreatedAt.Equal(response.GetCreatedAt().AsTime().Truncate(time.Millisecond)) ||
		!document.UpdatedAt.Equal(response.GetUpdatedAt().AsTime().Truncate(time.Millisecond)) {
		t.Fatalf("persisted version audit = %#v; response = %v", document, response)
	}
}

func e2eAssertNextVersionSequence(t *testing.T, database *drivermongo.Database, applicationID string, want int32) {
	t.Helper()
	var document struct {
		NextVersionSequence int32 `bson:"nextVersionSequence"`
	}
	if err := database.Collection("applications").FindOne(
		t.Context(), bson.D{{Key: "id", Value: applicationID}},
	).Decode(&document); err != nil {
		t.Fatalf("read nextVersionSequence for %s: %v", applicationID, err)
	}
	if document.NextVersionSequence != want {
		t.Fatalf("nextVersionSequence = %d, want %d", document.NextVersionSequence, want)
	}
}

func e2eAssertResponse(t *testing.T, label string, response *applicationv1.CreateApplicationResponse, wantName, wantAdminID string) {
	t.Helper()
	if response.GetName() != wantName {
		t.Fatalf("%s response name = %q, want %q", label, response.GetName(), wantName)
	}
	if response.GetAdminId() != wantAdminID {
		t.Fatalf("%s response adminId = %q, want %q", label, response.GetAdminId(), wantAdminID)
	}
	if !e2eIsUUIDv7(response.GetId()) {
		t.Fatalf("%s response id = %q, want a UUIDv7", label, response.GetId())
	}
	createdAt := response.GetCreatedAt().AsTime()
	if createdAt.IsZero() {
		t.Fatalf("%s response createdAt is zero", label)
	}
	if delta := time.Since(createdAt); delta < 0 || delta > 5*time.Minute {
		t.Fatalf("%s response createdAt = %v, not within the last 5 minutes", label, createdAt)
	}
}

func e2eIsUUIDv7(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		switch index {
		case 8, 13, 18, 23:
			if character != '-' {
				return false
			}
		case 14:
			if character != '7' {
				return false
			}
		case 19:
			if !strings.ContainsRune("89aAbB", character) {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
				return false
			}
		}
	}
	return true
}

func e2eFindApplication(t *testing.T, database *drivermongo.Database, id string) e2eApplicationDocument {
	t.Helper()
	var document e2eApplicationDocument
	err := database.Collection("applications").
		FindOne(t.Context(), bson.D{{Key: "id", Value: id}}).
		Decode(&document)
	if err != nil {
		t.Fatalf("read persisted application %s: %v", id, err)
	}
	return document
}

func e2eAssertPersisted(t *testing.T, document e2eApplicationDocument, wantName, wantAdminID string) {
	t.Helper()
	if document.Name != wantName {
		t.Fatalf("persisted name = %q, want %q", document.Name, wantName)
	}
	if document.AdminID != wantAdminID {
		t.Fatalf("persisted adminId = %q, want %q", document.AdminID, wantAdminID)
	}
	if document.NameKey != strings.ToLower(wantName) {
		t.Fatalf("persisted nameKey = %q, want %q", document.NameKey, strings.ToLower(wantName))
	}
	if document.CreatedAt.IsZero() {
		t.Fatal("persisted createdAt is zero")
	}
}

func e2eAssertCollectionCount(t *testing.T, database *drivermongo.Database, collectionName string, want int) {
	t.Helper()
	count, err := database.Collection(collectionName).CountDocuments(t.Context(), bson.D{})
	if err != nil {
		t.Fatalf("count %s: %v", collectionName, err)
	}
	if count != int64(want) {
		t.Fatalf("%s count = %d, want %d", collectionName, count, want)
	}
}

func e2eAssertQuota(t *testing.T, database *drivermongo.Database, adminID string, wantLimit, wantUsed int32) {
	t.Helper()
	var quota e2eQuotaDocument
	err := database.Collection("application_creation_quotas").
		FindOne(t.Context(), bson.D{{Key: "adminId", Value: adminID}}).
		Decode(&quota)
	if err != nil {
		t.Fatalf("read quota for %s: %v", adminID, err)
	}
	if quota.Limit != wantLimit || quota.UsedCount != wantUsed {
		t.Fatalf("quota for %s = (limit=%d, used=%d), want (%d, %d)", adminID, quota.Limit, quota.UsedCount, wantLimit, wantUsed)
	}
}

// e2eAssertNoSecret fails if any raw document in the collection contains a JWS
// or private-key fragment.
func e2eAssertNoSecret(t *testing.T, database *drivermongo.Database, collectionName string, secrets ...string) {
	t.Helper()
	cursor, err := database.Collection(collectionName).Find(t.Context(), bson.D{})
	if err != nil {
		t.Fatalf("scan %s for secrets: %v", collectionName, err)
	}
	defer func() { _ = cursor.Close(context.Background()) }()
	for cursor.Next(t.Context()) {
		raw := cursor.Current
		for _, secret := range secrets {
			if secret != "" && bytes.Contains(raw, []byte(secret)) {
				t.Fatalf("collection %s persisted secret material", collectionName)
			}
		}
	}
	if err := cursor.Err(); err != nil {
		t.Fatalf("scan %s for secrets: %v", collectionName, err)
	}
}
