/**
 * Native <-> protocol conversions, for tools that print or persist
 * protocol-shaped data (JSON output, backup files, debug traces) and want the
 * exact mapping the client itself uses. Ordinary operations never need this
 * namespace; it is exported as `protoconv` from `@spicedb/client`.
 *
 * `*ToProto` functions validate native input exactly as the client does before
 * an RPC and throw `InvalidArgumentError` for unrepresentable values.
 * `*FromProto` functions return immutable native values.
 */
import { create, type JsonObject } from "@bufbuild/protobuf";
import { DurationSchema, timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
    type ObjectReference, ObjectReferenceSchema, type SubjectReference, SubjectReferenceSchema,
    type Relationship as ProtoRelationship, type RelationshipFilter as ProtoRelationshipFilter,
    type Precondition as ProtoPrecondition, Precondition_Operation,
    type RelationshipUpdate as ProtoRelationshipUpdate, RelationshipUpdate_Operation,
    type Consistency as ProtoConsistency, type ZedToken as ProtoZedToken, ZedTokenSchema,
    type CheckPermissionRequest, CheckPermissionRequestSchema,
    type CheckPermissionResponse, CheckPermissionResponseSchema,
    type DebugInformation as ProtoDebugInformation, DebugInformationSchema,
    type CheckDebugTrace as ProtoCheckDebugTrace, CheckDebugTraceSchema, CheckDebugTrace_SubProblemsSchema, CaveatEvalInfoSchema,
    PartialCaveatInfoSchema,
    type LookupResourcesResponse, LookupResourcesResponseSchema,
    type LookupSubjectsResponse, LookupSubjectsResponseSchema, ResolvedSubjectSchema, LookupPermissionship,
    type ExpandPermissionTreeResponse, ExpandPermissionTreeResponseSchema,
    type PermissionRelationshipTree, PermissionRelationshipTreeSchema, AlgebraicSubjectSetSchema, DirectSubjectSetSchema,
    type ReadRelationshipsResponse, ReadRelationshipsResponseSchema,
    type CheckBulkPermissionsRequestItem, CheckBulkPermissionsRequestItemSchema,
    type CheckBulkPermissionsResponse, CheckBulkPermissionsResponseSchema, CheckBulkPermissionsPairSchema, CheckBulkPermissionsResponseItemSchema,
    type WriteRelationshipsResponse, WriteRelationshipsResponseSchema,
    CursorSchema,
} from "@spicedb/proto";
import { Consistency } from "./consistency.js";
import { InvalidArgumentError, type SpiceDBError } from "./errors.js";
import type { Any } from "@bufbuild/protobuf/wkt";
import { SubjectRelationKind } from "./modes.js";
import { ZedToken, serverToken, tokenString } from "./token.js";
import {
    type ObjectRef, type SubjectRef, type Relationship, type RelationshipFilter, type SubjectRelationFilter,
    type Precondition, type RelationshipUpdate, type CheckRequest, CheckResult, type CheckDebugTrace, type DebugInformation,
    type LookupResource, type LookupSubject, type ResolvedSubject, type ExpandResult, type PermissionTree,
    type ReadRelationship, type WriteResult, type Permissionship,
    toProtoRelationship, fromProtoRelationship, toProtoRelationshipFilter, toProtoUpdate, toProtoPreconditionValue,
    protoSubject, nativeSubject, immutableSnapshot, mergeCheckContext, checkResultFromProto as checkResultFromProtoTypes, fromProtoLookupResource,
    fromProtoLookupSubject, fromProtoPermissionTree, fromProtoDebugInformation,
} from "./types.js";

