//! Consistency strategies for SpiceDB read operations.
//!
//! Every read operation on the SpiceDB client requires an explicit consistency
//! strategy. This prevents silent defaults and makes consistency choices visible.
//!
//! ZedTokens (revisions) are opaque `String` values, never proto types.
//!
//! # Examples
//!
//! ```
//! use spicedb::consistency;
//!
//! // Fully consistent — least performant, most up-to-date
//! let cs = consistency::full();
//!
//! // Minimize latency — SpiceDB picks an optimal revision
//! let cs = consistency::min_latency();
//!
//! // At least as fresh as a previous write
//! let cs = consistency::at_least("some-zedtoken");
//!
//! // Exact snapshot
//! let cs = consistency::snapshot("some-zedtoken");
//!
//! // Conditional: use the token if present, otherwise full consistency
//! let cs = consistency::at_least_or_full(Some("some-zedtoken"));
//! let cs = consistency::at_least_or_full(None::<&str>);
//!
//! // Conditional: use the token if present, otherwise minimize latency
//! let cs = consistency::at_least_or_min_latency(Some("some-zedtoken"));
//! ```

/// A consistency requirement for SpiceDB read operations.
///
/// Use the free functions in this module to construct a `Strategy`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Strategy {
    /// Fully consistent read. Most expensive but guarantees the latest data.
    Full,
    /// Minimize latency. SpiceDB picks a recent-enough revision for optimal
    /// performance. Recommended default for most read operations.
    MinLatency,
    /// Read at least as fresh as the given revision (ZedToken). Use for
    /// read-after-write consistency.
    AtLeast(String),
    /// Read at the exact given revision (ZedToken).
    Snapshot(String),
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
pub fn at_least(revision: impl Into<String>) -> Strategy {
    Strategy::AtLeast(revision.into())
}

/// Returns a strategy that reads at the exact given revision.
pub fn snapshot(revision: impl Into<String>) -> Strategy {
    Strategy::Snapshot(revision.into())
}

/// Returns [`at_least`] if the revision is `Some`, otherwise [`full`].
///
/// Use this when you have an optional revision from a previous write and want
/// the safest fallback.
pub fn at_least_or_full(revision: Option<impl Into<String>>) -> Strategy {
    match revision {
        Some(r) => at_least(r),
        None => full(),
    }
}

/// Returns [`at_least`] if the revision is `Some`, otherwise [`min_latency`].
///
/// Use this when you have an optional revision but prefer performance over full
/// consistency as the fallback.
pub fn at_least_or_min_latency(revision: Option<impl Into<String>>) -> Strategy {
    match revision {
        Some(r) => at_least(r),
        None => min_latency(),
    }
}

// TODO: When spicedb-proto types are available, add a method to convert
// Strategy to the proto Consistency type:
//
// impl Strategy {
//     pub(crate) fn to_proto(&self) -> proto::Consistency { ... }
// }

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_full() {
        assert_eq!(full(), Strategy::Full);
    }

    #[test]
    fn test_min_latency() {
        assert_eq!(min_latency(), Strategy::MinLatency);
    }

    #[test]
    fn test_at_least() {
        let s = at_least("token-123");
        assert_eq!(s, Strategy::AtLeast("token-123".to_string()));
    }

    #[test]
    fn test_at_least_accepts_string() {
        let s = at_least(String::from("token-456"));
        assert_eq!(s, Strategy::AtLeast("token-456".to_string()));
    }

    #[test]
    fn test_snapshot() {
        let s = snapshot("snap-789");
        assert_eq!(s, Strategy::Snapshot("snap-789".to_string()));
    }

    #[test]
    fn test_at_least_or_full_with_some() {
        let s = at_least_or_full(Some("token-abc"));
        assert_eq!(s, Strategy::AtLeast("token-abc".to_string()));
    }

    #[test]
    fn test_at_least_or_full_with_none() {
        let s = at_least_or_full(None::<&str>);
        assert_eq!(s, Strategy::Full);
    }

    #[test]
    fn test_at_least_or_min_latency_with_some() {
        let s = at_least_or_min_latency(Some("token-def"));
        assert_eq!(s, Strategy::AtLeast("token-def".to_string()));
    }

    #[test]
    fn test_at_least_or_min_latency_with_none() {
        let s = at_least_or_min_latency(None::<&str>);
        assert_eq!(s, Strategy::MinLatency);
    }

    #[test]
    fn test_strategy_is_clone() {
        let s = at_least("token");
        let s2 = s.clone();
        assert_eq!(s, s2);
    }

    #[test]
    fn test_strategy_debug() {
        let s = full();
        let dbg = format!("{:?}", s);
        assert_eq!(dbg, "Full");
    }
}
