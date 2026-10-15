package transport

import (
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	applicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
)

func messageFieldNames(t *testing.T, message proto.Message) []string {
	t.Helper()
	fields := message.ProtoReflect().Descriptor().Fields()
	names := make([]string, 0, fields.Len())
	for index := 0; index < fields.Len(); index++ {
		names = append(names, string(fields.Get(index).Name()))
	}
	sort.Strings(names)
	return names
}

func containsField(message proto.Message, name string) bool {
	return message.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name)) != nil
}

func TestAPIContract_MessageFieldsExcludeIdentityAndServerState(t *testing.T) {
	t.Parallel()

	request := &applicationv1.CreateApplicationRequest{}
	for _, field := range []string{"id", "admin_id", "created_at", "auth_id", "developer_status", "name_key", "next_version_sequence", "next_profile_revision_sequence"} {
		if containsField(request, field) {
			t.Fatalf("CreateApplicationRequest must not contain %q", field)
		}
	}
	if fields := messageFieldNames(t, request); strings.Join(fields, ",") != "name" {
		t.Fatalf("CreateApplicationRequest fields = %v, want [name]", fields)
	}

	response := &applicationv1.CreateApplicationResponse{}
	want := []string{"admin_id", "created_at", "id", "name"}
	if fields := messageFieldNames(t, response); strings.Join(fields, ",") != strings.Join(want, ",") {
		t.Fatalf("CreateApplicationResponse fields = %v, want %v", fields, want)
	}
	for _, field := range []string{"name_key", "next_version_sequence", "next_profile_revision_sequence", "developer_status", "auth_id"} {
		if containsField(response, field) {
			t.Fatalf("CreateApplicationResponse must not contain %q", field)
		}
	}
}
