package transport

import (
	"testing"

	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_archival"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestApplicationArchivalContract_UCAPP033_RoutesAndMinimalCommands(t *testing.T) {
	file := pb.File_app_center_v1_application_archival_application_archival_proto
	service := file.Services().ByName("ApplicationArchivalService")
	if service == nil || service.Methods().Len() != 3 {
		t.Fatalf("service=%v", service)
	}
	for _, method := range []protoreflect.Name{"GetApplicationArchiveState", "ArchiveApplication", "UnarchiveApplication"} {
		if service.Methods().ByName(method) == nil {
			t.Fatalf("missing method %s", method)
		}
	}
	command := (&pb.ApplicationArchiveCommand{}).ProtoReflect().Descriptor()
	if command.Fields().Len() != 1 || string(command.Fields().Get(0).Name()) != "expected_archive_revision" {
		t.Fatalf("command fields=%v", command.Fields().Len())
	}
	state := (&pb.ApplicationArchiveState{}).ProtoReflect().Descriptor()
	for _, forbidden := range []protoreflect.Name{"actor_auth_id", "admin_id", "last_archive_event_id"} {
		if state.Fields().ByName(forbidden) != nil {
			t.Fatalf("state leaks %s", forbidden)
		}
	}
}
