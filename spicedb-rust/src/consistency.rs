use crate::types::ZedToken;

/// A consistency requirement for SpiceDB read operations.
///
/// Use the free functions in this module to construct a `Strategy`.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum Strategy {
    /// Fully consistent read. Most expensive but guarantees the latest data.
    Full,
    /// Minimize latency. SpiceDB picks a recent-enough revision for optimal
    /// performance. Recommended default for most read operations.
    MinLatency,
    /// Read at least as fresh as the given revision (ZedToken). Use for
    /// read-after-write consistency.
    AtLeast(ZedToken),
    /// Read at the exact given revision (ZedToken).
    Snapshot(ZedToken),
}

/// Returns a strategy that requires full consistency. This is the least
/// performant option but guarantees the most up-to-date results.
pub fn full() -> Strategy {
    Strategy::Full
}

/// Returns a strategy that uses SpiceDB's preferred revision for optimal
/// performance. This is the recommended default for most read operations.
pub fn min_latency() -> Strategy {
    Strategy::MinLatency
}

/// Returns a strategy that ensures results are at least as fresh as the given
/// revision. Use this for read-after-write consistency.
pub fn at_least(revision: ZedToken) -> Strategy {
    Strategy::AtLeast(revision)
}

/// Returns a strategy that reads at the exact given revision.
pub fn snapshot(revision: ZedToken) -> Strategy {
    Strategy::Snapshot(revision)
}

/// Returns [`at_least`] if the revision is `Some`, otherwise [`full`].
///
/// Use this when you have an optional revision from a previous write and want
/// the safest fallback.
pub fn at_least_or_full(revision: Option<ZedToken>) -> Strategy {
    match revision {
        Some(r) if !r.as_ref().is_empty() => at_least(r),
        Some(_) => full(),
        None => full(),
    }
}

/// Returns [`at_least`] if the revision is `Some`, otherwise [`min_latency`].
///
/// Use this when you have an optional revision but prefer performance over full
/// consistency as the fallback.
pub fn at_least_or_min_latency(revision: Option<ZedToken>) -> Strategy {
    match revision {
        Some(r) if !r.as_ref().is_empty() => at_least(r),
        Some(_) => min_latency(),
        None => min_latency(),
    }
}

use spicedb_proto::authzed::api::v1 as proto;

impl Strategy {
    pub(crate) fn validate(&self) -> Result<(), crate::error::SpiceDBError> {
        match self {
            Self::AtLeast(t) | Self::Snapshot(t) if t.as_ref().is_empty() => Err(
                crate::types::invalid("consistency.token", "must not be empty"),
            ),
            _ => Ok(()),
        }
    }
    pub(crate) fn to_proto(&self) -> proto::Consistency {
        let requirement = match self {
            Strategy::Full => proto::consistency::Requirement::FullyConsistent(true),
            Strategy::MinLatency => proto::consistency::Requirement::MinimizeLatency(true),
            Strategy::AtLeast(token) => {
                proto::consistency::Requirement::AtLeastAsFresh(proto::ZedToken {
                    token: token.to_string(),
                })
            }
            Strategy::Snapshot(token) => {
                proto::consistency::Requirement::AtExactSnapshot(proto::ZedToken {
                    token: token.to_string(),
                })
            }
        };
        proto::Consistency {
            requirement: Some(requirement),
        }
    }
}
