package transport

import (
	"errors"
	"fmt"
	"net/http"

	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	khttp "github.com/go-kratos/kratos/v2/transport/http"

	applicationv1 "iwut-app-center/api/gen/go/app_center/v1/application"
	applicationreviewv1 "iwut-app-center/api/gen/go/app_center/v1/application_review"
	applicationversionv1 "iwut-app-center/api/gen/go/app_center/v1/application_version"
)

const (
	// ServicePrefix is the Gateway-only service prefix. It is not part of the
	// Proto HTTP annotation.
	ServicePrefix = "/app-center"
	// CreateApplicationInternalPath is the HTTP path declared by the Proto
	// google.api.http annotation.
	CreateApplicationInternalPath = "/v1/applications"
	// CreateApplicationExternalPath is the client-visible path after the Gateway
	// strips ServicePrefix.
	CreateApplicationExternalPath = ServicePrefix + CreateApplicationInternalPath
	// CreateApplicationGRPCMethod is the generated full method name.
	CreateApplicationGRPCMethod = applicationv1.OperationApplicationCreateApplication
	// CreateApplicationVersionInternalPath is declared by the UC-APP-002 Proto.
	CreateApplicationVersionInternalPath       = "/v1/applications/{application_id}/versions"
	CreateApplicationVersionExternalPath       = ServicePrefix + CreateApplicationVersionInternalPath
	CreateApplicationVersionGRPCMethod         = applicationversionv1.OperationApplicationVersionCreateApplicationVersion
	UpdateApplicationVersionInternalPath       = "/v1/applications/{application_id}/versions/{version_id}"
	UpdateApplicationVersionExternalPath       = ServicePrefix + UpdateApplicationVersionInternalPath
	UpdateApplicationVersionGRPCMethod         = applicationversionv1.OperationApplicationVersionUpdateApplicationVersion
	SubmitApplicationVersionReviewInternalPath = "/v1/applications/{application_id}/versions/{version_id}/reviews"
	SubmitApplicationVersionReviewExternalPath = ServicePrefix + SubmitApplicationVersionReviewInternalPath
	SubmitApplicationVersionReviewGRPCMethod   = applicationreviewv1.OperationApplicationReviewSubmitApplicationVersionReview
	RestoreApplicationVersionInternalPath      = "/v1/applications/{application_id}/versions/{version_id}/reviews/{review_id}/draft-restoration"
	RestoreApplicationVersionExternalPath      = ServicePrefix + RestoreApplicationVersionInternalPath
	RestoreApplicationVersionGRPCMethod        = applicationreviewv1.OperationApplicationReviewRestoreRejectedApplicationVersionToDraft
)

// ServerConfig carries the two listen addresses validated at startup.
type ServerConfig struct {
	HTTPAddr string
	GRPCAddr string
}

// Servers groups the two Kratos transports that share one ApplicationService.
type Servers struct {
	HTTP *khttp.Server
	GRPC *kgrpc.Server
}

func NewServers(
	config ServerConfig,
	verifier *IdentityVerifier,
	service *ApplicationService,
	versionService *ApplicationVersionService,
	reviewService *ApplicationReviewService,
) (*Servers, error) {
	if verifier == nil {
		return nil, errors.New("transport servers: identity verifier is required")
	}
	if service == nil {
		return nil, errors.New("transport servers: application service is required")
	}
	if versionService == nil {
		return nil, errors.New("transport servers: application version service is required")
	}
	if reviewService == nil {
		return nil, errors.New("transport servers: application review service is required")
	}

	httpServer := khttp.NewServer(
		khttp.Address(config.HTTPAddr),
		khttp.Middleware(identityMiddleware(verifier)),
		khttp.ResponseEncoder(createdResponseEncoder),
	)
	applicationv1.RegisterApplicationHTTPServer(httpServer, service)
	applicationversionv1.RegisterApplicationVersionHTTPServer(httpServer, versionService)
	applicationreviewv1.RegisterApplicationReviewHTTPServer(httpServer, reviewService)

	grpcServer := kgrpc.NewServer(
		kgrpc.Address(config.GRPCAddr),
		kgrpc.Middleware(identityMiddleware(verifier)),
	)
	applicationv1.RegisterApplicationServer(grpcServer, service)
	applicationversionv1.RegisterApplicationVersionServer(grpcServer, versionService)
	applicationreviewv1.RegisterApplicationReviewServer(grpcServer, reviewService)

	return &Servers{HTTP: httpServer, GRPC: grpcServer}, nil
}

// createdResponseEncoder turns the generated handler's default 200 into the
// contract's 201 Created without touching the shared gRPC service. Kratos
// stores the status in a response writer whose WriteHeader only records the
// code, so overriding it here is safe and transport-local.
func createdResponseEncoder(w http.ResponseWriter, r *http.Request, v any) error {
	switch response := v.(type) {
	case *applicationv1.CreateApplicationResponse:
		w.WriteHeader(http.StatusCreated)
	case *applicationversionv1.CreateApplicationVersionResponse:
		w.Header().Set("ETag", fmt.Sprintf("\"%d\"", response.GetRevision()))
		w.WriteHeader(http.StatusCreated)
	case *applicationversionv1.UpdateApplicationVersionResponse:
		w.Header().Set("ETag", fmt.Sprintf("\"%d\"", response.GetRevision()))
	case *applicationreviewv1.SubmitApplicationVersionReviewResponse:
		if review := response.GetReview(); review != nil {
			w.Header().Set("Location", fmt.Sprintf(
				"/v1/applications/%s/versions/%s/reviews/%s",
				review.GetApplicationId(), review.GetVersionId(), review.GetReviewId(),
			))
		}
		w.WriteHeader(http.StatusCreated)
	}
	return khttp.DefaultResponseEncoder(w, r, v)
}
