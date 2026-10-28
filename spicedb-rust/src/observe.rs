//! Observable native values: readable string forms, SpiceDB tuple syntax,
//! client-side filter matching, short accessors and copy helpers.
//!
//! See root DESIGN.md, "Make native values observable and testable without the
//! server". Every value here also derives `Debug` (structured logging) and,
//! with the `serde` feature, `serde::Serialize`.

use std::fmt;
use std::str::FromStr;

use chrono::{DateTime, Utc};

use super::*;

impl fmt::Display for ObjectRef {
    /// `type:id`.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}:{}", self.object_type(), self.object_id())
    }
}

impl fmt::Display for SubjectRef {
    /// `type:id` or `type:id#relation`.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.object())?;
        if let Some(r) = self.relation() {
            write!(f, "#{r}")?;
        }
        Ok(())
    }
}

impl FromStr for ObjectRef {
    type Err = SpiceDBError;
    /// Parses `type:id`.
    fn from_str(s: &str) -> Result<Self, SpiceDBError> {
        let (t, id) = s
            .split_once(':')
            .ok_or_else(|| invalid("object", "expected type:id"))?;
        ObjectRef::new(t, id)
    }
}

impl FromStr for SubjectRef {
    type Err = SpiceDBError;
    /// Parses `type:id` or `type:id#relation`; `#...` means a direct subject.
    fn from_str(s: &str) -> Result<Self, SpiceDBError> {
        let (object, relation) = match s.split_once('#') {
            Some((o, "...")) => (o, None),
            Some((o, r)) => (o, Some(r.to_string())),
            None => (s, None),
        };
        SubjectRef::new(object.parse()?, relation)
    }
}

impl FromStr for Relationship {
    type Err = SpiceDBError;
    /// Same as [`Relationship::from_tuple`].
    fn from_str(s: &str) -> Result<Self, SpiceDBError> {
        Relationship::from_tuple(s)
    }
}

/// Formats an expiration the way SpiceDB's tuple formatter does (Go's
/// `time.RFC3339Nano`): UTC, `Z`, and fractional seconds only when nonzero,
/// with trailing zeros trimmed.
fn format_expiration(t: &DateTime<Utc>) -> String {
    let mut s = t.format("%Y-%m-%dT%H:%M:%S").to_string();
    let nanos = t.timestamp_subsec_nanos();
    if nanos > 0 {
        let frac = format!("{nanos:09}");
        s.push('.');
        s.push_str(frac.trim_end_matches('0'));
    }
    s.push('Z');
    s
}

/// Formats a caveat context as compact JSON with sorted keys (serde_json's
/// default map is ordered), matching SpiceDB's formatter.
fn format_context(context: &Context) -> String {
    let map: serde_json::Map<String, serde_json::Value> = context
        .iter()
        .map(|(k, v)| (k.clone(), v.clone()))
        .collect();
    serde_json::Value::Object(map).to_string()
}

impl fmt::Display for Relationship {
    /// SpiceDB tuple syntax, including caveat and expiration:
    /// `document:readme#viewer@user:alice[ip:{"cidr":"10.0.0.0/8"}][expiration:2030-01-01T00:00:00Z]`.
    /// [`Relationship::from_tuple`] parses the result.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}#{}@{}", self.resource, self.relation, self.subject)?;
        if !self.caveat_name.is_empty() {
            write!(f, "[{}", self.caveat_name)?;
            if let Some(c) = self.caveat_context.as_ref().filter(|c| !c.is_empty()) {
                write!(f, ":{}", format_context(c))?;
            }
            f.write_str("]")?;
        }
        if let Some(t) = &self.expiration {
            write!(f, "[expiration:{}]", format_expiration(t))?;
        }
        Ok(())
    }
}

const EXPIRATION_PREFIX: &str = "[expiration:";

