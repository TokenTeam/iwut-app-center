package transport

import (
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/account_owner_exit"
	"google.golang.org/protobuf/reflect/protoreflect"
	"testing"
)

func TestBR_APP_009_ProviderContract(t *testing.T) {
	methods := map[string]string{pb.AccountOwnerExitService_PrepareAccountOwnerExit_FullMethodName: "app.account-owner-exit.prepare", pb.AccountOwnerExitService_FinishAccountOwnerExit_FullMethodName: "app.account-owner-exit.finish", pb.AccountOwnerExitService_GetAccountOwnerExitStatus_FullMethodName: "app.account-owner-exit.read"}
	for method, want := range methods {
		got, ok := providerPermission(method)
		if !ok || got != want {
			t.Fatalf("%s: %s %v", method, got, ok)
		}
	}
	f := (&pb.FinishAccountOwnerExitRequest{}).ProtoReflect().Descriptor().Fields()
	for name, number := range map[protoreflect.Name]protoreflect.FieldNumber{"auth_id": 1, "operation_id": 2, "receipt_id": 3, "decision": 4, "purpose": 5} {
		if f.ByName(name) == nil || f.ByName(name).Number() != number {
			t.Fatal(name)
		}
	}
}
