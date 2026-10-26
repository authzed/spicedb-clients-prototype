package com.authzed.spicedb;

import java.time.Instant;
import java.util.Map;

public record Relationship(
    ObjectRef resource,
    String relation,
    SubjectRef subject,
    String caveatName,
    Map<String, Object> caveatContext,
    Instant expiration) {
  public Relationship {
    if (resource == null || subject == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException(
          "resource and subject are required");
    JsonContext.required(relation, "relation");
    caveatContext = JsonContext.snapshot(caveatContext);
    if (caveatName != null) JsonContext.required(caveatName, "caveatName");
    if (caveatContext != null && caveatName == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException(
          "caveat context requires a caveat name");
  }

  public static Relationship of(ObjectRef resource, String relation, SubjectRef subject) {
    return new Relationship(resource, relation, subject, null, null, null);
  }

  public Relationship withCaveat(String name, Map<String, Object> context) {
    JsonContext.required(name, "caveatName");
    return new Relationship(resource, relation, subject, name, context, expiration);
  }

  public Relationship withExpiration(Instant expiration) {
    return new Relationship(resource, relation, subject, caveatName, caveatContext, expiration);
  }

  public static Relationship fromTuple(String tuple) {
    try {
      var sides = tuple.split("@", -1);
      var r = sides[0].split("#", -1);
      var ro = r[0].split(":", 2);
      var s = sides[1].split("#", -1);
      var so = s[0].split(":", 2);
      if (sides.length != 2 || r.length != 2 || s.length > 2) throw new IllegalArgumentException();
      return of(
          new ObjectRef(ro[0], ro[1]),
          r[1],
          s.length == 2
              ? SubjectRef.named(new ObjectRef(so[0], so[1]), s[1])
              : SubjectRef.direct(new ObjectRef(so[0], so[1])));
    } catch (RuntimeException e) {
      throw new com.authzed.spicedb.errors.InvalidArgumentException(
          "invalid relationship tuple", e);
    }
  }

  public Filter toFilter() {
    return Filter.of(resource.objectType())
        .withResourceId(resource.objectId())
        .withRelation(relation)
        .withSubjectType(subject.object().objectType())
        .withSubjectId(subject.object().objectId())
        .withSubjectRelation(
            subject.relation() == null
                ? SubjectRelationFilter.direct()
                : SubjectRelationFilter.named(subject.relation()));
  }

  @Override
  public String toString() {
    return resource.objectType()
        + ":"
        + resource.objectId()
        + "#"
        + relation
        + "@"
        + subject.object().objectType()
        + ":"
        + subject.object().objectId()
        + (subject.relation() == null ? "" : "#" + subject.relation());
  }
}