impl Relationship {
    /// Parses SpiceDB's tuple syntax:
    /// `type:id#relation@type:id[#relation][[caveat]|[caveat:{json}]][[expiration:RFC3339]]`.
    ///
    /// A subject relation of `...` means a direct subject. The caveat context is
    /// decoded as one JSON object, so `]` inside a context string does not end
    /// the bracket.
    pub fn from_tuple(tuple: &str) -> Result<Self, SpiceDBError> {
        let bad = || {
            invalid(
                "tuple",
                "expected type:id#relation@type:id[#relation][caveat][expiration]",
            )
        };
        let (main, suffix) = tuple.split_at(tuple.find('[').unwrap_or(tuple.len()));
        let (resource, subject) = main.split_once('@').ok_or_else(bad)?;
        if subject.contains('@') {
            return Err(bad());
        }
        let (resource, relation) = resource.split_once('#').ok_or_else(bad)?;
        let mut r = Self::new(resource.parse()?, relation, subject.parse()?)?;
        let mut rest = suffix;
        if !rest.is_empty()
            && (!rest.starts_with(EXPIRATION_PREFIX)
                || rest[EXPIRATION_PREFIX.len()..].starts_with('{'))
        {
            let (name, context, after) = parse_caveat_bracket(rest)?;
            r = r.with_caveat(name, context);
            rest = after;
        }
        if !rest.is_empty() {
            let value = rest
                .strip_prefix(EXPIRATION_PREFIX)
                .and_then(|v| v.strip_suffix(']'))
                .ok_or_else(|| invalid("tuple", "invalid suffix after caveat"))?;
            let t = DateTime::parse_from_rfc3339(value)
                .map_err(|e| invalid("tuple expiration", &e.to_string()))?;
            r = r.with_expiration(t.with_timezone(&Utc));
        }
        r.validate()?;
        Ok(r)
    }

    /// Resource object type, short for `resource().object_type()`.
    pub fn resource_type(&self) -> &str {
        self.resource.object_type()
    }
    /// Resource object ID, short for `resource().object_id()`.
    pub fn resource_id(&self) -> &str {
        self.resource.object_id()
    }
    /// Subject object type, short for `subject().object().object_type()`.
    pub fn subject_type(&self) -> &str {
        self.subject.object().object_type()
    }
    /// Subject object ID, short for `subject().object().object_id()`.
    pub fn subject_id(&self) -> &str {
        self.subject.object().object_id()
    }
    /// Subject relation; `None` for a direct subject.
    pub fn subject_relation(&self) -> Option<&str> {
        self.subject.relation()
    }

    /// A copy on a different resource, keeping relation, subject, caveat and
    /// expiration.
    pub fn with_resource(mut self, resource: ObjectRef) -> Self {
        self.resource = resource;
        self
    }
    /// A copy with a different subject.
    pub fn with_subject(mut self, subject: SubjectRef) -> Self {
        self.subject = subject;
        self
    }
    /// A copy with a different relation. Fails for an empty relation.
    pub fn with_relation(mut self, relation: impl Into<String>) -> Result<Self, SpiceDBError> {
        let relation = relation.into();
        required("relation", &relation)?;
        self.relation = relation;
        Ok(self)
    }
}

/// Parses `[name]` or `[name:{json}]` at the start of `s`, returning what
/// follows.
fn parse_caveat_bracket(s: &str) -> Result<(String, Option<Context>, &str), SpiceDBError> {
    let bad = || invalid("tuple caveat", "expected [name] or [name:{json}]");
    let body = &s[1..];
    let end = body.find([':', ']']).ok_or_else(bad)?;
    if end == 0 {
        return Err(bad());
    }
    let name = body[..end].to_string();
    if body.as_bytes()[end] == b']' {
        return Ok((name, None, &body[end + 1..]));
    }
    let json = &body[end + 1..];
    let mut values = serde_json::Deserializer::from_str(json).into_iter::<serde_json::Value>();
    let value = values
        .next()
        .ok_or_else(bad)?
        .map_err(|e| invalid("tuple caveat context", &e.to_string()))?;
    let offset = values.byte_offset();
    let serde_json::Value::Object(map) = value else {
        return Err(invalid("tuple caveat context", "must be a JSON object"));
    };
    let after = json[offset..].strip_prefix(']').ok_or_else(bad)?;
    Ok((name, Some(map.into_iter().collect()), after))
}

