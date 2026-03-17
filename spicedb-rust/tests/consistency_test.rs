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
    let s = consistency::at_least("token-123");
    assert_eq!(s, Strategy::AtLeast("token-123".to_string()));
}

#[test]
fn test_at_least_with_owned_string() {
    let s = consistency::at_least(String::from("token-456"));
    assert_eq!(s, Strategy::AtLeast("token-456".to_string()));
}

#[test]
fn test_snapshot() {
    let s = consistency::snapshot("snap-789");
    assert_eq!(s, Strategy::Snapshot("snap-789".to_string()));
}

#[test]
fn test_at_least_or_full_with_some() {
    let s = consistency::at_least_or_full(Some("token-abc"));
    assert_eq!(s, Strategy::AtLeast("token-abc".to_string()));
}

#[test]
fn test_at_least_or_full_with_none() {
    let s = consistency::at_least_or_full(None::<&str>);
    assert_eq!(s, Strategy::Full);
}

#[test]
fn test_at_least_or_min_latency_with_some() {
    let s = consistency::at_least_or_min_latency(Some("token-def"));
    assert_eq!(s, Strategy::AtLeast("token-def".to_string()));
}

#[test]
fn test_at_least_or_min_latency_with_none() {
    let s = consistency::at_least_or_min_latency(None::<&str>);
    assert_eq!(s, Strategy::MinLatency);
}

#[test]
fn test_strategy_clone_and_eq() {
    let s = consistency::at_least("token");
    let s2 = s.clone();
    assert_eq!(s, s2);
}

#[test]
fn test_strategy_debug_formatting() {
    assert_eq!(format!("{:?}", consistency::full()), "Full");
    assert_eq!(format!("{:?}", consistency::min_latency()), "MinLatency");
    assert!(format!("{:?}", consistency::at_least("t")).contains("AtLeast"));
    assert!(format!("{:?}", consistency::snapshot("t")).contains("Snapshot"));
}
