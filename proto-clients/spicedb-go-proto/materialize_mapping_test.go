package spicedbgoproto

import (
	"testing"

	materialize "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/materialize/v0"
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
)

func TestMaterializeUsesLocalV1Messages(t *testing.T) {
	request := &materialize.ExperimentalRoaringLookupResourcesRequest{
		Consistency: &v1.Consistency{Requirement: &v1.Consistency_FullyConsistent{FullyConsistent: true}},
		Subject:     &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "alice"}},
	}
	if !request.GetConsistency().GetFullyConsistent() || request.GetSubject().GetObject().GetObjectId() != "alice" {
		t.Fatal("materialize request lost local v1 values")
	}
}
