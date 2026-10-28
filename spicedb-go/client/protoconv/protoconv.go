// Package protoconv converts native spicedb-go values to and from the
// authzed.api.v1 protocol messages.
//
// It is a secondary API for tools that print or persist protocol-shaped data,
// such as stable JSON output (with protojson) or backup files. Ordinary
// operations never need it. Relationship-level values convert in both
// directions with the same code the client uses on the wire; results convert
// to their protocol response messages.
package protoconv

import (
	"fmt"
	"time"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/internal/conv"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// --- Relationship-level values, both directions ---

func ObjectToProto(o rel.ObjectRef) *v1.ObjectReference    { return conv.Object(o) }
func ObjectFromProto(p *v1.ObjectReference) rel.ObjectRef  { return conv.NativeObject(p) }
func SubjectToProto(s rel.SubjectRef) *v1.SubjectReference { return conv.Subject(s) }
func SubjectFromProto(p *v1.SubjectReference) rel.SubjectRef {
	return conv.NativeSubject(p)
}

// RelationshipToProto validates and converts a relationship, including its
// caveat context and expiration.
func RelationshipToProto(r rel.Relationship) (*v1.Relationship, error) { return conv.Relationship(r) }

// RelationshipFromProto converts and validates a relationship.
func RelationshipFromProto(p *v1.Relationship) (rel.Relationship, error) {
	return conv.NativeRelationship(p)
}

func FilterToProto(f rel.Filter) (*v1.RelationshipFilter, error)   { return conv.Filter(f) }
func FilterFromProto(p *v1.RelationshipFilter) (rel.Filter, error) { return conv.NativeFilter(p) }

func PreconditionToProto(p rel.Precondition) (*v1.Precondition, error) {
	ps, err := conv.Preconditions([]rel.Precondition{p})
	if err != nil {
		return nil, err
	}
	return ps[0], nil
}
func PreconditionFromProto(p *v1.Precondition) (rel.Precondition, error) {
	return conv.NativePrecondition(p)
}

func UpdateToProto(u rel.Update) (*v1.RelationshipUpdate, error)   { return conv.Update(u) }
func UpdateFromProto(p *v1.RelationshipUpdate) (rel.Update, error) { return conv.NativeUpdate(p) }

func ConsistencyToProto(s consistency.Strategy) (*v1.Consistency, error) { return conv.Strategy(s) }
func ConsistencyFromProto(p *v1.Consistency) (consistency.Strategy, error) {
	return conv.NativeStrategy(p)
}

// ZedTokenToProto converts a token; the zero token converts to nil.
func ZedTokenToProto(t consistency.ZedToken) *v1.ZedToken { return conv.ZedToken(t) }

// ZedTokenFromProto converts a token; nil converts to the zero token.
func ZedTokenFromProto(p *v1.ZedToken) consistency.ZedToken { return conv.NativeZedToken(p) }

// ContextToProto converts a native context map; nil converts to nil.
func ContextToProto(m map[string]any) (*structpb.Struct, error) { return conv.ContextStruct(m) }

// --- Results, to their protocol responses ---

func lookupPermissionship(p client.Permissionship) v1.LookupPermissionship {
	switch p {
	case client.PermissionshipHasPermission:
		return v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION
	case client.PermissionshipConditionalPermission:
		return v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION
	}
	return v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_UNSPECIFIED
}

func partialCaveat(missing []string) *v1.PartialCaveatInfo {
	if len(missing) == 0 {
		return nil
	}
	return &v1.PartialCaveatInfo{MissingRequiredContext: append([]string(nil), missing...)}
}

func timestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

// CheckResultToProto converts a check result, including its debug trace and
// expiration. A bulk item error (CheckResult.Err) is not representable in a
// CheckPermissionResponse and is omitted.
func CheckResultToProto(r client.CheckResult) (*v1.CheckPermissionResponse, error) {
	p := &v1.CheckPermissionResponse{
		CheckedAt:         conv.ZedToken(r.CheckedAt),
		PartialCaveatInfo: partialCaveat(r.MissingContext),
		OptionalExpiresAt: timestamp(r.Expiration),
	}
	switch r.Permissionship {
	case client.PermissionshipHasPermission:
		p.Permissionship = v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION
	case client.PermissionshipConditionalPermission:
		p.Permissionship = v1.CheckPermissionResponse_PERMISSIONSHIP_CONDITIONAL_PERMISSION
	case client.PermissionshipNoPermission:
		p.Permissionship = v1.CheckPermissionResponse_PERMISSIONSHIP_NO_PERMISSION
	}
	trace, err := DebugInformationToProto(r.DebugTrace)
	if err != nil {
		return nil, err
	}
	p.DebugTrace = trace
	return p, nil
}

// DebugInformationToProto converts a check debug trace; nil converts to nil.
func DebugInformationToProto(d *client.DebugInformation) (*v1.DebugInformation, error) {
	if d == nil {
		return nil, nil
	}
	p := &v1.DebugInformation{SchemaUsed: d.SchemaUsed}
	if d.Check != nil {
		check, err := checkTrace(*d.Check)
		if err != nil {
			return nil, err
		}
		p.Check = check
	}
	return p, nil
}

func checkTrace(t client.CheckDebugTrace) (*v1.CheckDebugTrace, error) {
	p := &v1.CheckDebugTrace{
		Resource:          conv.Object(t.Resource),
		Permission:        t.Permission,
		Subject:           conv.Subject(t.Subject),
		TraceOperationId:  t.TraceOperationID,
		Source:            t.Source,
		OptionalExpiresAt: timestamp(t.Expiration),
	}
	switch t.PermissionType {
	case client.TraceRelation:
		p.PermissionType = v1.CheckDebugTrace_PERMISSION_TYPE_RELATION
	case client.TracePermission:
		p.PermissionType = v1.CheckDebugTrace_PERMISSION_TYPE_PERMISSION
	}
	switch t.Result {
	case client.PermissionshipNoPermission:
		p.Result = v1.CheckDebugTrace_PERMISSIONSHIP_NO_PERMISSION
	case client.PermissionshipHasPermission:
		p.Result = v1.CheckDebugTrace_PERMISSIONSHIP_HAS_PERMISSION
	case client.PermissionshipConditionalPermission:
		p.Result = v1.CheckDebugTrace_PERMISSIONSHIP_CONDITIONAL_PERMISSION
	}
	if t.Duration != nil {
		p.Duration = durationpb.New(*t.Duration)
	}
	switch t.Resolution {
	case client.TraceCached:
		cached := t.WasCachedResult != nil && *t.WasCachedResult
		p.Resolution = &v1.CheckDebugTrace_WasCachedResult{WasCachedResult: cached}
	case client.TraceSubproblems:
		sub := &v1.CheckDebugTrace_SubProblems{}
		for _, child := range t.Subproblems {
			c, err := checkTrace(child)
			if err != nil {
				return nil, err
			}
			sub.Traces = append(sub.Traces, c)
		}
		p.Resolution = &v1.CheckDebugTrace_SubProblems_{SubProblems: sub}
	}
	if c := t.CaveatEvaluation; c != nil {
		ctx, err := conv.ContextStruct(c.Context)
		if err != nil {
			return nil, err
		}
		info := &v1.CaveatEvalInfo{Expression: c.Expression, Context: ctx, PartialCaveatInfo: partialCaveat(c.MissingContext), CaveatName: c.CaveatName}
		switch c.Result {
		case client.CaveatUnevaluated:
			info.Result = v1.CaveatEvalInfo_RESULT_UNEVALUATED
		case client.CaveatFalse:
			info.Result = v1.CaveatEvalInfo_RESULT_FALSE
		case client.CaveatTrue:
			info.Result = v1.CaveatEvalInfo_RESULT_TRUE
		case client.CaveatMissingContext:
			info.Result = v1.CaveatEvalInfo_RESULT_MISSING_SOME_CONTEXT
		}
		p.CaveatEvaluationInfo = info
	}
	return p, nil
}

func resolvedSubject(s client.ResolvedSubject) *v1.ResolvedSubject {
	var missing []string
	if s.PartialCaveat != nil {
		missing = s.PartialCaveat.MissingRequiredContext
	}
	return &v1.ResolvedSubject{SubjectObjectId: s.SubjectID, Permissionship: lookupPermissionship(s.Permissionship), PartialCaveatInfo: partialCaveat(missing)}
}

// LookupSubjectToProto converts a looked-up subject, including wildcard
// exclusions. It also fills the deprecated top-level fields (subject ID,
// excluded IDs, permissionship and partial caveat info) that SpiceDB still
// sends, so the message matches the server's wire response.
func LookupSubjectToProto(s client.LookupSubject) *v1.LookupSubjectsResponse {
	subject := resolvedSubject(s.Subject)
	p := &v1.LookupSubjectsResponse{
		LookedUpAt:        conv.ZedToken(s.LookedUpAt),
		Subject:           subject,
		SubjectObjectId:   subject.GetSubjectObjectId(),
		Permissionship:    subject.GetPermissionship(),
		PartialCaveatInfo: subject.GetPartialCaveatInfo(),
	}
	for _, e := range s.ExcludedSubjects {
		p.ExcludedSubjects = append(p.ExcludedSubjects, resolvedSubject(e))
		p.ExcludedSubjectIds = append(p.ExcludedSubjectIds, e.SubjectID)
	}
	return p
}

// LookupResourceToProto converts a looked-up resource with its cursor.
func LookupResourceToProto(r client.LookupResource) *v1.LookupResourcesResponse {
	var missing []string
	if r.PartialCaveat != nil {
		missing = r.PartialCaveat.MissingRequiredContext
	}
	return &v1.LookupResourcesResponse{
		LookedUpAt: conv.ZedToken(r.LookedUpAt), ResourceObjectId: r.ResourceID,
		Permissionship: lookupPermissionship(r.Permissionship), PartialCaveatInfo: partialCaveat(missing),
		AfterResultCursor: conv.Cursor(r.AfterResultCursor),
	}
}

// ReadRelationshipToProto converts a read relationship with its cursor and revision.
func ReadRelationshipToProto(r client.ReadRelationship) (*v1.ReadRelationshipsResponse, error) {
	p, err := conv.Relationship(r.Relationship)
	if err != nil {
		return nil, err
	}
	return &v1.ReadRelationshipsResponse{ReadAt: conv.ZedToken(r.ReadAt), Relationship: p, AfterResultCursor: conv.Cursor(r.AfterResultCursor)}, nil
}

// ExpandResultToProto converts an expanded permission tree.
func ExpandResultToProto(r client.ExpandPermissionTreeResult) *v1.ExpandPermissionTreeResponse {
	return &v1.ExpandPermissionTreeResponse{ExpandedAt: conv.ZedToken(r.Revision), TreeRoot: PermissionTreeToProto(r.Tree)}
}

// PermissionTreeToProto converts a permission tree; an Unknown tree converts to nil.
func PermissionTreeToProto(t client.PermissionTree) *v1.PermissionRelationshipTree {
	p := &v1.PermissionRelationshipTree{ExpandedObject: conv.Object(t.ExpandedObject()), ExpandedRelation: t.ExpandedRelation()}
	switch t.Kind() {
	case client.PermissionTreeIntermediate:
		set := &v1.AlgebraicSubjectSet{}
		switch t.Operation() {
		case client.TreeOperationUnion:
			set.Operation = v1.AlgebraicSubjectSet_OPERATION_UNION
		case client.TreeOperationIntersection:
			set.Operation = v1.AlgebraicSubjectSet_OPERATION_INTERSECTION
		case client.TreeOperationExclusion:
			set.Operation = v1.AlgebraicSubjectSet_OPERATION_EXCLUSION
		}
		for _, c := range t.Children() {
			set.Children = append(set.Children, PermissionTreeToProto(c))
		}
		p.TreeType = &v1.PermissionRelationshipTree_Intermediate{Intermediate: set}
	case client.PermissionTreeLeaf:
		leaf := &v1.DirectSubjectSet{}
		for _, s := range t.Subjects() {
			leaf.Subjects = append(leaf.Subjects, conv.Subject(s))
		}
		p.TreeType = &v1.PermissionRelationshipTree_Leaf{Leaf: leaf}
	default:
		return nil
	}
	return p
}

// CheckRequestToProto converts a native permission check to a bulk request item.
func CheckRequestToProto(c client.PermissionCheck) (*v1.CheckBulkPermissionsRequestItem, error) {
	ctx, err := conv.ContextStruct(c.Context)
	if err != nil {
		return nil, err
	}
	return &v1.CheckBulkPermissionsRequestItem{
		Resource: conv.Object(c.Resource), Permission: c.Permission, Subject: conv.Subject(c.Subject), Context: ctx,
	}, nil
}

// CheckBulkToProto converts a bulk check (its requests and the results
// CheckBulk returned for them, in the same order) to the server's response
// message: one pair per item, carrying either the item's result or, for a
// result with Err set (WithBulkItemErrors(ItemErrorsReturned)), its status.
// The response's checked-at is the last item's revision; CheckBulk may span
// several requests, each with its own revision.
func CheckBulkToProto(requests []client.PermissionCheck, results []client.CheckResult) (*v1.CheckBulkPermissionsResponse, error) {
	if len(requests) != len(results) {
		return nil, fmt.Errorf("check bulk: %d requests but %d results", len(requests), len(results))
	}
	p := &v1.CheckBulkPermissionsResponse{}
	for i, req := range requests {
		item, err := CheckRequestToProto(req)
		if err != nil {
			return nil, fmt.Errorf("check bulk item %d: %w", i, err)
		}
		pair := &v1.CheckBulkPermissionsPair{Request: item}
		r := results[i]
		if r.Err != nil {
			pair.Response = &v1.CheckBulkPermissionsPair_Error{Error: status.Convert(r.Err).Proto()}
		} else {
			single, err := CheckResultToProto(r)
			if err != nil {
				return nil, fmt.Errorf("check bulk item %d: %w", i, err)
			}
			pair.Response = &v1.CheckBulkPermissionsPair_Item{Item: &v1.CheckBulkPermissionsResponseItem{
				Permissionship: single.GetPermissionship(), PartialCaveatInfo: single.GetPartialCaveatInfo(), DebugTrace: single.GetDebugTrace(),
			}}
		}
		if t := conv.ZedToken(r.CheckedAt); t != nil {
			p.CheckedAt = t
		}
		p.Pairs = append(p.Pairs, pair)
	}
	return p, nil
}

// WriteResultToProto converts a relationship write result.
func WriteResultToProto(r client.WriteResult) *v1.WriteRelationshipsResponse {
	return &v1.WriteRelationshipsResponse{WrittenAt: conv.ZedToken(r.Revision)}
}

// WriteSchemaResultToProto converts a schema write result.
func WriteSchemaResultToProto(r client.WriteSchemaResult) *v1.WriteSchemaResponse {
	return &v1.WriteSchemaResponse{WrittenAt: conv.ZedToken(r.Revision)}
}

// ReadSchemaResultToProto converts a schema read result.
func ReadSchemaResultToProto(r client.ReadSchemaResult) *v1.ReadSchemaResponse {
	return &v1.ReadSchemaResponse{SchemaText: r.Schema, ReadAt: conv.ZedToken(r.Revision)}
}
