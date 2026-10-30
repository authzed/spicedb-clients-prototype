package com.authzed.spicedb;

import com.authzed.spicedb.errors.LocalValidationException;
import java.util.Map;

public record PermissionCheck(
    ObjectRef resource,
    String permission,
    SubjectRef subject,
    Consistency consistency,
    Map<String, Object> context) {
  public PermissionCheck {
    if (resource == null || subject == null || consistency == null)
      throw new LocalValidationException("resource, subject and consistency are required");
    JsonContext.required(permission, "permission");
    context = JsonContext.snapshot(context);
  }

  public PermissionCheck(
      ObjectRef resource, String permission, SubjectRef subject, Consistency consistency) {
    this(resource, permission, subject, consistency, null);
  }

  public PermissionCheck withContext(Map<String, Object> context) {
    return new PermissionCheck(resource, permission, subject, consistency, context);
  }

  /** Creates a mutable builder for an immutable permission check. */
  public static Builder builder() {
    return new Builder();
  }

  public static final class Builder {
    private ObjectRef resource;
    private String permission;
    private SubjectRef subject;
    private Consistency consistency;
    private Map<String, Object> context;

    private Builder() {}

    public Builder resource(ObjectRef value) {
      resource = value;
      return this;
    }

    public Builder permission(String value) {
      permission = value;
      return this;
    }

    public Builder subject(SubjectRef value) {
      subject = value;
      return this;
    }

    public Builder consistency(Consistency value) {
      consistency = value;
      return this;
    }

    public Builder context(Map<String, Object> value) {
      context = JsonContext.snapshot(value);
      return this;
    }

    /** Validates required fields and builds an independent immutable request. */
    public PermissionCheck build() {
      return new PermissionCheck(resource, permission, subject, consistency, context);
    }
  }
}
