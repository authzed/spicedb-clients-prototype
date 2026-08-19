//! Demonstrates reaching past the idiomatic API with `raw_proto()`.
//!
//! Every wrapper eventually meets a request the wrapper does not express. This
//! crate's answer is `raw_proto()`: the underlying `SpiceDBProtoClient`, with
//! the four generated tonic clients it makes its own calls through -- a
//! workaround short of forking the crate. Root DESIGN.md, "What NOT To Do",
//! allows exactly this as "clearly marked secondary API".
//!
//! The gaps demonstrated here are real, not hypothetical:
//!
//! 1. `WriteRelationshipsRequest::optional_transaction_metadata` is a proto
//!    field this client does not surface anywhere. Applications use it to stamp
//!    an audit correlation ID onto a write, which comes back out of the Watch
//!    stream.
//! 2. `CheckPermission` -- the single-check RPC. The idiomatic
//!    `check_permission()` routes every check through `CheckBulkPermissions`,
//!    so the raw client is how you drive the unary RPC itself.
//!
//! What you give up on the raw path, and why the idiomatic methods stay the
//! default: no `SpiceDBError` mapping (you handle `tonic::Status`), no retry on
//! a transient failure, and no `default_timeout` -- set a deadline yourself or
//! the call is unbounded.
//!
//! Run with: `cargo run --example raw_escape_hatch`

use std::collections::BTreeMap;
use std::time::Duration;

use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::error::SpiceDBError;
use spicedb::spicedb_proto::authzed::api::v1 as proto;
use spicedb::types::{Filter, Relationship};

const SCHEMA: &str = r#"definition user {}

definition document {
    relation viewer: user
    permission view = viewer
}"#;

#[tokio::main]
async fn main() {
    // Endpoint and token come from the environment so the example runs against
    // whichever SpiceDB the caller started; the defaults match
    // docker-compose.test.yml.
    let endpoint =
        std::env::var("SPICEDB_ENDPOINT").unwrap_or_else(|_| "localhost:50051".to_string());
    let token = std::env::var("SPICEDB_TOKEN").unwrap_or_else(|_| "testtoken".to_string());

    let client = SpiceDBClient::new_plaintext(endpoint, token)
        .await
        .expect("failed to create client");

    // Clear before writing the schema, not after using it. Every example runs
    // against one SpiceDB and writes a whole schema, and SpiceDB refuses a
    // WriteSchema that drops a relation while a relationship still exists
    // under it -- so what a *previous* example left behind is this example's
    // problem, and a cleanup at exit does not help if this example fails
    // first.
    //
    // Exactly one error is tolerated: on a fresh server there is no `document`
    // definition yet, which SpiceDB reports as FailedPrecondition
    // (ERROR_REASON_UNKNOWN_DEFINITION). Anything else -- an unreachable
    // server, a bad token -- must still fail the example.
    match client.delete_relationships(&Filter::new("document")).await {
        Ok(_) | Err(SpiceDBError::FailedPrecondition(_)) => {}
        Err(e) => panic!("cleanup before schema write failed: {e:?}"),
    }

    client
        .write_schema(SCHEMA)
        .await
        .expect("write schema failed");

    // ── 1. A proto field the idiomatic API does not expose ───────────────
    //
    // Clone the generated client: it takes `&mut self`, and a tonic clone
    // shares the same channel rather than opening a second connection. The
    // bearer token rides this client's interceptor, so there is nothing extra
    // to attach.
    let mut permissions = client.raw_proto().permissions.clone();

    let mut metadata = BTreeMap::new();
    metadata.insert(
        "correlation_id".to_string(),
        prost_types::Value {
            kind: Some(prost_types::value::Kind::StringValue(
                "example-42".to_string(),
            )),
        },
    );

    let written = permissions
        .write_relationships(proto::WriteRelationshipsRequest {
            updates: vec![proto::RelationshipUpdate {
                operation: proto::relationship_update::Operation::Touch as i32,
                relationship: Some(proto::Relationship {
                    resource: Some(proto::ObjectReference {
                        object_type: "document".to_string(),
                        object_id: "ledger".to_string(),
                    }),
                    relation: "viewer".to_string(),
                    subject: Some(proto::SubjectReference {
                        object: Some(proto::ObjectReference {
                            object_type: "user".to_string(),
                            object_id: "jimmy".to_string(),
                        }),
                        optional_relation: String::new(),
                    }),
                    optional_caveat: None,
                    optional_expires_at: None,
                }),
            }],
            optional_preconditions: vec![],
            optional_transaction_metadata: Some(prost_types::Struct { fields: metadata }),
        })
        .await
        .expect("raw write failed")
        .into_inner();

    let revision = written.written_at.expect("a revision").token;
    println!("raw write committed at revision {revision}");

    // The idiomatic API picks up right where the raw call left off -- same
    // client, same connection, including read-your-writes on the raw revision.
    let rel = Relationship::new("document", "ledger", "view", "user", "jimmy", "")
        .expect("invalid relationship");
    let result = client
        .check_permission(&consistency::at_least(&revision), "view", &rel)
        .await
        .expect("idiomatic check failed");
    println!(
        "user:jimmy can view document:ledger: {}",
        result.has_permission()
    );
    assert!(result.has_permission());

    // ── 2. An RPC the idiomatic API routes around ────────────────────────
    //
    // A raw call gets no client default deadline -- set one yourself.
    let mut request = tonic::Request::new(proto::CheckPermissionRequest {
        consistency: Some(proto::Consistency {
            requirement: Some(proto::consistency::Requirement::FullyConsistent(true)),
        }),
        resource: Some(proto::ObjectReference {
            object_type: "document".to_string(),
            object_id: "ledger".to_string(),
        }),
        permission: "view".to_string(),
        subject: Some(proto::SubjectReference {
            object: Some(proto::ObjectReference {
                object_type: "user".to_string(),
                object_id: "jimmy".to_string(),
            }),
            optional_relation: String::new(),
        }),
        context: None,
        with_tracing: false,
    });
    request.set_timeout(Duration::from_secs(30));

    let single = permissions
        .check_permission(request)
        .await
        .expect("raw CheckPermission failed")
        .into_inner();
    println!(
        "raw CheckPermission permissionship: {}",
        single.permissionship
    );
    assert_eq!(
        single.permissionship,
        proto::check_permission_response::Permissionship::HasPermission as i32
    );

    // Clean up so a later example isn't blocked by leftover relationships.
    client
        .delete_relationships(&Filter::new("document").with_resource_id("ledger"))
        .await
        .expect("cleanup failed");

    // The connection belongs to `client`: it is released when `client` drops,
    // and the clone above must not outlive it.
    println!("done");
}