export function objectToProto(object: ObjectRef): ObjectReference {
    if (!object?.objectType || typeof object.objectId !== "string")
        throw new InvalidArgumentError("object requires objectType and objectId");
    return create(ObjectReferenceSchema, { objectType: object.objectType, objectId: object.objectId });
}
export function objectFromProto(object: ObjectReference | undefined): ObjectRef {
    return immutableSnapshot({ objectType: object?.objectType ?? "", objectId: object?.objectId ?? "" });
}
export function subjectToProto(subject: SubjectRef): SubjectReference { return protoSubject(subject); }
export function subjectFromProto(subject: SubjectReference | undefined): SubjectRef { return nativeSubject(subject ?? { optionalRelation: "" }); }
export function relationshipToProto(rel: Relationship): ProtoRelationship { return toProtoRelationship(rel); }
export function relationshipFromProto(rel: ProtoRelationship): Relationship { return fromProtoRelationship(rel); }
export function filterToProto(filter: RelationshipFilter): ProtoRelationshipFilter { return toProtoRelationshipFilter(filter); }
export function filterFromProto(filter: ProtoRelationshipFilter): RelationshipFilter {
    const s = filter.optionalSubjectFilter;
    const relation: SubjectRelationFilter | undefined = s === undefined ? undefined
        : s.optionalRelation === undefined ? { kind: SubjectRelationKind.Any }
            : s.optionalRelation.relation === "" ? { kind: SubjectRelationKind.Direct }
                : { kind: SubjectRelationKind.Named, relation: s.optionalRelation.relation };
    return immutableSnapshot({
        resourceType: filter.resourceType,
        ...(filter.optionalResourceId ? { resourceId: filter.optionalResourceId } : {}),
        ...(filter.optionalResourceIdPrefix ? { resourceIdPrefix: filter.optionalResourceIdPrefix } : {}),
        ...(filter.optionalRelation ? { relation: filter.optionalRelation } : {}),
        ...(s === undefined ? {} : { subject: { subjectType: s.subjectType, ...(s.optionalSubjectId ? { subjectId: s.optionalSubjectId } : {}), ...(relation && relation.kind !== SubjectRelationKind.Any ? { relation } : {}) } }),
    });
}
export function preconditionToProto(precondition: Precondition): ProtoPrecondition { return toProtoPreconditionValue(precondition); }
export function preconditionFromProto(precondition: ProtoPrecondition): Precondition {
    if (precondition.operation !== Precondition_Operation.MUST_MATCH && precondition.operation !== Precondition_Operation.MUST_NOT_MATCH)
        throw new InvalidArgumentError(`unknown precondition operation ${precondition.operation}`);
    if (!precondition.filter)
        throw new InvalidArgumentError("precondition has no filter");
    return immutableSnapshot({ operation: precondition.operation === Precondition_Operation.MUST_MATCH ? "mustMatch" : "mustNotMatch", filter: filterFromProto(precondition.filter) });
}
export function updateToProto(update: RelationshipUpdate): ProtoRelationshipUpdate { return toProtoUpdate(update); }
export function updateFromProto(update: ProtoRelationshipUpdate): RelationshipUpdate {
    const operation = ({ [RelationshipUpdate_Operation.CREATE]: "create", [RelationshipUpdate_Operation.TOUCH]: "touch", [RelationshipUpdate_Operation.DELETE]: "delete" } as const)[update.operation as RelationshipUpdate_Operation.CREATE];
    if (operation === undefined)
        throw new InvalidArgumentError(`unknown update operation ${update.operation}`);
    if (!update.relationship)
        throw new InvalidArgumentError("update has no relationship");
    return immutableSnapshot({ operation, relationship: fromProtoRelationship(update.relationship) });
}
export function consistencyToProto(consistency: Consistency): ProtoConsistency {
    if (!(consistency instanceof Consistency))
        throw new InvalidArgumentError("consistency must be a Consistency");
    return consistency._toProto();
}
export function consistencyFromProto(consistency: ProtoConsistency): Consistency {
    const r = consistency.requirement;
    if (r.case === undefined)
        throw new InvalidArgumentError("consistency has no requirement");
    if ((r.case === "atLeastAsFresh" || r.case === "atExactSnapshot") && !r.value.token)
        throw new InvalidArgumentError("token consistency requires a nonempty token");
    return Consistency._wrap(consistency);
}
export function zedTokenToProto(token: ZedToken): ProtoZedToken { return create(ZedTokenSchema, { token: tokenString(token) }); }
export function zedTokenFromProto(token: ProtoZedToken | undefined): ZedToken { return serverToken(token?.token); }
/** The singular `CheckPermissionRequest` the client sends for `request` (with optional call-level context merged under the request's). */
export function checkRequestToProto(request: CheckRequest, defaultContext?: CheckRequest["context"]): CheckPermissionRequest {
    return create(CheckPermissionRequestSchema, { resource: objectToProto(request.resource), permission: request.permission, subject: protoSubject(request.subject), consistency: consistencyToProto(request.consistency), context: mergeCheckContext(defaultContext, request.context) });
}
export function checkResultFromProto(response: CheckPermissionResponse): CheckResult { return checkResultFromProtoTypes(response); }
/** The bulk request item `checkBulk` sends for `request` (with optional call-level context merged under the request's). */
export function checkBulkRequestItemToProto(request: CheckRequest, defaultContext?: CheckRequest["context"]): CheckBulkPermissionsRequestItem {
    return create(CheckBulkPermissionsRequestItemSchema, { resource: objectToProto(request.resource), permission: request.permission, subject: protoSubject(request.subject), context: mergeCheckContext(defaultContext, request.context) });
}
/** The `google.rpc.Status` an item error carries: the server's own status when it came from one, else its code and message. */
function itemErrorStatus(error: SpiceDBError) {
    const cause = error.cause as { details?: unknown } | undefined;
    const details = cause !== null && typeof cause === "object" && Array.isArray(cause.details) ? cause.details as Any[] : [];
    return { code: error.code, message: error.serverMessage ?? error.message, details };
}
/**
 * The server's `CheckBulkPermissionsResponse` for `requests` and the results
 * `checkBulk` returned for them (same order, same `defaultContext` as the
 * call's `context` option): one pair per item, carrying either the item's
 * result or, for a result with `error` set (`BulkItemErrors.Returned`), its
 * status. `checkedAt` is the last item's revision; `checkBulk` may span several
 * requests, each with its own revision.
 */
