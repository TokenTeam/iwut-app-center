package transport

import (
	"sort"

	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func registerObservableOperations(register func(string, []string), httpServer *khttp.Server, grpcServer *kgrpc.Server) {
	if register == nil {
		return
	}
	httpRoutes := map[khttp.RouteInfo]struct{}{}
	httpOperations := map[string]struct{}{}
	_ = httpServer.WalkRoute(func(route khttp.RouteInfo) error {
		httpRoutes[route] = struct{}{}
		// Preserve fixed path operations for manually registered handlers.
		httpOperations[route.Path] = struct{}{}
		return nil
	})
	grpcOperations := map[string]struct{}{}
	for service, info := range grpcServer.GetServiceInfo() {
		descriptor, _ := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(service))
		serviceDescriptor, _ := descriptor.(protoreflect.ServiceDescriptor)
		for _, method := range info.Methods {
			operation := "/" + service + "/" + method.Name
			grpcOperations[operation] = struct{}{}
			if serviceDescriptor == nil {
				continue
			}
			methodDescriptor := serviceDescriptor.Methods().ByName(protoreflect.Name(method.Name))
			if methodDescriptor == nil || !proto.HasExtension(methodDescriptor.Options(), annotations.E_Http) {
				continue
			}
			rule, _ := proto.GetExtension(methodDescriptor.Options(), annotations.E_Http).(*annotations.HttpRule)
			// Generated HTTP handlers use the RPC operation. Only admit a
			// method whose annotated HTTP binding is actually registered.
			if hasRegisteredHTTPBinding(rule, httpRoutes) {
				httpOperations[operation] = struct{}{}
			}
		}
	}
	register("http", sortedOperationKeys(httpOperations))
	register("grpc", sortedOperationKeys(grpcOperations))
}

func hasRegisteredHTTPBinding(rule *annotations.HttpRule, routes map[khttp.RouteInfo]struct{}) bool {
	if rule == nil {
		return false
	}
	var route khttp.RouteInfo
	switch pattern := rule.Pattern.(type) {
	case *annotations.HttpRule_Get:
		route = khttp.RouteInfo{Method: "GET", Path: pattern.Get}
	case *annotations.HttpRule_Post:
		route = khttp.RouteInfo{Method: "POST", Path: pattern.Post}
	case *annotations.HttpRule_Put:
		route = khttp.RouteInfo{Method: "PUT", Path: pattern.Put}
	case *annotations.HttpRule_Patch:
		route = khttp.RouteInfo{Method: "PATCH", Path: pattern.Patch}
	case *annotations.HttpRule_Delete:
		route = khttp.RouteInfo{Method: "DELETE", Path: pattern.Delete}
	case *annotations.HttpRule_Custom:
		route = khttp.RouteInfo{Method: pattern.Custom.GetKind(), Path: pattern.Custom.GetPath()}
	}
	if route.Method != "" && route.Path != "" {
		if _, registered := routes[route]; registered {
			return true
		}
	}
	for _, binding := range rule.AdditionalBindings {
		if hasRegisteredHTTPBinding(binding, routes) {
			return true
		}
	}
	return false
}

func sortedOperationKeys(operations map[string]struct{}) []string {
	keys := make([]string, 0, len(operations))
	for operation := range operations {
		keys = append(keys, operation)
	}
	sort.Strings(keys)
	return keys
}
