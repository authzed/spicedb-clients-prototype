package com.authzed.spicedb;

public sealed interface SubjectRelationFilter
    permits SubjectRelationFilter.Any, SubjectRelationFilter.Direct, SubjectRelationFilter.Named {
  static SubjectRelationFilter any() {
    return new Any();
  }

  static SubjectRelationFilter direct() {
    return new Direct();
  }

  static SubjectRelationFilter named(String name) {
    return new Named(name);
  }

  record Any() implements SubjectRelationFilter {}

  record Direct() implements SubjectRelationFilter {}

  record Named(String name) implements SubjectRelationFilter {
    public Named {
      JsonContext.required(name, "subject relation");
      if (name.equals("..."))
        throw new com.authzed.spicedb.errors.InvalidArgumentException("use direct relation filter");
    }
  }
}