export function checkBulkToProto(requests: readonly CheckRequest[], results: readonly CheckResult[], defaultContext?: CheckRequest["context"]): CheckBulkPermissionsResponse {
    if (requests.length !== results.length)
        throw new InvalidArgumentError(`check bulk: ${requests.length} requests but ${results.length} results`);
    const response = create(CheckBulkPermissionsResponseSchema);
    requests.forEach((request, i) => {
        const result = results[i];
        const single = result.error === undefined ? checkResultToProto(result) : undefined;
        response.pairs.push(create(CheckBulkPermissionsPairSchema, {
            request: checkBulkRequestItemToProto(request, defaultContext),
            response: result.error !== undefined ? { case: "error", value: itemErrorStatus(result.error) }
                : { case: "item", value: create(CheckBulkPermissionsResponseItemSchema, { permissionship: single!.permissionship, partialCaveatInfo: single!.partialCaveatInfo, debugTrace: single!.debugTrace }) },
        }));
        response.checkedAt = zedTokenToProto(result.checkedAt);
    });
    return response;
}
const checkPermissionships: readonly Permissionship[] = ["unspecified", "noPermission", "hasPermission", "conditionalPermission"];
function checkPermissionshipToProto(p: Permissionship): number { return Math.max(0, checkPermissionships.indexOf(p)); }
/** The `CheckPermissionResponse` (debug trace included) that `result` came from. */
export function checkResultToProto(result: CheckResult): CheckPermissionResponse {
    return create(CheckPermissionResponseSchema, {
        checkedAt: zedTokenToProto(result.checkedAt), permissionship: checkPermissionshipToProto(result.permissionship),
        partialCaveatInfo: result.missingContext.length ? create(PartialCaveatInfoSchema, { missingRequiredContext: [...result.missingContext] }) : undefined,
        debugTrace: result.debugTrace === undefined ? undefined : debugInformationToProto(result.debugTrace),
        optionalExpiresAt: result.expiration === undefined ? undefined : timestampFromDate(result.expiration),
    });
}
export function debugInformationToProto(debug: DebugInformation): ProtoDebugInformation {
    return create(DebugInformationSchema, { schemaUsed: debug.schemaUsed, check: debug.check === undefined ? undefined : traceToProto(debug.check) });
}
export function debugInformationFromProto(debug: ProtoDebugInformation): DebugInformation { return fromProtoDebugInformation(debug)!; }
const caveatResults = ["unspecified", "unevaluated", "false", "true", "missingContext"] as const;
const permissionTypes = ["unspecified", "relation", "permission"] as const;
function traceToProto(trace: CheckDebugTrace): ProtoCheckDebugTrace {
    const caveat = trace.caveatEvaluation;
    return create(CheckDebugTraceSchema, {
        resource: trace.resource === undefined ? undefined : objectToProto(trace.resource),
        permission: trace.permission, permissionType: Math.max(0, permissionTypes.indexOf(trace.permissionType)),
        subject: trace.subject === undefined ? undefined : protoSubject(trace.subject),
        result: checkPermissionshipToProto(trace.result),
        caveatEvaluationInfo: caveat === undefined ? undefined : create(CaveatEvalInfoSchema, {
            expression: caveat.expression, result: Math.max(0, caveatResults.indexOf(caveat.result)),
            context: caveat.context as JsonObject | undefined, caveatName: caveat.caveatName,
            partialCaveatInfo: caveat.missingContext.length ? create(PartialCaveatInfoSchema, { missingRequiredContext: [...caveat.missingContext] }) : undefined,
        }),
        duration: trace.durationNs === undefined ? undefined : create(DurationSchema, { seconds: trace.durationNs / 1_000_000_000n, nanos: Number(trace.durationNs % 1_000_000_000n) }),
        resolution: trace.resolution.kind === "cached" ? { case: "wasCachedResult", value: trace.resolution.cached }
            : trace.resolution.kind === "subproblems" ? { case: "subProblems", value: create(CheckDebugTrace_SubProblemsSchema, { traces: trace.resolution.traces.map(traceToProto) }) }
                : { case: undefined },
        optionalExpiresAt: trace.expiration === undefined ? undefined : timestampFromDate(trace.expiration),
        traceOperationId: trace.traceOperationId, source: trace.source,
    });
}
const lookupPermissionships: Partial<Record<Permissionship, LookupPermissionship>> = { hasPermission: LookupPermissionship.HAS_PERMISSION, conditionalPermission: LookupPermissionship.CONDITIONAL_PERMISSION };
export function lookupResourceFromProto(response: LookupResourcesResponse): LookupResource { return immutableSnapshot(fromProtoLookupResource(response)); }
export function lookupResourceToProto(resource: LookupResource): LookupResourcesResponse {
    return create(LookupResourcesResponseSchema, {
        lookedUpAt: zedTokenToProto(resource.lookedUpAt), resourceObjectId: resource.resourceId,
        permissionship: lookupPermissionships[resource.permissionship] ?? LookupPermissionship.UNSPECIFIED,
        partialCaveatInfo: resource.partialCaveat === undefined ? undefined : create(PartialCaveatInfoSchema, { missingRequiredContext: [...resource.partialCaveat.missingRequiredContext] }),
        afterResultCursor: resource.afterResultCursor === undefined ? undefined : create(CursorSchema, { token: resource.afterResultCursor }),
    });
}
function resolvedSubjectToProto(subject: ResolvedSubject) {
    return create(ResolvedSubjectSchema, {
        subjectObjectId: subject.subjectId, permissionship: lookupPermissionships[subject.permissionship] ?? LookupPermissionship.UNSPECIFIED,
        partialCaveatInfo: subject.partialCaveat === undefined ? undefined : create(PartialCaveatInfoSchema, { missingRequiredContext: [...subject.partialCaveat.missingRequiredContext] }),
    });
}
export function lookupSubjectFromProto(response: LookupSubjectsResponse): LookupSubject { return immutableSnapshot(fromProtoLookupSubject(response)); }
/**
 * The server's `LookupSubjectsResponse` for `subject`, including the deprecated
 * top-level fields (subject ID, excluded subject IDs, permissionship and
 * partial caveat info) that SpiceDB still sends alongside `subject`.
 */
