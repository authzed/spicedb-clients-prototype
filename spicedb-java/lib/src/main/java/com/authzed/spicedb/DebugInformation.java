package com.authzed.spicedb;

import java.time.*;
import java.util.*;

/** Native diagnostic trace, present only when returned by the server. */
public record DebugInformation(String schemaUsed, CheckTrace check) {
  public enum PermissionType {
    UNSPECIFIED,
    RELATION,
    PERMISSION
  }

  public enum CaveatResult {
    UNSPECIFIED,
    UNEVALUATED,
    FALSE,
    TRUE,
    MISSING_SOME_CONTEXT
  }

  public sealed interface Resolution permits Cached, Subproblems, Unspecified {}

  public record Cached(boolean value) implements Resolution {}

  public record Subproblems(List<CheckTrace> traces) implements Resolution {
    public Subproblems {
      traces = List.copyOf(traces);
    }
  }

  public record Unspecified() implements Resolution {}

  public record CaveatEvaluation(
      String expression,
      CaveatResult result,
      Map<String, Object> context,
      List<String> missingContext,
      String caveatName) {
    public CaveatEvaluation {
      context = JsonContext.snapshot(context);
      missingContext = List.copyOf(missingContext);
    }
  }

  public record CheckTrace(
      ObjectRef resource,
      String permission,
      PermissionType permissionType,
      SubjectRef subject,
      Permissionship result,
      CaveatEvaluation caveatEvaluation,
      Duration duration,
      Resolution resolution,
      Instant expiration,
      String traceOperationId,
      String source) {}

  static DebugInformation fromProto(com.authzed.api.v1.DebugInformation info) {
    return new DebugInformation(
        info.getSchemaUsed(), info.hasCheck() ? trace(info.getCheck()) : null);
  }

  private static CheckTrace trace(com.authzed.api.v1.CheckDebugTrace t) {
    var r = t.getResource();
    var s = t.getSubject();
    var o = s.getObject();
    var object = t.hasSubject() ? new ObjectRef(o.getObjectType(), o.getObjectId()) : null;
    var subject =
        !t.hasSubject()
            ? null
            : s.getOptionalRelation().isEmpty()
                ? SubjectRef.direct(object)
                : SubjectRef.named(object, s.getOptionalRelation());
    var pt =
        switch (t.getPermissionType()) {
          case PERMISSION_TYPE_RELATION -> PermissionType.RELATION;
          case PERMISSION_TYPE_PERMISSION -> PermissionType.PERMISSION;
          default -> PermissionType.UNSPECIFIED;
        };
    var result =
        switch (t.getResult()) {
          case PERMISSIONSHIP_HAS_PERMISSION -> Permissionship.HAS_PERMISSION;
          case PERMISSIONSHIP_NO_PERMISSION -> Permissionship.NO_PERMISSION;
          case PERMISSIONSHIP_CONDITIONAL_PERMISSION -> Permissionship.CONDITIONAL_PERMISSION;
          default -> Permissionship.UNSPECIFIED;
        };
    CaveatEvaluation caveat = null;
    if (t.hasCaveatEvaluationInfo()) {
      var c = t.getCaveatEvaluationInfo();
      var cr =
          switch (c.getResult()) {
            case RESULT_UNEVALUATED -> CaveatResult.UNEVALUATED;
            case RESULT_FALSE -> CaveatResult.FALSE;
            case RESULT_TRUE -> CaveatResult.TRUE;
            case RESULT_MISSING_SOME_CONTEXT -> CaveatResult.MISSING_SOME_CONTEXT;
            default -> CaveatResult.UNSPECIFIED;
          };
      var context = new LinkedHashMap<String, Object>();
      c.getContext()
          .getFieldsMap()
          .forEach((k, v) -> context.put(k, SpiceDBClient.fromProtoValue(v)));
      caveat =
          new CaveatEvaluation(
              c.getExpression(),
              cr,
              c.hasContext() ? context : null,
              c.getPartialCaveatInfo().getMissingRequiredContextList(),
              c.getCaveatName());
    }
    Resolution resolution =
        t.hasWasCachedResult()
            ? new Cached(t.getWasCachedResult())
            : t.hasSubProblems()
                ? new Subproblems(
                    t.getSubProblems().getTracesList().stream()
                        .map(DebugInformation::trace)
                        .toList())
                : new Unspecified();
    return new CheckTrace(
        t.hasResource() ? new ObjectRef(r.getObjectType(), r.getObjectId()) : null,
        t.getPermission(),
        pt,
        subject,
        result,
        caveat,
        t.hasDuration()
            ? Duration.ofSeconds(t.getDuration().getSeconds(), t.getDuration().getNanos())
            : null,
        resolution,
        t.hasOptionalExpiresAt()
            ? Instant.ofEpochSecond(
                t.getOptionalExpiresAt().getSeconds(), t.getOptionalExpiresAt().getNanos())
            : null,
        t.getTraceOperationId(),
        t.getSource());
  }
}
