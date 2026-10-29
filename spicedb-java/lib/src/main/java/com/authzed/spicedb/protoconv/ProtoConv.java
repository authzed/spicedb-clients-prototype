package com.authzed.spicedb.protoconv;

import com.authzed.api.v1.AlgebraicSubjectSet;
import com.authzed.api.v1.CaveatEvalInfo;
import com.authzed.api.v1.CheckBulkPermissionsPair;
import com.authzed.api.v1.CheckBulkPermissionsRequestItem;
import com.authzed.api.v1.CheckBulkPermissionsResponse;
import com.authzed.api.v1.CheckBulkPermissionsResponseItem;
import com.authzed.api.v1.CheckDebugTrace;
import com.authzed.api.v1.CheckPermissionResponse;
import com.authzed.api.v1.ContextualizedCaveat;
import com.authzed.api.v1.Cursor;
import com.authzed.api.v1.DirectSubjectSet;
import com.authzed.api.v1.LookupPermissionship;
import com.authzed.api.v1.LookupResourcesResponse;
import com.authzed.api.v1.LookupSubjectsResponse;
import com.authzed.api.v1.ObjectReference;
import com.authzed.api.v1.PartialCaveatInfo;
import com.authzed.api.v1.PermissionRelationshipTree;
import com.authzed.api.v1.ReadRelationshipsResponse;
import com.authzed.api.v1.RelationshipFilter;
import com.authzed.api.v1.RelationshipUpdate;
import com.authzed.api.v1.ResolvedSubject;
import com.authzed.api.v1.SubjectFilter;
import com.authzed.api.v1.SubjectReference;
import com.authzed.api.v1.WriteRelationshipsRequest;
import com.authzed.api.v1.WriteRelationshipsResponse;
import com.authzed.spicedb.CheckResult;
import com.authzed.spicedb.Consistency;
import com.authzed.spicedb.DebugInformation;
import com.authzed.spicedb.ExportedRelationship;
import com.authzed.spicedb.Filter;
import com.authzed.spicedb.LookupResult;
import com.authzed.spicedb.ObjectRef;
import com.authzed.spicedb.PermissionCheck;
import com.authzed.spicedb.PermissionTree;
import com.authzed.spicedb.Permissionship;
import com.authzed.spicedb.ReadRelationship;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.SubjectRef;
import com.authzed.spicedb.SubjectRelationFilter;
import com.authzed.spicedb.Transaction;
import com.authzed.spicedb.WriteResult;
import com.authzed.spicedb.ZedToken;
import com.authzed.spicedb.errors.ErrorMapper;
import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.SpiceDBException;
import com.google.protobuf.ListValue;
import com.google.protobuf.NullValue;
import com.google.protobuf.Struct;
import com.google.protobuf.Timestamp;
import com.google.protobuf.Value;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Native ↔ protocol conversions (root DESIGN.md, "Native↔protocol conversions ... are exported in a
 * secondary namespace"). For tools that persist or print protocol-shaped data -- JSON output,
 * backup files, debug traces -- so they reuse the client's own mapping instead of re-implementing
 * it. The client itself uses these functions, so both always agree.
 *
 * <p>Ordinary application code never needs this class.
 */
public final class ProtoConv {
  private ProtoConv() {}

  /**
   * A protocol message this class cannot interpret: a required field (relationship, resource,
   * subject, object, ID) is missing. It fails with {@code INTERNAL}, which no retry policy retries
   * by default; conversions never skip such a message or fill its fields with empty values.
   */
  private static SpiceDBException malformed(String detail) {
    return ErrorMapper.newError(io.grpc.Status.Code.INTERNAL, "malformed response: " + detail);
  }

  // ---------------------------------------------------------------- tokens, cursors, timestamps

  public static com.authzed.api.v1.ZedToken toProto(ZedToken token) {
    return com.authzed.api.v1.ZedToken.newBuilder().setToken(token.value()).build();
  }

  public static ZedToken fromProto(com.authzed.api.v1.ZedToken token) {
    return new ZedToken(token.getToken());
  }

  /** The token, or {@code null} when the message field is absent. */
  public static ZedToken tokenOrNull(boolean present, com.authzed.api.v1.ZedToken token) {
    return present ? fromProto(token) : null;
  }

  public static Cursor cursorToProto(String cursor) {
    return Cursor.newBuilder().setToken(cursor).build();
  }

  public static Timestamp toProto(Instant instant) {
    return Timestamp.newBuilder()
        .setSeconds(instant.getEpochSecond())
        .setNanos(instant.getNano())
        .build();
  }

  public static Instant fromProto(Timestamp timestamp) {
    return Instant.ofEpochSecond(timestamp.getSeconds(), timestamp.getNanos());
  }

  // ---------------------------------------------------------------- consistency

  public static com.authzed.api.v1.Consistency toProto(Consistency consistency) {
    var b = com.authzed.api.v1.Consistency.newBuilder();
    return switch (consistency.kind()) {
      case FULL -> b.setFullyConsistent(true).build();
      case MIN_LATENCY -> b.setMinimizeLatency(true).build();
      case AT_LEAST -> b.setAtLeastAsFresh(toProto(consistency.token())).build();
      case SNAPSHOT -> b.setAtExactSnapshot(toProto(consistency.token())).build();
    };
  }

  /** Rejects an unset requirement: native consistency is always an explicit choice. */
  public static Consistency fromProto(com.authzed.api.v1.Consistency consistency) {
    return switch (consistency.getRequirementCase()) {
      case FULLY_CONSISTENT -> Consistency.full();
      case MINIMIZE_LATENCY -> Consistency.minLatency();
      case AT_LEAST_AS_FRESH -> Consistency.atLeast(fromProto(consistency.getAtLeastAsFresh()));
      case AT_EXACT_SNAPSHOT -> Consistency.snapshot(fromProto(consistency.getAtExactSnapshot()));
      default -> throw new InvalidArgumentException("consistency requirement is not set");
    };
  }

  // ---------------------------------------------------------------- objects and subjects

  public static ObjectReference toProto(ObjectRef object) {
    return ObjectReference.newBuilder()
        .setObjectType(object.objectType())
        .setObjectId(object.objectId())
        .build();
  }

  /** Fails with {@code INTERNAL} when the type or ID is missing. */
  public static ObjectRef fromProto(ObjectReference object) {
    if (object.getObjectType().isEmpty()) throw malformed("object has no type");
    if (object.getObjectId().isEmpty()) throw malformed("object has no ID");
    return new ObjectRef(object.getObjectType(), object.getObjectId());
  }

  public static SubjectReference toProto(SubjectRef subject) {
    return SubjectReference.newBuilder()
        .setObject(toProto(subject.object()))
        .setOptionalRelation(subject.relation() == null ? "" : subject.relation())
        .build();
  }

  /**
   * An empty relation or {@code ...} is a direct subject. Fails with {@code INTERNAL} when the
   * object is missing.
   */
  public static SubjectRef fromProto(SubjectReference subject) {
    if (!subject.hasObject()) throw malformed("subject has no object");
    var object = fromProto(subject.getObject());
    var relation = subject.getOptionalRelation();
    return relation.isEmpty() || relation.equals("...")
        ? SubjectRef.direct(object)
        : SubjectRef.named(object, relation);
  }

  // ---------------------------------------------------------------- context (caveat / check)

  public static Struct toStruct(Map<String, Object> context) {
    var builder = Struct.newBuilder();
    for (var entry : context.entrySet())
      builder.putFields(entry.getKey(), toValueForKey(entry.getKey(), entry.getValue()));
    return builder.build();
  }

  public static Map<String, Object> fromStruct(Struct struct) {
    Map<String, Object> m = new LinkedHashMap<>();
    for (var entry : struct.getFieldsMap().entrySet())
      m.put(entry.getKey(), fromValue(entry.getValue()));
    return m;
  }

  public static Value toValue(Object value) {
    if (value == null) {
      return Value.newBuilder().setNullValue(NullValue.NULL_VALUE).build();
    } else if (value instanceof Boolean b) {
      return Value.newBuilder().setBoolValue(b).build();
    } else if (value instanceof Number n) {
      return Value.newBuilder().setNumberValue(n.doubleValue()).build();
    } else if (value instanceof String s) {
      return Value.newBuilder().setStringValue(s).build();
    } else if (value instanceof Map<?, ?> m) {
      var structBuilder = Struct.newBuilder();
      for (var entry : m.entrySet()) {
        structBuilder.putFields(
            String.valueOf(entry.getKey()),
            toValueForKey(String.valueOf(entry.getKey()), entry.getValue()));
      }
      return Value.newBuilder().setStructValue(structBuilder.build()).build();
    } else if (value instanceof List<?> l) {
      var listBuilder = ListValue.newBuilder();
      for (var item : l) listBuilder.addValues(toValue(item));
      return Value.newBuilder().setListValue(listBuilder.build()).build();
    }
    // A value this conversion cannot represent came from the caller, who can see this error and
    // fix their input -- stringifying it would silently produce a context SpiceDB never intended.
    throw new InvalidArgumentException(
        "unsupported caveat context value type: " + value.getClass().getName());
  }

  private static Value toValueForKey(String key, Object value) {
    try {
      return toValue(value);
    } catch (InvalidArgumentException e) {
      throw new InvalidArgumentException("caveat context key \"" + key + "\": " + e.getMessage());
    }
  }

  public static Object fromValue(Value value) {
    return switch (value.getKindCase()) {
      case NULL_VALUE -> null;
      case BOOL_VALUE -> value.getBoolValue();
      case NUMBER_VALUE -> value.getNumberValue();
      case STRING_VALUE -> value.getStringValue();
      case STRUCT_VALUE -> fromStruct(value.getStructValue());
      case LIST_VALUE -> {
        List<Object> l = new ArrayList<>();
        for (var item : value.getListValue().getValuesList()) l.add(fromValue(item));
        yield l;
      }
      default -> null;
    };
  }

  // ---------------------------------------------------------------- relationships

  public static com.authzed.api.v1.Relationship toProto(Relationship r) {
    var builder =
        com.authzed.api.v1.Relationship.newBuilder()
            .setResource(toProto(r.resource()))
            .setRelation(r.relation())
            .setSubject(toProto(r.subject()));
    if (r.caveatName() != null && !r.caveatName().isEmpty()) {
      var caveat = ContextualizedCaveat.newBuilder().setCaveatName(r.caveatName());
      if (r.caveatContext() != null) caveat.setContext(toStruct(r.caveatContext()));
      builder.setOptionalCaveat(caveat.build());
    }
    if (r.expiration() != null) builder.setOptionalExpiresAt(toProto(r.expiration()));
    return builder.build();
  }

  /** Fails with {@code INTERNAL} when the resource, relation or subject is missing. */
  public static Relationship fromProto(com.authzed.api.v1.Relationship pr) {
    if (!pr.hasResource()) throw malformed("relationship has no resource");
    if (pr.getRelation().isEmpty()) throw malformed("relationship has no relation");
    if (!pr.hasSubject()) throw malformed("relationship has no subject");
    String caveatName = null;
    Map<String, Object> caveatContext = null;
    if (pr.hasOptionalCaveat()) {
      caveatName = pr.getOptionalCaveat().getCaveatName();
      if (caveatName.isEmpty()) throw malformed("relationship caveat has no name");
      if (pr.getOptionalCaveat().hasContext())
        caveatContext = fromStruct(pr.getOptionalCaveat().getContext());
    }
    return new Relationship(
        fromProto(pr.getResource()),
        pr.getRelation(),
        fromProto(pr.getSubject()),
        caveatName,
        caveatContext,
        pr.hasOptionalExpiresAt() ? fromProto(pr.getOptionalExpiresAt()) : null);
  }

  // ---------------------------------------------------------------- filters and preconditions

  private static boolean set(String s) {
    return s != null && !s.isEmpty();
  }

  public static RelationshipFilter toProto(Filter f) {
    var builder = RelationshipFilter.newBuilder().setResourceType(f.resourceType());
    if (set(f.resourceID())) builder.setOptionalResourceId(f.resourceID());
    if (set(f.resourceIDPrefix())) builder.setOptionalResourceIdPrefix(f.resourceIDPrefix());
    if (set(f.relation())) builder.setOptionalRelation(f.relation());
    boolean hasSubjectID = set(f.subjectID());
    boolean hasSubjectRelation = !(f.subjectRelation() instanceof SubjectRelationFilter.Any);
    if (set(f.subjectType())) {
      var subjectBuilder = SubjectFilter.newBuilder().setSubjectType(f.subjectType());
      if (hasSubjectID) subjectBuilder.setOptionalSubjectId(f.subjectID());
      if (hasSubjectRelation) {
        subjectBuilder.setOptionalRelation(
            SubjectFilter.RelationFilter.newBuilder()
                .setRelation(
                    f.subjectRelation() instanceof SubjectRelationFilter.Named n ? n.name() : "")
                .build());
      }
      builder.setOptionalSubjectFilter(subjectBuilder.build());
    } else if (hasSubjectID || hasSubjectRelation) {
      String missing = hasSubjectID ? "subjectID" : "subjectRelation";
      throw new InvalidArgumentException(
          "Filter has "
              + missing
              + " set without subjectType. The wire format requires subjectType whenever a"
              + " subject constraint is present -- call withSubjectType(...) before with"
              + Character.toUpperCase(missing.charAt(0))
              + missing.substring(1)
              + "(...).");
    }
    return builder.build();
  }

  public static Filter fromProto(RelationshipFilter f) {
    if (f.getResourceType().isEmpty())
      throw new InvalidArgumentException("native filters require a resource type");
    var filter =
        Filter.of(f.getResourceType())
            .withResourceId(f.getOptionalResourceId())
            .withResourceIdPrefix(f.getOptionalResourceIdPrefix())
            .withRelation(f.getOptionalRelation());
    if (f.hasOptionalSubjectFilter()) {
      var s = f.getOptionalSubjectFilter();
      filter = filter.withSubjectType(s.getSubjectType()).withSubjectId(s.getOptionalSubjectId());
      if (s.hasOptionalRelation()) {
        var rel = s.getOptionalRelation().getRelation();
        filter =
            filter.withSubjectRelation(
                rel.isEmpty() || rel.equals("...")
                    ? SubjectRelationFilter.direct()
                    : SubjectRelationFilter.named(rel));
      }
    }
    return filter;
  }

  public static com.authzed.api.v1.Precondition toProto(Transaction.Precondition p) {
    var op =
        switch (p.operation()) {
          case MUST_NOT_MATCH -> com.authzed.api.v1.Precondition.Operation.OPERATION_MUST_NOT_MATCH;
          case MUST_MATCH -> com.authzed.api.v1.Precondition.Operation.OPERATION_MUST_MATCH;
        };
    return com.authzed.api.v1.Precondition.newBuilder()
        .setOperation(op)
        .setFilter(toProto(p.filter()))
        .build();
  }

  public static Transaction.Precondition fromProto(com.authzed.api.v1.Precondition p) {
    var op =
        switch (p.getOperation()) {
          case OPERATION_MUST_MATCH -> Transaction.PreconditionOperation.MUST_MATCH;
          case OPERATION_MUST_NOT_MATCH -> Transaction.PreconditionOperation.MUST_NOT_MATCH;
          default -> throw new InvalidArgumentException("precondition operation is not set");
        };
    return new Transaction.Precondition(op, fromProto(p.getFilter()));
  }

  // ---------------------------------------------------------------- updates and writes

  public static RelationshipUpdate toProto(Transaction.Mutation m) {
    var op =
        switch (m.operation()) {
          case CREATE -> RelationshipUpdate.Operation.OPERATION_CREATE;
          case TOUCH -> RelationshipUpdate.Operation.OPERATION_TOUCH;
          case DELETE -> RelationshipUpdate.Operation.OPERATION_DELETE;
        };
    return RelationshipUpdate.newBuilder()
        .setOperation(op)
        .setRelationship(toProto(m.relationship()))
        .build();
  }

  /**
   * A watch-style update. An unrecognized operation maps to {@code UNSPECIFIED}, never to a write:
   * server-supplied unknowns degrade safely.
   */
  public static SpiceDBClient.Update fromProto(RelationshipUpdate u) {
    var op =
        switch (u.getOperation()) {
          case OPERATION_CREATE -> SpiceDBClient.UpdateOperation.CREATE;
          case OPERATION_TOUCH -> SpiceDBClient.UpdateOperation.TOUCH;
          case OPERATION_DELETE -> SpiceDBClient.UpdateOperation.DELETE;
          default -> SpiceDBClient.UpdateOperation.UNSPECIFIED;
        };
    if (!u.hasRelationship()) throw malformed("relationship update has no relationship");
    return new SpiceDBClient.Update(op, fromProto(u.getRelationship()));
  }

  /** The request {@code write(txn, options)} sends, without options. */
  public static WriteRelationshipsRequest toProto(Transaction txn) {
    var b = WriteRelationshipsRequest.newBuilder();
    for (var m : txn.mutations()) b.addUpdates(toProto(m));
    for (var p : txn.preconditions()) b.addOptionalPreconditions(toProto(p));
    return b.build();
  }

  public static WriteRelationshipsResponse toProto(WriteResult result) {
    var b = WriteRelationshipsResponse.newBuilder();
    if (result.revision() != null) b.setWrittenAt(toProto(result.revision()));
    return b.build();
  }

  // ---------------------------------------------------------------- reads and lookups

  public static ReadRelationshipsResponse toProto(ReadRelationship item) {
    var b = ReadRelationshipsResponse.newBuilder().setRelationship(toProto(item.relationship()));
    if (item.readAt() != null) b.setReadAt(toProto(item.readAt()));
    if (item.afterResultCursor() != null)
      b.setAfterResultCursor(cursorToProto(item.afterResultCursor()));
    return b.build();
  }

  public static ReadRelationship fromProto(ReadRelationshipsResponse r) {
    if (!r.hasRelationship()) throw malformed("read response has no relationship");
    return new ReadRelationship(
        fromProto(r.getRelationship()),
        r.hasAfterResultCursor() ? r.getAfterResultCursor().getToken() : null,
        tokenOrNull(r.hasReadAt(), r.getReadAt()));
  }

  /** Export has no per-response revision; each item carries its batch's cursor. */
  public static List<ExportedRelationship> fromProto(
      com.authzed.api.v1.ExportBulkRelationshipsResponse r) {
    var cursor = r.hasAfterResultCursor() ? r.getAfterResultCursor().getToken() : null;
    return r.getRelationshipsList().stream()
        .map(p -> new ExportedRelationship(fromProto(p), cursor))
        .toList();
  }

  public static Permissionship fromProto(LookupPermissionship v) {
    return switch (v) {
      case LOOKUP_PERMISSIONSHIP_HAS_PERMISSION -> Permissionship.HAS_PERMISSION;
      case LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION -> Permissionship.CONDITIONAL_PERMISSION;
      default -> Permissionship.UNSPECIFIED;
    };
  }

  public static LookupPermissionship lookupPermissionshipToProto(Permissionship p) {
    return switch (p) {
      case HAS_PERMISSION -> LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION;
      case CONDITIONAL_PERMISSION ->
          LookupPermissionship.LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION;
      default -> LookupPermissionship.LOOKUP_PERMISSIONSHIP_UNSPECIFIED;
    };
  }

  private static LookupResult.PartialCaveatInfo partialFromProto(PartialCaveatInfo v) {
    return new LookupResult.PartialCaveatInfo(List.copyOf(v.getMissingRequiredContextList()));
  }

  private static PartialCaveatInfo partialToProto(LookupResult.PartialCaveatInfo v) {
    return PartialCaveatInfo.newBuilder()
        .addAllMissingRequiredContext(v.missingRequiredContext())
        .build();
  }

  public static LookupResult.LookupResource fromProto(LookupResourcesResponse resp) {
    if (resp.getResourceObjectId().isEmpty()) throw malformed("looked-up resource has no ID");
    return new LookupResult.LookupResource(
        resp.getResourceObjectId(),
        fromProto(resp.getPermissionship()),
        resp.hasPartialCaveatInfo() ? partialFromProto(resp.getPartialCaveatInfo()) : null,
        tokenOrNull(resp.hasLookedUpAt(), resp.getLookedUpAt()),
        resp.hasAfterResultCursor() ? resp.getAfterResultCursor().getToken() : null);
  }

  public static LookupResourcesResponse toProto(LookupResult.LookupResource r) {
    var b =
        LookupResourcesResponse.newBuilder()
            .setResourceObjectId(r.resourceId())
            .setPermissionship(lookupPermissionshipToProto(r.permissionship()));
    if (r.partialCaveat() != null) b.setPartialCaveatInfo(partialToProto(r.partialCaveat()));
    if (r.lookedUpAt() != null) b.setLookedUpAt(toProto(r.lookedUpAt()));
    if (r.afterResultCursor() != null) b.setAfterResultCursor(cursorToProto(r.afterResultCursor()));
    return b.build();
  }

  public static LookupResult.ResolvedSubject fromProto(ResolvedSubject v) {
    if (v.getSubjectObjectId().isEmpty()) throw malformed("resolved subject has no ID");
    return new LookupResult.ResolvedSubject(
        v.getSubjectObjectId(),
        fromProto(v.getPermissionship()),
        v.hasPartialCaveatInfo() ? partialFromProto(v.getPartialCaveatInfo()) : null);
  }

  public static ResolvedSubject toProto(LookupResult.ResolvedSubject s) {
    var b =
        ResolvedSubject.newBuilder()
            .setSubjectObjectId(s.subjectId())
            .setPermissionship(lookupPermissionshipToProto(s.permissionship()));
    if (s.partialCaveat() != null) b.setPartialCaveatInfo(partialToProto(s.partialCaveat()));
    return b.build();
  }

  /** Reads the current fields, falling back to the deprecated top-level ones older servers send. */
  @SuppressWarnings("deprecation")
  public static LookupResult.LookupSubject fromProto(LookupSubjectsResponse resp) {
    LookupResult.ResolvedSubject subject =
        resp.hasSubject() && !resp.getSubject().getSubjectObjectId().isEmpty()
            ? fromProto(resp.getSubject())
            : null;
    if (subject == null) {
      if (resp.getSubjectObjectId().isEmpty()) throw malformed("looked-up subject has no ID");
      subject =
          new LookupResult.ResolvedSubject(
              resp.getSubjectObjectId(),
              fromProto(resp.getPermissionship()),
              resp.hasPartialCaveatInfo() ? partialFromProto(resp.getPartialCaveatInfo()) : null);
    }
    List<LookupResult.ResolvedSubject> excluded;
    if (!resp.getExcludedSubjectsList().isEmpty()) {
      excluded = resp.getExcludedSubjectsList().stream().map(ProtoConv::fromProto).toList();
    } else if (!resp.getExcludedSubjectIdsList().isEmpty()) {
      excluded =
          resp.getExcludedSubjectIdsList().stream()
              .map(
                  id -> {
                    if (id.isEmpty()) throw malformed("excluded subject has no ID");
                    return new LookupResult.ResolvedSubject(id, Permissionship.UNSPECIFIED, null);
                  })
              .toList();
    } else {
      excluded = List.of();
    }
    return new LookupResult.LookupSubject(
        subject, excluded, tokenOrNull(resp.hasLookedUpAt(), resp.getLookedUpAt()));
  }

  /**
   * The server's wire shape for a looked-up subject: besides {@code subject} and {@code
   * excluded_subjects} it fills the deprecated top-level fields SpiceDB still sends (subject ID,
   * excluded IDs, permissionship and partial caveat info), so the message matches the server's
   * response.
   */
  @SuppressWarnings("deprecation")
  public static LookupSubjectsResponse toProto(LookupResult.LookupSubject s) {
    var subject = toProto(s.subject());
    var b =
        LookupSubjectsResponse.newBuilder()
            .setSubject(subject)
            .setSubjectObjectId(subject.getSubjectObjectId())
            .setPermissionship(subject.getPermissionship());
    if (subject.hasPartialCaveatInfo()) b.setPartialCaveatInfo(subject.getPartialCaveatInfo());
    for (var e : s.excludedSubjects()) {
      b.addExcludedSubjects(toProto(e));
      b.addExcludedSubjectIds(e.subjectId());
    }
    if (s.lookedUpAt() != null) b.setLookedUpAt(toProto(s.lookedUpAt()));
    return b.build();
  }

  // ---------------------------------------------------------------- checks and traces

  public static Permissionship fromProto(CheckPermissionResponse.Permissionship v) {
    return switch (v) {
      case PERMISSIONSHIP_HAS_PERMISSION -> Permissionship.HAS_PERMISSION;
      case PERMISSIONSHIP_CONDITIONAL_PERMISSION -> Permissionship.CONDITIONAL_PERMISSION;
      case PERMISSIONSHIP_NO_PERMISSION -> Permissionship.NO_PERMISSION;
      default -> Permissionship.UNSPECIFIED;
    };
  }

  public static CheckPermissionResponse.Permissionship checkPermissionshipToProto(
      Permissionship p) {
    return switch (p) {
      case HAS_PERMISSION -> CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION;
      case CONDITIONAL_PERMISSION ->
          CheckPermissionResponse.Permissionship.PERMISSIONSHIP_CONDITIONAL_PERMISSION;
      case NO_PERMISSION -> CheckPermissionResponse.Permissionship.PERMISSIONSHIP_NO_PERMISSION;
      default -> CheckPermissionResponse.Permissionship.PERMISSIONSHIP_UNSPECIFIED;
    };
  }

  /** The singular response shape for a (singular or bulk) result, including its trace. */
  public static CheckPermissionResponse toProto(CheckResult r) {
    var b =
        CheckPermissionResponse.newBuilder()
            .setPermissionship(checkPermissionshipToProto(r.permissionship()));
    if (!r.missingContext().isEmpty())
      b.setPartialCaveatInfo(
          PartialCaveatInfo.newBuilder().addAllMissingRequiredContext(r.missingContext()));
    if (r.checkedAt() != null) b.setCheckedAt(toProto(r.checkedAt()));
    if (r.debugTrace() != null) b.setDebugTrace(toProto(r.debugTrace()));
    if (r.expiration() != null) b.setOptionalExpiresAt(toProto(r.expiration()));
    return b.build();
  }

  /** A native permission check as a bulk request item (its own context only). */
  public static CheckBulkPermissionsRequestItem toProto(PermissionCheck check) {
    return toProto(check, null);
  }

  /**
   * A native permission check as the bulk request item the client sends: call-level context ({@code
   * CheckOptions.context}) merged by top-level key with the check's own, which wins.
   */
  public static CheckBulkPermissionsRequestItem toProto(
      PermissionCheck check, Map<String, Object> callContext) {
    if (check == null) throw new InvalidArgumentException("check must not be null");
    var builder =
        CheckBulkPermissionsRequestItem.newBuilder()
            .setResource(toProto(check.resource()))
            .setPermission(check.permission())
            .setSubject(toProto(check.subject()));
    boolean callEmpty = callContext == null || callContext.isEmpty();
    boolean itemEmpty = check.context() == null || check.context().isEmpty();
    if (!callEmpty || !itemEmpty) {
      var merged = new LinkedHashMap<String, Object>();
      if (callContext != null) merged.putAll(callContext);
      if (check.context() != null) merged.putAll(check.context());
      builder.setContext(toStruct(merged));
    }
    return builder.build();
  }

  /** See {@link #checkBulkToProto(List, List, Map)}; the checks carry no call-level context. */
  public static CheckBulkPermissionsResponse checkBulkToProto(
      List<PermissionCheck> requests, List<CheckResult> results) {
    return checkBulkToProto(requests, results, null);
  }

  /**
   * Rebuilds the server's bulk-check response from the checks and the results {@code checkBulk}
   * returned for them, in the same order: one pair per item carrying the echoed request item and
   * either the item's result or, for a result with an {@link CheckResult#error()} ({@code
   * BulkItemErrors.RETURNED}), its status with details. The response's checked-at is the last
   * item's revision; {@code checkBulk} may span several requests, each with its own revision.
   *
   * @param callContext the {@code CheckOptions.context} the checks were sent with, or {@code null}
   */
  public static CheckBulkPermissionsResponse checkBulkToProto(
      List<PermissionCheck> requests, List<CheckResult> results, Map<String, Object> callContext) {
    if (requests == null || results == null)
      throw new InvalidArgumentException("requests and results must not be null");
    if (requests.size() != results.size())
      throw new InvalidArgumentException(
          "check bulk: " + requests.size() + " requests but " + results.size() + " results");
    var b = CheckBulkPermissionsResponse.newBuilder();
    for (int i = 0; i < requests.size(); i++) {
      var pair =
          CheckBulkPermissionsPair.newBuilder().setRequest(toProto(requests.get(i), callContext));
      var r = results.get(i);
      if (r.error() != null) {
        var status = io.grpc.protobuf.StatusProto.fromThrowable(r.error());
        pair.setError(
            status != null
                ? status
                : com.google.rpc.Status.newBuilder()
                    .setCode(r.error().code().value())
                    .setMessage(String.valueOf(r.error().getMessage()))
                    .build());
      } else {
        var single = toProto(r);
        var item =
            CheckBulkPermissionsResponseItem.newBuilder()
                .setPermissionship(single.getPermissionship());
        if (single.hasPartialCaveatInfo()) item.setPartialCaveatInfo(single.getPartialCaveatInfo());
        if (single.hasDebugTrace()) item.setDebugTrace(single.getDebugTrace());
        pair.setItem(item);
      }
      if (r.checkedAt() != null) b.setCheckedAt(toProto(r.checkedAt()));
      b.addPairs(pair);
    }
    return b.build();
  }

  public static DebugInformation fromProto(com.authzed.api.v1.DebugInformation info) {
    return new DebugInformation(
        info.getSchemaUsed(), info.hasCheck() ? fromProto(info.getCheck()) : null);
  }

  public static com.authzed.api.v1.DebugInformation toProto(DebugInformation info) {
    var b = com.authzed.api.v1.DebugInformation.newBuilder();
    if (info.schemaUsed() != null) b.setSchemaUsed(info.schemaUsed());
    if (info.check() != null) b.setCheck(toProto(info.check()));
    return b.build();
  }

  public static DebugInformation.CheckTrace fromProto(CheckDebugTrace t) {
    var pt =
        switch (t.getPermissionType()) {
          case PERMISSION_TYPE_RELATION -> DebugInformation.PermissionType.RELATION;
          case PERMISSION_TYPE_PERMISSION -> DebugInformation.PermissionType.PERMISSION;
          default -> DebugInformation.PermissionType.UNSPECIFIED;
        };
    var result =
        switch (t.getResult()) {
          case PERMISSIONSHIP_HAS_PERMISSION -> Permissionship.HAS_PERMISSION;
          case PERMISSIONSHIP_NO_PERMISSION -> Permissionship.NO_PERMISSION;
          case PERMISSIONSHIP_CONDITIONAL_PERMISSION -> Permissionship.CONDITIONAL_PERMISSION;
          default -> Permissionship.UNSPECIFIED;
        };
    DebugInformation.CaveatEvaluation caveat = null;
    if (t.hasCaveatEvaluationInfo()) {
      var c = t.getCaveatEvaluationInfo();
      var cr =
          switch (c.getResult()) {
            case RESULT_UNEVALUATED -> DebugInformation.CaveatResult.UNEVALUATED;
            case RESULT_FALSE -> DebugInformation.CaveatResult.FALSE;
            case RESULT_TRUE -> DebugInformation.CaveatResult.TRUE;
            case RESULT_MISSING_SOME_CONTEXT -> DebugInformation.CaveatResult.MISSING_SOME_CONTEXT;
            default -> DebugInformation.CaveatResult.UNSPECIFIED;
          };
      caveat =
          new DebugInformation.CaveatEvaluation(
              c.getExpression(),
              cr,
              c.hasContext() ? fromStruct(c.getContext()) : null,
              c.getPartialCaveatInfo().getMissingRequiredContextList(),
              c.getCaveatName());
    }
    DebugInformation.Resolution resolution =
        t.hasWasCachedResult()
            ? new DebugInformation.Cached(t.getWasCachedResult())
            : t.hasSubProblems()
                ? new DebugInformation.Subproblems(
                    t.getSubProblems().getTracesList().stream().map(ProtoConv::fromProto).toList())
                : new DebugInformation.Unspecified();
    return new DebugInformation.CheckTrace(
        t.hasResource() ? fromProto(t.getResource()) : null,
        t.getPermission(),
        pt,
        t.hasSubject() ? fromProto(t.getSubject()) : null,
        result,
        caveat,
        t.hasDuration()
            ? Duration.ofSeconds(t.getDuration().getSeconds(), t.getDuration().getNanos())
            : null,
        resolution,
        t.hasOptionalExpiresAt() ? fromProto(t.getOptionalExpiresAt()) : null,
        t.getTraceOperationId(),
        t.getSource());
  }

  public static CheckDebugTrace toProto(DebugInformation.CheckTrace t) {
    var b = CheckDebugTrace.newBuilder();
    if (t.resource() != null) b.setResource(toProto(t.resource()));
    if (t.permission() != null) b.setPermission(t.permission());
    b.setPermissionType(
        switch (t.permissionType()) {
          case RELATION -> CheckDebugTrace.PermissionType.PERMISSION_TYPE_RELATION;
          case PERMISSION -> CheckDebugTrace.PermissionType.PERMISSION_TYPE_PERMISSION;
          default -> CheckDebugTrace.PermissionType.PERMISSION_TYPE_UNSPECIFIED;
        });
    if (t.subject() != null) b.setSubject(toProto(t.subject()));
    b.setResult(
        switch (t.result()) {
          case HAS_PERMISSION -> CheckDebugTrace.Permissionship.PERMISSIONSHIP_HAS_PERMISSION;
          case NO_PERMISSION -> CheckDebugTrace.Permissionship.PERMISSIONSHIP_NO_PERMISSION;
          case CONDITIONAL_PERMISSION ->
              CheckDebugTrace.Permissionship.PERMISSIONSHIP_CONDITIONAL_PERMISSION;
          default -> CheckDebugTrace.Permissionship.PERMISSIONSHIP_UNSPECIFIED;
        });
    var c = t.caveatEvaluation();
    if (c != null) {
      var cb =
          CaveatEvalInfo.newBuilder()
              .setResult(
                  switch (c.result()) {
                    case UNEVALUATED -> CaveatEvalInfo.Result.RESULT_UNEVALUATED;
                    case FALSE -> CaveatEvalInfo.Result.RESULT_FALSE;
                    case TRUE -> CaveatEvalInfo.Result.RESULT_TRUE;
                    case MISSING_SOME_CONTEXT -> CaveatEvalInfo.Result.RESULT_MISSING_SOME_CONTEXT;
                    default -> CaveatEvalInfo.Result.RESULT_UNSPECIFIED;
                  })
              .setPartialCaveatInfo(
                  PartialCaveatInfo.newBuilder().addAllMissingRequiredContext(c.missingContext()));
      if (c.expression() != null) cb.setExpression(c.expression());
      if (c.caveatName() != null) cb.setCaveatName(c.caveatName());
      if (c.context() != null) cb.setContext(toStruct(c.context()));
      b.setCaveatEvaluationInfo(cb);
    }
    if (t.duration() != null)
      b.setDuration(
          com.google.protobuf.Duration.newBuilder()
              .setSeconds(t.duration().getSeconds())
              .setNanos(t.duration().getNano()));
    if (t.resolution() instanceof DebugInformation.Cached cached)
      b.setWasCachedResult(cached.value());
    if (t.resolution() instanceof DebugInformation.Subproblems sub) {
      var sp = CheckDebugTrace.SubProblems.newBuilder();
      for (var child : sub.traces()) sp.addTraces(toProto(child));
      b.setSubProblems(sp);
    }
    if (t.expiration() != null) b.setOptionalExpiresAt(toProto(t.expiration()));
    if (t.traceOperationId() != null) b.setTraceOperationId(t.traceOperationId());
    if (t.source() != null) b.setSource(t.source());
    return b.build();
  }

  // ---------------------------------------------------------------- expand

  /**
   * Fails with {@code INTERNAL} when the tree, a node's expanded object or a subject is missing.
   */
  public static PermissionTree fromProto(PermissionRelationshipTree t) {
    if (t == null) throw malformed("expand response has no tree");
    if (!t.hasExpandedObject()) throw malformed("expanded node has no object");
    var expanded = fromProto(t.getExpandedObject());
    PermissionTree.IntermediateNode intermediate = null;
    if (t.hasIntermediate()) {
      AlgebraicSubjectSet algebraic = t.getIntermediate();
      var children = new ArrayList<PermissionTree>(algebraic.getChildrenCount());
      for (var child : algebraic.getChildrenList()) children.add(fromProto(child));
      var op =
          switch (algebraic.getOperation()) {
            case OPERATION_UNION -> PermissionTree.Operation.UNION;
            case OPERATION_INTERSECTION -> PermissionTree.Operation.INTERSECTION;
            case OPERATION_EXCLUSION -> PermissionTree.Operation.EXCLUSION;
            default -> PermissionTree.Operation.UNSPECIFIED;
          };
      intermediate = new PermissionTree.IntermediateNode(op, List.copyOf(children));
    }
    PermissionTree.LeafNode leaf = null;
    if (t.hasLeaf()) {
      DirectSubjectSet direct = t.getLeaf();
      var subjects = new ArrayList<PermissionTree.SubjectRef>(direct.getSubjectsCount());
      for (var subject : direct.getSubjectsList()) {
        fromProto(subject); // validates the subject's object
        subjects.add(
            new PermissionTree.SubjectRef(
                subject.getObject().getObjectType(),
                subject.getObject().getObjectId(),
                subject.getOptionalRelation()));
      }
      leaf = new PermissionTree.LeafNode(List.copyOf(subjects));
    }
    return new PermissionTree(
        new PermissionTree.ObjectRef(expanded.objectType(), expanded.objectId()),
        t.getExpandedRelation(),
        intermediate,
        leaf);
  }

  public static PermissionRelationshipTree toProto(PermissionTree t) {
    var b =
        PermissionRelationshipTree.newBuilder()
            .setExpandedObject(
                ObjectReference.newBuilder()
                    .setObjectType(t.expandedObject().objectType())
                    .setObjectId(t.expandedObject().objectID()))
            .setExpandedRelation(t.expandedRelation());
    if (t.intermediate() != null) {
      var a =
          AlgebraicSubjectSet.newBuilder()
              .setOperation(
                  switch (t.intermediate().operation()) {
                    case UNION -> AlgebraicSubjectSet.Operation.OPERATION_UNION;
                    case INTERSECTION -> AlgebraicSubjectSet.Operation.OPERATION_INTERSECTION;
                    case EXCLUSION -> AlgebraicSubjectSet.Operation.OPERATION_EXCLUSION;
                    default -> AlgebraicSubjectSet.Operation.OPERATION_UNSPECIFIED;
                  });
      for (var child : t.intermediate().children()) a.addChildren(toProto(child));
      b.setIntermediate(a);
    }
    if (t.leaf() != null) {
      var d = DirectSubjectSet.newBuilder();
      for (var s : t.leaf().subjects())
        d.addSubjects(
            SubjectReference.newBuilder()
                .setObject(
                    ObjectReference.newBuilder()
                        .setObjectType(s.subjectType())
                        .setObjectId(s.subjectID()))
                .setOptionalRelation(s.optionalRelation() == null ? "" : s.optionalRelation()));
      b.setLeaf(d);
    }
    return b.build();
  }
}
