use spicedb::consistency::{self, Strategy};

#[test]
fn test_full() {
    assert_eq!(consistency::full(), Strategy::Full);
}

#[test]
fn test_min_latency() {
    assert_eq!(consistency::min_latency(), Strategy::MinLatency);
}

#[test]
fn test_at_least() {
    let s = consistency::at_least(("token-123").into());
    assert_eq!(s, Strategy::AtLeast("token-123".to_string().into()));
}

#[test]
fn test_at_least_with_owned_string() {
    let s = consistency::at_least((String::from("token-456")).into());
    assert_eq!(s, Strategy::AtLeast("token-456".to_string().into()));
}

#[test]
fn test_snapshot() {
    let s = consistency::snapshot(("snap-789").into());
    assert_eq!(s, Strategy::Snapshot("snap-789".to_string().into()));
}

#[test]
fn test_at_least_or_full_with_some() {
    let s = consistency::at_least_or_full(Some(("token-abc").into()));
    assert_eq!(s, Strategy::AtLeast("token-abc".to_string().into()));
}

#[test]
fn test_at_least_or_full_with_none() {
    let s = consistency::at_least_or_full(None);
    assert_eq!(s, Strategy::Full);
}

#[test]
fn test_at_least_or_min_latency_with_some() {
    let s = consistency::at_least_or_min_latency(Some(("token-def").into()));
    assert_eq!(s, Strategy::AtLeast("token-def".to_string().into()));
}

#[test]
fn test_at_least_or_min_latency_with_none() {
    let s = consistency::at_least_or_min_latency(None);
    assert_eq!(s, Strategy::MinLatency);
}

#[test]
fn test_strategy_clone_and_eq() {
    let s = consistency::at_least(("token").into());
    let s2 = s.clone();
    assert_eq!(s, s2);
}

#[test]
fn test_strategy_debug_formatting() {
    assert_eq!(format!("{:?}", consistency::full()), "Full");
    assert_eq!(format!("{:?}", consistency::min_latency()), "MinLatency");
    assert!(format!("{:?}", consistency::at_least(("t").into())).contains("AtLeast"));
    assert!(format!("{:?}", consistency::snapshot(("t").into())).contains("Snapshot"));
}

/// `Display` names the strategy and its token (root DESIGN.md, "Make native
/// values observable and testable without the server").
#[test]
fn test_strategy_display() {
    assert_eq!(consistency::full().to_string(), "full");
    assert_eq!(consistency::min_latency().to_string(), "min_latency");
    assert_eq!(
        consistency::at_least("GhUKEzE2".into()).to_string(),
        "at_least(GhUKEzE2)"
    );
    assert_eq!(
        consistency::snapshot("GhUKEzE2".into()).to_string(),
        "snapshot(GhUKEzE2)"
    );
    assert_eq!(
        consistency::at_least_or_full(Some("GhUKEzE2".into())).to_string(),
        "at_least(GhUKEzE2)"
    );
    assert_eq!(
        consistency::at_least_or_min_latency(None).to_string(),
        "min_latency"
    );
}

/// With `serde`, a strategy serializes as `{"strategy":...,"token":...}`,
/// never as an empty object.
#[cfg(feature = "serde")]
#[test]
fn test_strategy_serde() {
    let json = |s: Strategy| serde_json::to_string(&s).unwrap();
    assert_eq!(json(consistency::full()), r#"{"strategy":"full"}"#);
    assert_eq!(
        json(consistency::min_latency()),
        r#"{"strategy":"min_latency"}"#
    );
    assert_eq!(
        json(consistency::at_least("GhUKEzE2".into())),
        r#"{"strategy":"at_least","token":"GhUKEzE2"}"#
    );
    assert_eq!(
        json(consistency::snapshot("GhUKEzE2".into())),
        r#"{"strategy":"snapshot","token":"GhUKEzE2"}"#
    );
}
