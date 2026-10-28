package com.authzed.spicedb;

import java.time.Instant;
import java.util.List;

/** Native experimental materialization results. */
public final class Materialize {
  private Materialize() {}

  public record RoaringLookupResourcesResult(byte[] bitmap, long cardinality, ZedToken atRevision) {
    public RoaringLookupResourcesResult {
      bitmap = bitmap.clone();
    }

    @Override
    public byte[] bitmap() {
      return bitmap.clone();
    }
  }

  public enum SetOperation {
    UNSPECIFIED,
    ADDED,
    REMOVED
  }

  public record SetRef(ObjectRef object, String permissionOrRelation) {}

  public sealed interface Child permits ChildSet, ChildMember, UnknownChild {}

  public record ChildSet(SetRef set) implements Child {}

  public record ChildMember(ObjectRef object, String permissionOrRelation) implements Child {}

  public record UnknownChild() implements Child {}

  public record PermissionSetChange(
      ZedToken atRevision, SetOperation operation, SetRef parentSet, Child child) {}

  public record LookupPermissionSet(PermissionSetChange change, String resumeCursor) {}

  public record WatchedPermission(
      String resourceType, String permission, String subjectType, String subjectRelation) {}

  public sealed interface PermissionSetEvent
      permits Change, CompletedRevision, LookupRequired, BreakingSchemaChange, UnknownEvent {}

  public record Change(PermissionSetChange change) implements PermissionSetEvent {}

  public record CompletedRevision(ZedToken revision) implements PermissionSetEvent {}

  public record LookupRequired(ZedToken requiredLookupAt) implements PermissionSetEvent {}

  public record BreakingSchemaChange(ZedToken changeAt, List<WatchedPermission> affectedPermissions)
      implements PermissionSetEvent {
    public BreakingSchemaChange {
      affectedPermissions = List.copyOf(affectedPermissions);
    }
  }

  public record UnknownEvent() implements PermissionSetEvent {}

  public record DownloadFile(String name, String url) {}

  public record DownloadPermissionSetsResult(
      List<DownloadFile> files, Instant timestamp, ZedToken atRevision) {
    public DownloadPermissionSetsResult {
      files = List.copyOf(files);
    }
  }

  static SetRef set(com.authzed.api.materialize.v0.SetReference s) {
    return new SetRef(
        new ObjectRef(s.getObjectType(), s.getObjectId()), s.getPermissionOrRelation());
  }

  static PermissionSetChange change(com.authzed.api.materialize.v0.PermissionSetChange c) {
    var op =
        switch (c.getOperation()) {
          case SET_OPERATION_ADDED -> SetOperation.ADDED;
          case SET_OPERATION_REMOVED -> SetOperation.REMOVED;
          default -> SetOperation.UNSPECIFIED;
        };
    Child child = new UnknownChild();
    if (c.hasChildSet()) child = new ChildSet(set(c.getChildSet()));
    else if (c.hasChildMember()) {
      var m = c.getChildMember();
      child =
          new ChildMember(
              new ObjectRef(m.getObjectType(), m.getObjectId()),
              m.getOptionalPermissionOrRelation().isEmpty()
                  ? null
                  : m.getOptionalPermissionOrRelation());
    }
    return new PermissionSetChange(
        c.hasAtRevision() ? new ZedToken(c.getAtRevision().getToken()) : null,
        op,
        c.hasParentSet() ? set(c.getParentSet()) : null,
        child);
  }

  static PermissionSetEvent event(com.authzed.api.materialize.v0.WatchPermissionSetsResponse r) {
    if (r.hasChange()) return new Change(change(r.getChange()));
    if (r.hasCompletedRevision())
      return new CompletedRevision(new ZedToken(r.getCompletedRevision().getToken()));
    if (r.hasLookupPermissionSetsRequired())
      return new LookupRequired(
          r.getLookupPermissionSetsRequired().hasRequiredLookupAt()
              ? new ZedToken(r.getLookupPermissionSetsRequired().getRequiredLookupAt().getToken())
              : null);
    if (r.hasBreakingSchemaChange()) {
      var c = r.getBreakingSchemaChange();
      return new BreakingSchemaChange(
          c.hasChangeAt() ? new ZedToken(c.getChangeAt().getToken()) : null,
          c.getAffectedPermissionsList().stream()
              .map(
                  p ->
                      new WatchedPermission(
                          p.getResourceType(),
                          p.getPermission(),
                          p.getSubjectType(),
                          p.getOptionalSubjectRelation().isEmpty()
                              ? null
                              : p.getOptionalSubjectRelation()))
              .toList());
    }
    return new UnknownEvent();
  }
}
