//! The escape hatch reaches a real stub and makes a real call.
//!
//! `SpiceDBClient::raw_proto()` exists so a request the idiomatic surface
//! cannot express has a workaround short of forking the crate -- root
//! DESIGN.md, "What NOT To Do", permits exactly this as "clearly marked
//! secondary API". Asserting that the accessor compiles, or that the returned
//! value has a `permissions` field, would prove none of that. What matters is
//! whether a caller can drive a generated tonic client through it and get an
//! answer out of a real server, with this client's bearer token attached.
//!
//! So these tests stand up a real in-process tonic server (the shared
//! `support` harness) and drive `CheckPermission` -- the single-check RPC the
//! idiomatic client never calls, since every check it makes goes through
//! `CheckBulkPermissions`. That makes it a genuine gap rather than a
//! contrived one.

mod support;

use spicedb::client::SpiceDBClient;
use spicedb::spicedb_proto::authzed::api::v1 as proto;
use support::{spawn_permissions_server, MockPermissionsService};

fn check_request() -> proto::CheckPermissionRequest {
    proto::CheckPermissionRequest {
        consistency: Some(proto::Consistency {
            requirement: Some(proto::consistency::Requirement::FullyConsistent(true)),
        }),
        resource: Some(proto::ObjectReference {
            object_type: "document".to_string(),
            object_id: "readme".to_string(),
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
    }
}

#[tokio::test]
async fn raw_proto_drives_a_real_generated_client_against_a_real_server() {
    let mock = MockPermissionsService::new();
    let requests = mock.check_permission_requests();
    let authorizations = mock.check_permission_authorizations();
    let addr = spawn_permissions_server(mock).await;

    let client = SpiceDBClient::new_plaintext(addr.to_string(), "test-token")
        .await
        .expect("client should connect to the mock server");

    // Clone, because the generated clients take `&mut self`. A tonic client
    // clone shares the same channel -- it does not open a second connection.
    let mut permissions = client.raw_proto().permissions.clone();
    let response = permissions
        .check_permission(check_request())
        .await
        .expect("raw CheckPermission should succeed")
        .into_inner();

    assert_eq!(
        response.permissionship,
        proto::check_permission_response::Permissionship::HasPermission as i32,
        "the raw call must return the server's real answer"
    );
    assert_eq!(
        response.checked_at.expect("a revision").token,
        "rev-raw",
        "the response must be the one this server produced"
    );

    let received = requests.lock().unwrap();
    assert_eq!(received.len(), 1, "the RPC must have reached the server");
    assert_eq!(received[0].permission, "view");

    // The bearer token rides this client's interceptor, so a raw call is
    // authenticated exactly as an idiomatic one is -- nothing extra to pass.
    assert_eq!(
        authorizations.lock().unwrap().as_slice(),
        &["Bearer test-token".to_string()],
    );
}

#[tokio::test]
async fn raw_proto_shares_the_connection_the_idiomatic_methods_use() {
    let mock = MockPermissionsService::new();
    mock.push_check_bulk_permissions_response(proto::CheckBulkPermissionsResponse {
        pairs: vec![proto::CheckBulkPermissionsPair {
            request: None,
            response: Some(proto::check_bulk_permissions_pair::Response::Item(
                proto::CheckBulkPermissionsResponseItem {
                    permissionship: proto::check_permission_response::Permissionship::HasPermission
                        as i32,
                    partial_caveat_info: None,
                    debug_trace: None,
                },
            )),
        }],
        checked_at: Some(proto::ZedToken {
            token: "rev-bulk".to_string(),
        }),
    });
    let bulk_calls = mock.check_bulk_permissions_calls();
    let raw_calls = mock.check_permission_requests();
    let addr = spawn_permissions_server(mock).await;

    let client = SpiceDBClient::new_plaintext(addr.to_string(), "test-token")
        .await
        .expect("client should connect to the mock server");

    // An idiomatic call and a raw one, in that order, over one client.
    let rel = spicedb::types::Relationship::new("document", "readme", "view", "user", "jimmy", "")
        .expect("valid relationship");
    let result = client
        .check_permission(&spicedb::consistency::full(), "view", &rel)
        .await
        .expect("idiomatic check should succeed");
    assert!(result.has_permission());

    let mut permissions = client.raw_proto().permissions.clone();
    permissions
        .check_permission(check_request())
        .await
        .expect("raw CheckPermission should succeed");

    assert_eq!(
        bulk_calls.load(std::sync::atomic::Ordering::SeqCst),
        1,
        "the idiomatic call must have gone through CheckBulkPermissions"
    );
    assert_eq!(
        raw_calls.lock().unwrap().len(),
        1,
        "the raw call must have gone through the single-check RPC the idiomatic API never uses"
    );
}

/// The hatch must never grow into a way to build a connection.
///
/// Root DESIGN.md, "RULE: Credentials over insecure transport require an
/// explicit opt-in", is enforced in `SpiceDBClientBuilder::build`, on the
/// single path that creates a channel. Handing back an already-built client
/// cannot bypass that; accepting an endpoint, token, or transport setting here
/// would -- it would be a second construction path with no guard on it.
#[tokio::test]
async fn raw_proto_is_an_accessor_not_a_second_construction_path() {
    // `raw_proto` takes only `&self`: this would not compile if it took
    // connection parameters, which is the shape that makes a bypass possible.
    let accessor: fn(&SpiceDBClient) -> &spicedb::spicedb_proto::SpiceDBProtoClient =
        SpiceDBClient::raw_proto;
    let _ = accessor;

    // And the guard still refuses what it always did, so nothing has moved.
    let err = match SpiceDBClient::builder("evil.example.com:50051", "token")
        .plaintext()
        .build()
        .await
    {
        Ok(_) => panic!("plaintext to a non-loopback host must still be refused"),
        Err(err) => err,
    };
    assert!(
        err.to_string()
            .contains("allow_insecure_remote_credentials"),
        "unexpected error: {err}"
    );
}
