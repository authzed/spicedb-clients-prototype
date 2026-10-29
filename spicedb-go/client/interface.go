package client

import (
	"context"
	"iter"

	proto "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto"
)

// Interface is the method set of *Client. Depend on it (or a subset you
// define) where tests substitute clienttest.Store, which also implements it.
// TestInterfaceMatchesClient keeps it identical to *Client's exported methods.
type Interface interface {
	Check(ctx context.Context, request PermissionCheck, opts ...CheckOption) (CheckResult, error)
	CheckBulk(ctx context.Context, requests []PermissionCheck, opts ...CheckOption) ([]CheckResult, error)
	CheckIter(ctx context.Context, requests iter.Seq[PermissionCheck], opts ...CheckOption) iter.Seq2[CheckResult, error]
	Close() error
	ComputablePermissions(ctx context.Context, request ComputablePermissionsRequest, opts ...ComputablePermissionsOption) (ComputablePermissionsResult, error)
	CountRelationships(ctx context.Context, request CountRelationshipsRequest, opts ...CountRelationshipsOption) (CountRelationshipsResult, error)
	CountRelationshipsByFilter(ctx context.Context, request CountRelationshipsByFilterRequest, opts ...CountRelationshipsByFilterOption) (CountRelationshipsByFilterResult, error)
	DeleteAllRelationshipsNonAtomic(ctx context.Context, request DeleteAllRequest, opts ...DeleteAllOption) (DeleteResult, error)
	DeleteRelationships(ctx context.Context, request DeleteRequest, opts ...DeleteOption) (DeleteResult, error)
	DependentRelations(ctx context.Context, request DependentRelationsRequest, opts ...DependentRelationsOption) (DependentRelationsResult, error)
	DiffSchema(ctx context.Context, request DiffSchemaRequest, opts ...DiffSchemaOption) (DiffSchemaResult, error)
	DownloadPermissionSets(ctx context.Context, request DownloadPermissionSetsRequest, opts ...DownloadPermissionSetsOption) (DownloadPermissionSetsResult, error)
	ExpandPermissionTree(ctx context.Context, request ExpandPermissionTreeRequest, opts ...ExpandPermissionTreeOption) (ExpandPermissionTreeResult, error)
	ExportRelationships(ctx context.Context, request ExportRelationshipsRequest, opts ...ExportRelationshipsOption) iter.Seq2[ExportedRelationship, error]
	ImportRelationships(ctx context.Context, request ImportRelationshipsRequest, opts ...ImportRelationshipsOption) (ImportRelationshipsResult, error)
	LookupPermissionSets(ctx context.Context, request LookupPermissionSetsRequest, opts ...LookupPermissionSetsOption) iter.Seq2[LookupPermissionSet, error]
	LookupResources(ctx context.Context, request LookupResourcesRequest, opts ...LookupResourcesOption) iter.Seq2[LookupResource, error]
	LookupSubjects(ctx context.Context, request LookupSubjectsRequest, opts ...LookupSubjectsOption) iter.Seq2[LookupSubject, error]
	RawProto() *proto.Client
	ReadRelationships(ctx context.Context, request ReadRelationshipsRequest, opts ...ReadRelationshipsOption) iter.Seq2[ReadRelationship, error]
	ReadSchema(ctx context.Context, request ReadSchemaRequest, opts ...ReadSchemaOption) (ReadSchemaResult, error)
	ReflectSchema(ctx context.Context, request ReflectSchemaRequest, opts ...ReflectSchemaOption) (ReflectSchemaResult, error)
	RegisterRelationshipCounter(ctx context.Context, request RegisterRelationshipCounterRequest, opts ...RegisterRelationshipCounterOption) (RegisterRelationshipCounterResult, error)
	RoaringLookupResources(ctx context.Context, request RoaringLookupResourcesRequest, opts ...RoaringLookupResourcesOption) (RoaringLookupResourcesResult, error)
	UnregisterRelationshipCounter(ctx context.Context, request UnregisterRelationshipCounterRequest, opts ...UnregisterRelationshipCounterOption) (UnregisterRelationshipCounterResult, error)
	Updates(ctx context.Context, request UpdatesRequest, opts ...UpdatesOption) iter.Seq2[WatchEvent, error]
	WatchPermissionSets(ctx context.Context, request WatchPermissionSetsRequest, opts ...WatchPermissionSetsOption) iter.Seq2[PermissionSetEvent, error]
	Write(ctx context.Context, request WriteRequest, opts ...WriteOption) (WriteResult, error)
	WriteSchema(ctx context.Context, request WriteSchemaRequest, opts ...WriteSchemaOption) (WriteSchemaResult, error)
}

var _ Interface = (*Client)(nil)
