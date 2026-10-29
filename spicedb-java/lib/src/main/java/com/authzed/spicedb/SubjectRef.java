package com.authzed.spicedb;

import com.authzed.spicedb.errors.LocalValidationException;

public record SubjectRef(ObjectRef object, String relation) {
  public SubjectRef {
    if (object == null) throw new LocalValidationException("subject object is required");
    if (relation != null) {
      JsonContext.required(relation, "subject relation");
      if (relation.equals("..."))
        throw new LocalValidationException("use direct() for direct subjects");
    }
  }

  /** {@code type:id} or {@code type:id#relation}, as in SpiceDB tuple syntax. */
  @Override
  public String toString() {
    return relation == null ? object.toString() : object + "#" + relation;
  }

  public static SubjectRef direct(ObjectRef object) {
    return new SubjectRef(object, null);
  }

  public static SubjectRef named(ObjectRef object, String relation) {
    JsonContext.required(relation, "relation");
    return new SubjectRef(object, relation);
  }
}
