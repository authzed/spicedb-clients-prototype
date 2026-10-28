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
    return com.authzed.spicedb.protoconv.ProtoConv.fromProto(info);
  }
}