export function lookupSubjectToProto(subject: LookupSubject): LookupSubjectsResponse {
    const resolved = resolvedSubjectToProto(subject.subject);
    return create(LookupSubjectsResponseSchema, {
        lookedUpAt: zedTokenToProto(subject.lookedUpAt), subject: resolved, excludedSubjects: subject.excludedSubjects.map(resolvedSubjectToProto),
        subjectObjectId: resolved.subjectObjectId, permissionship: resolved.permissionship, partialCaveatInfo: resolved.partialCaveatInfo,
        excludedSubjectIds: subject.excludedSubjects.map(e => e.subjectId),
    });
}
const treeOperations = ["unspecified", "union", "intersection", "exclusion"] as const;
function treeToProto(tree: PermissionTree): PermissionRelationshipTree {
    const node = tree.node;
    return create(PermissionRelationshipTreeSchema, {
        expandedObject: objectToProto(tree.expandedObject), expandedRelation: tree.expandedRelation,
        treeType: node.kind === "intermediate" ? { case: "intermediate", value: create(AlgebraicSubjectSetSchema, { operation: Math.max(0, treeOperations.indexOf(node.operation)), children: node.children.map(treeToProto) }) }
            : node.kind === "leaf" ? { case: "leaf", value: create(DirectSubjectSetSchema, { subjects: node.subjects.map(protoSubject) }) }
                : { case: undefined },
    });
}
export function expandResultFromProto(response: ExpandPermissionTreeResponse): ExpandResult {
    return immutableSnapshot({ treeRoot: fromProtoPermissionTree(response.treeRoot), revision: serverToken(response.expandedAt?.token) });
}
export function expandResultToProto(result: ExpandResult): ExpandPermissionTreeResponse {
    return create(ExpandPermissionTreeResponseSchema, { expandedAt: zedTokenToProto(result.revision), treeRoot: treeToProto(result.treeRoot) });
}
export function readRelationshipFromProto(response: ReadRelationshipsResponse): ReadRelationship {
    if (!response.relationship)
        throw new InvalidArgumentError("read response has no relationship");
    return immutableSnapshot({ ...fromProtoRelationship(response.relationship), ...(response.afterResultCursor?.token ? { afterResultCursor: response.afterResultCursor.token } : {}), readAt: serverToken(response.readAt?.token) });
}
export function readRelationshipToProto(rel: ReadRelationship): ReadRelationshipsResponse {
    return create(ReadRelationshipsResponseSchema, { readAt: zedTokenToProto(rel.readAt), relationship: toProtoRelationship(rel), afterResultCursor: rel.afterResultCursor === undefined ? undefined : create(CursorSchema, { token: rel.afterResultCursor }) });
}
export function writeResultFromProto(response: WriteRelationshipsResponse): WriteResult { return immutableSnapshot({ revision: serverToken(response.writtenAt?.token) }); }
export function writeResultToProto(result: WriteResult): WriteRelationshipsResponse { return create(WriteRelationshipsResponseSchema, { writtenAt: zedTokenToProto(result.revision) }); }
