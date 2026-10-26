package com.authzed.spicedb;

public record SubjectRef(ObjectRef object, String relation) {
  public SubjectRef {
    if (object == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("subject object is required");
    if (relation != null) {
      JsonContext.required(relation, "subject relation");
      if (relation.equals("..."))
        throw new com.authzed.spicedb.errors.InvalidArgumentException(
            "use direct() for direct subjects");
    }
  }

  public static SubjectRef direct(ObjectRef object) {
    return new SubjectRef(object, null);
  }

  public static SubjectRef named(ObjectRef object, String relation) {
    JsonContext.required(relation, "relation");
    return new SubjectRef(object, relation);
  }
}
