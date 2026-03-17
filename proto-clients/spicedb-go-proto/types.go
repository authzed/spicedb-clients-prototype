package spicedbgoproto

import (
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
)

// Core types re-exported from the generated proto package.
type (
	ObjectReference          = v1.ObjectReference
	SubjectReference         = v1.SubjectReference
	Relationship             = v1.Relationship
	RelationshipUpdate       = v1.RelationshipUpdate
	ContextualizedCaveat     = v1.ContextualizedCaveat
	ZedToken                 = v1.ZedToken
	Cursor                   = v1.Cursor
	PartialCaveatInfo        = v1.PartialCaveatInfo
	PermissionRelationshipTree = v1.PermissionRelationshipTree
	AlgebraicSubjectSet      = v1.AlgebraicSubjectSet
	DirectSubjectSet         = v1.DirectSubjectSet
)

// Permission service types.
type (
	Consistency                = v1.Consistency
	RelationshipFilter         = v1.RelationshipFilter
	SubjectFilter              = v1.SubjectFilter
	Precondition               = v1.Precondition
	CheckPermissionRequest     = v1.CheckPermissionRequest
	CheckPermissionResponse    = v1.CheckPermissionResponse
	CheckBulkPermissionsRequest     = v1.CheckBulkPermissionsRequest
	CheckBulkPermissionsResponse    = v1.CheckBulkPermissionsResponse
	ReadRelationshipsRequest   = v1.ReadRelationshipsRequest
	ReadRelationshipsResponse  = v1.ReadRelationshipsResponse
	WriteRelationshipsRequest  = v1.WriteRelationshipsRequest
	WriteRelationshipsResponse = v1.WriteRelationshipsResponse
	DeleteRelationshipsRequest  = v1.DeleteRelationshipsRequest
	DeleteRelationshipsResponse = v1.DeleteRelationshipsResponse
	LookupResourcesRequest     = v1.LookupResourcesRequest
	LookupResourcesResponse    = v1.LookupResourcesResponse
	LookupSubjectsRequest      = v1.LookupSubjectsRequest
	LookupSubjectsResponse     = v1.LookupSubjectsResponse
	ExpandPermissionTreeRequest  = v1.ExpandPermissionTreeRequest
	ExpandPermissionTreeResponse = v1.ExpandPermissionTreeResponse
	ImportBulkRelationshipsRequest  = v1.ImportBulkRelationshipsRequest
	ImportBulkRelationshipsResponse = v1.ImportBulkRelationshipsResponse
	ExportBulkRelationshipsRequest  = v1.ExportBulkRelationshipsRequest
	ExportBulkRelationshipsResponse = v1.ExportBulkRelationshipsResponse
)

// Schema service types.
type (
	ReadSchemaRequest    = v1.ReadSchemaRequest
	ReadSchemaResponse   = v1.ReadSchemaResponse
	WriteSchemaRequest   = v1.WriteSchemaRequest
	WriteSchemaResponse  = v1.WriteSchemaResponse
)

// Watch service types.
type (
	WatchRequest  = v1.WatchRequest
	WatchResponse = v1.WatchResponse
)

// Deprecated: Experimental service types. Many of these methods have been
// promoted to stable APIs in PermissionsService and SchemaService.
type (
	// Deprecated: Use CheckBulkPermissionsRequest instead.
	BulkCheckPermissionRequest = v1.BulkCheckPermissionRequest
	// Deprecated: Use CheckBulkPermissionsResponse instead.
	BulkCheckPermissionResponse = v1.BulkCheckPermissionResponse
	// Deprecated: Use ImportBulkRelationshipsRequest instead.
	BulkImportRelationshipsRequest = v1.BulkImportRelationshipsRequest
	// Deprecated: Use ImportBulkRelationshipsResponse instead.
	BulkImportRelationshipsResponse = v1.BulkImportRelationshipsResponse
	// Deprecated: Use ExportBulkRelationshipsRequest instead.
	BulkExportRelationshipsRequest = v1.BulkExportRelationshipsRequest
	// Deprecated: Use ExportBulkRelationshipsResponse instead.
	BulkExportRelationshipsResponse = v1.BulkExportRelationshipsResponse
)
