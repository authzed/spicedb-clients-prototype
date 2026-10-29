package com.authzed.spicedb;

import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.LocalValidationException;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.time.format.DateTimeParseException;
import java.util.LinkedHashMap;
import java.util.Map;

/**
 * A persisted relationship: resource, relation, subject, optional caveat (name and stored context)
 * and optional expiration.
 *
 * <p>{@link #toString()} and {@link #fromTuple(String)} use SpiceDB's full tuple syntax, including
 * {@code [caveat:{...}]} and {@code [expiration:...]}, and match SpiceDB's own formatter.
 */
public record Relationship(
    ObjectRef resource,
    String relation,
    SubjectRef subject,
    String caveatName,
    Map<String, Object> caveatContext,
    Instant expiration)
    implements Json.JsonValue {
  public Relationship {
    if (resource == null || subject == null)
      throw new LocalValidationException("resource and subject are required");
    JsonContext.required(relation, "relation");
    caveatContext = JsonContext.snapshot(caveatContext);
    if (caveatName != null) JsonContext.required(caveatName, "caveatName");
    if (caveatContext != null && caveatName == null)
      throw new LocalValidationException("caveat context requires a caveat name");
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

  /** A copy with a different resource; relation, subject, caveat and expiration are kept. */
  public Relationship withResource(ObjectRef resource) {
    return new Relationship(resource, relation, subject, caveatName, caveatContext, expiration);
  }

  /** A copy with a different relation. */
  public Relationship withRelation(String relation) {
    return new Relationship(resource, relation, subject, caveatName, caveatContext, expiration);
  }

  /** A copy with a different subject. */
  public Relationship withSubject(SubjectRef subject) {
    return new Relationship(resource, relation, subject, caveatName, caveatContext, expiration);
  }

  /** Short path for {@code resource().objectType()}. */
  public String resourceType() {
    return resource.objectType();
  }

  /** Short path for {@code resource().objectId()}. */
  public String resourceId() {
    return resource.objectId();
  }

  /** Short path for {@code subject().object().objectType()}. */
  public String subjectType() {
    return subject.object().objectType();
  }

  /** Short path for {@code subject().object().objectId()}. */
  public String subjectId() {
    return subject.object().objectId();
  }

  /** Short path for {@code subject().relation()}: {@code null} for a direct subject. */
  public String subjectRelation() {
    return subject.relation();
  }

  /**
   * Parses SpiceDB tuple syntax: {@code type:id#relation@type:id[#relation]}, optionally followed
   * by {@code [caveat]} or {@code [caveat:{json}]}, then optionally {@code [expiration:RFC3339]}. A
   * subject relation of {@code ...} means a direct subject.
   *
   * @throws InvalidArgumentException for anything SpiceDB's parser would reject
   */
  public static Relationship fromTuple(String tuple) {
    if (tuple == null) throw new LocalValidationException("tuple must not be null");
    try {
      int bracket = tuple.indexOf('[');
      String main = bracket < 0 ? tuple : tuple.substring(0, bracket);
      String suffix = bracket < 0 ? "" : tuple.substring(bracket);
      var sides = main.split("@", -1);
      if (sides.length != 2) throw new IllegalArgumentException("tuple requires one @");
      var r = sides[0].split("#", -1);
      var s = sides[1].split("#", -1);
      if (r.length != 2 || s.length > 2) throw new IllegalArgumentException("invalid tuple");
      var ro = r[0].split(":", 2);
      var so = s[0].split(":", 2);
      if (ro.length != 2 || so.length != 2) throw new IllegalArgumentException("invalid tuple");
      var subjectObject = new ObjectRef(so[0], so[1]);
      var subject =
          s.length == 2 && !s[1].equals("...")
              ? SubjectRef.named(subjectObject, s[1])
              : SubjectRef.direct(subjectObject);
      return parseSuffix(of(new ObjectRef(ro[0], ro[1]), r[1], subject), suffix);
    } catch (InvalidArgumentException e) {
      throw new LocalValidationException("invalid relationship tuple: " + e.getMessage(), e);
    } catch (RuntimeException e) {
      throw new LocalValidationException("invalid relationship tuple", e);
    }
  }

  private static final String EXPIRATION_PREFIX = "[expiration:";

  private static Relationship parseSuffix(Relationship rel, String suffix) {
    if (suffix.isEmpty()) return rel;
    if (!suffix.startsWith(EXPIRATION_PREFIX) || suffix.startsWith(EXPIRATION_PREFIX + "{")) {
      // [name] or [name:{json}]
      int end = 1;
      while (end < suffix.length() && suffix.charAt(end) != ':' && suffix.charAt(end) != ']') end++;
      if (end <= 1 || end >= suffix.length())
        throw new IllegalArgumentException("invalid caveat " + suffix);
      String name = suffix.substring(1, end);
      Map<String, Object> context = null;
      int rest;
      if (suffix.charAt(end) == ']') {
        rest = end + 1;
      } else {
        var parsed = Json.parse(suffix, end + 1);
        if (!(parsed.value() instanceof Map<?, ?> m))
          throw new IllegalArgumentException("caveat context must be a JSON object");
        context = new LinkedHashMap<>();
        for (var e : m.entrySet()) context.put((String) e.getKey(), e.getValue());
        if (parsed.end() >= suffix.length() || suffix.charAt(parsed.end()) != ']')
          throw new IllegalArgumentException("unterminated caveat " + suffix);
        rest = parsed.end() + 1;
      }
      rel = rel.withCaveat(name, context);
      suffix = suffix.substring(rest);
    }
    if (suffix.isEmpty()) return rel;
    if (!suffix.startsWith(EXPIRATION_PREFIX) || !suffix.endsWith("]"))
      throw new IllegalArgumentException("invalid tuple suffix " + suffix);
    String when = suffix.substring(EXPIRATION_PREFIX.length(), suffix.length() - 1);
    try {
      return rel.withExpiration(OffsetDateTime.parse(when).toInstant());
    } catch (DateTimeParseException e) {
      throw new IllegalArgumentException("invalid expiration " + when, e);
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

  /** SpiceDB tuple syntax, including caveat and expiration; round-trips with {@link #fromTuple}. */
  @Override
  public String toString() {
    var sb =
        new StringBuilder()
            .append(resource)
            .append('#')
            .append(relation)
            .append('@')
            .append(subject);
    if (caveatName != null) {
      sb.append('[').append(caveatName);
      if (caveatContext != null && !caveatContext.isEmpty())
        sb.append(':').append(Json.writeCanonical(caveatContext));
      sb.append(']');
    }
    if (expiration != null) sb.append(EXPIRATION_PREFIX).append(rfc3339(expiration)).append(']');
    return sb.toString();
  }

  /** RFC 3339 in UTC with trailing fractional zeros trimmed, as Go's RFC3339Nano prints it. */
  static String rfc3339(Instant instant) {
    var t = instant.atOffset(ZoneOffset.UTC);
    var sb =
        new StringBuilder(
            String.format(
                "%04d-%02d-%02dT%02d:%02d:%02d",
                t.getYear(),
                t.getMonthValue(),
                t.getDayOfMonth(),
                t.getHour(),
                t.getMinute(),
                t.getSecond()));
    if (t.getNano() != 0) {
      String frac = String.format("%09d", t.getNano());
      int end = frac.length();
      while (frac.charAt(end - 1) == '0') end--;
      sb.append('.').append(frac, 0, end);
    }
    return sb.append('Z').toString();
  }

  /** Structured diagnostic form; never an empty object. */
  @Override
  public Object toJsonValue() {
    var m = new LinkedHashMap<String, Object>();
    m.put("resourceType", resourceType());
    m.put("resourceId", resourceId());
    m.put("relation", relation);
    m.put("subjectType", subjectType());
    m.put("subjectId", subjectId());
    if (subject.relation() != null) m.put("subjectRelation", subject.relation());
    if (caveatName != null) m.put("caveatName", caveatName);
    if (caveatContext != null) m.put("caveatContext", caveatContext);
    if (expiration != null) m.put("expiration", rfc3339(expiration));
    return m;
  }

  /** Structured JSON diagnostic form, for structured loggers. */
  public String toJson() {
    return Json.write(toJsonValue());
  }
}