impl Filter {
    /// Reports whether `relationship` matches this filter with SpiceDB's
    /// semantics: resource type, resource ID or ID prefix, relation, subject
    /// type and ID, and the subject relation (`Any` matches every subject,
    /// `Direct` only subjects without a relation, `Named` exactly that
    /// relation). Use it in test doubles instead of reimplementing filters.
    ///
    /// A filter that [`to_proto`](Self) would reject (for example a subject ID
    /// without a subject type) matches nothing.
    pub fn matches(&self, relationship: &Relationship) -> bool {
        if self.to_proto().is_err() {
            return false;
        }
        let eq = |want: &Option<String>, got: &str| want.as_deref().is_none_or(|w| w == got);
        if self.resource_type != relationship.resource_type()
            || !eq(&self.resource_id, relationship.resource_id())
            || !self
                .resource_id_prefix
                .as_deref()
                .is_none_or(|p| relationship.resource_id().starts_with(p))
            || !eq(&self.relation, relationship.relation())
            || !eq(&self.subject_type, relationship.subject_type())
            || !eq(&self.subject_id, relationship.subject_id())
        {
            return false;
        }
        match &self.subject_relation {
            SubjectRelationFilter::Any => true,
            SubjectRelationFilter::Direct => relationship.subject_relation().is_none(),
            SubjectRelationFilter::Named(n) => relationship.subject_relation() == Some(n.as_str()),
        }
    }
}

impl fmt::Display for SubjectRelationFilter {
    /// `*` (any), `...` (direct) or the relation name.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            SubjectRelationFilter::Any => f.write_str("*"),
            SubjectRelationFilter::Direct => f.write_str("..."),
            SubjectRelationFilter::Named(n) => f.write_str(n),
        }
    }
}

impl fmt::Display for Filter {
    /// A zed-like form with `*` for unset fields, e.g.
    /// `document:*#viewer@user:alice` or `document:doc-%*#*@*`.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let or = |v: &Option<String>| v.clone().unwrap_or_else(|| "*".into());
        let id = match &self.resource_id_prefix {
            Some(p) => format!("{p}%*"),
            None => or(&self.resource_id),
        };
        write!(
            f,
            "{}:{}#{}@{}",
            self.resource_type,
            id,
            or(&self.relation),
            or(&self.subject_type)
        )?;
        if self.subject_type.is_some() {
            write!(f, ":{}", or(&self.subject_id))?;
            if self.subject_relation != SubjectRelationFilter::Any {
                write!(f, "#{}", self.subject_relation)?;
            }
        }
        Ok(())
    }
}

impl Update {
    /// An update value, e.g. for test doubles or protocol conversion.
    pub fn new(operation: UpdateOperation, relationship: Relationship) -> Self {
        Self {
            operation,
            relationship,
        }
    }
}

impl Precondition {
    pub fn new(operation: PreconditionOperation, filter: Filter) -> Self {
        Self { operation, filter }
    }
}

impl fmt::Display for ReadRelationship {
    /// The relationship's tuple form.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        self.relationship.fmt(f)
    }
}

impl fmt::Display for ExportedRelationship {
    /// The relationship's tuple form.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        self.relationship.fmt(f)
    }
}

impl fmt::Display for UpdateOperation {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            UpdateOperation::Create => "CREATE",
            UpdateOperation::Touch => "TOUCH",
            UpdateOperation::Delete => "DELETE",
            UpdateOperation::Unspecified => "UNSPECIFIED",
        })
    }
}

impl fmt::Display for Update {
    /// `TOUCH(document:readme#viewer@user:alice)`.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}({})", self.operation, self.relationship)
    }
}

impl fmt::Display for PreconditionOperation {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            PreconditionOperation::MustMatch => "MUST_MATCH",
            PreconditionOperation::MustNotMatch => "MUST_NOT_MATCH",
        })
    }
}

impl fmt::Display for Precondition {
    /// `MUST_MATCH(document:*#viewer@*)`.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}({})", self.operation, self.filter)
    }
}

impl fmt::Display for Transaction {
    /// Preconditions then updates, e.g.
    /// `[MUST_NOT_MATCH(document:*#owner@*), TOUCH(document:readme#viewer@user:alice)]`.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("[")?;
        let mut first = true;
        let mut sep = |f: &mut fmt::Formatter<'_>| {
            if !std::mem::take(&mut first) {
                f.write_str(", ")?;
            }
            Ok::<(), fmt::Error>(())
        };
        for p in self.preconditions() {
            sep(f)?;
            write!(f, "{p}")?;
        }
        for (op, r) in self.updates() {
            sep(f)?;
            write!(f, "{op}({r})")?;
        }
        f.write_str("]")
    }
}
