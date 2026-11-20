//! `lookup_resources` asks the server for batched responses by default and
//! still yields one resource at a time. A batch has one cursor (after its last
//! result), so only the last item yielded from a batch carries one, and
//! paging, limits and the cursor-advance check count results, not messages.

mod support;

use std::num::NonZeroU32;
use std::sync::{Arc, Mutex};

use futures::StreamExt;
use spicedb::client::SpiceDBClient;
use spicedb::{
    consistency, LookupBatching, LookupPermissionshipFilter, LookupResource,
    LookupResourcesOptions, LookupResourcesRequest, Paging, Permissionship, SpiceDBError,
};
use spicedb_proto::authzed::api::v1 as proto;

use support::{spawn_permissions_server, MockPermissionsService};

const HAS: i32 = proto::LookupPermissionship::HasPermission as i32;
const CONDITIONAL: i32 = proto::LookupPermissionship::ConditionalPermission as i32;

fn cursor(token: &str) -> proto::Cursor {
    proto::Cursor {
        token: token.into(),
    }
}

fn result(id: &str, permissionship: i32) -> proto::LookupResourcesResult {
    proto::LookupResourcesResult {
        resource_object_id: id.into(),
        permissionship,
        partial_caveat_info: None,
    }
}

/// A batched response: results, the cursor after the last of them, and the
/// top-level revision.
fn batch(
    results: Vec<proto::LookupResourcesResult>,
    after: &str,
) -> proto::LookupResourcesResponse {
    proto::LookupResourcesResponse {
        looked_up_at: Some(proto::ZedToken {
            token: "rev-1".into(),
        }),
        batched: Some(proto::LookupResourcesBatch {
            results,
            after_cursor: Some(cursor(after)),
        }),
        ..Default::default()
    }
}

fn granted(ids: &[&str], after: &str) -> proto::LookupResourcesResponse {
    batch(ids.iter().map(|id| result(id, HAS)).collect(), after)
}

/// A one-resource response, as servers without batching send.
fn singular(id: &str, after: &str) -> proto::LookupResourcesResponse {
    proto::LookupResourcesResponse {
        looked_up_at: Some(proto::ZedToken {
            token: "rev-1".into(),
        }),
        resource_object_id: id.into(),
        permissionship: HAS,
        after_result_cursor: Some(cursor(after)),
        ..Default::default()
    }
}

struct Server {
    client: SpiceDBClient,
    requests: Arc<Mutex<Vec<proto::LookupResourcesRequest>>>,
}

impl Server {
    async fn start(pages: Vec<Vec<proto::LookupResourcesResponse>>) -> Self {
        let mock = MockPermissionsService::new();
        for page in pages {
            mock.push_lookup_resources_page(page);
        }
        let requests = mock.lookup_resources_requests();
        let addr = spawn_permissions_server(mock).await;
        let client = SpiceDBClient::new_plaintext(
            addr.to_string(),
            "token",
            spicedb::RemoteCredentialsPolicy::LoopbackOnly,
        )
        .await
        .expect("client should connect to mock server");
        Self { client, requests }
    }

    async fn lookup(
        &self,
        options: LookupResourcesOptions,
    ) -> Vec<Result<LookupResource, SpiceDBError>> {
        let request = LookupResourcesRequest::new(
            "document",
            "view",
            spicedb::SubjectRef::direct(spicedb::ObjectRef::new("user", "alice").unwrap()),
            consistency::full(),
        )
        .with_options(options);
        self.client.lookup_resources(request).collect().await
    }

    async fn lookup_ok(&self, options: LookupResourcesOptions) -> Vec<LookupResource> {
        self.lookup(options)
            .await
            .into_iter()
            .map(|r| r.expect("lookup result"))
            .collect()
    }

    fn batch_sizes(&self) -> Vec<u32> {
        self.requests
            .lock()
            .unwrap()
            .iter()
            .map(|r| r.optional_response_batch_size)
            .collect()
    }

    fn limits(&self) -> Vec<u32> {
        self.requests
            .lock()
            .unwrap()
            .iter()
            .map(|r| r.optional_limit)
            .collect()
    }

    fn cursors(&self) -> Vec<Option<String>> {
        self.requests
            .lock()
            .unwrap()
            .iter()
            .map(|r| r.optional_cursor.as_ref().map(|c| c.token.clone()))
            .collect()
    }
}

fn nz(n: u32) -> NonZeroU32 {
    NonZeroU32::new(n).unwrap()
}

fn ids(items: &[LookupResource]) -> Vec<&str> {
    items.iter().map(|r| r.resource_id.as_str()).collect()
}

fn item_cursors(items: &[LookupResource]) -> Vec<Option<&str>> {
    items
        .iter()
        .map(|r| r.after_result_cursor.as_deref())
        .collect()
}

/// The batch size one call with these options requests.
async fn requested(options: LookupResourcesOptions) -> u32 {
    let server = Server::start(vec![]).await;
    assert!(server.lookup_ok(options).await.is_empty());
    let sizes = server.batch_sizes();
    assert_eq!(sizes.len(), 1);
    sizes[0]
}

#[tokio::test]
async fn the_request_carries_the_default_explicit_or_no_batch_size() {
    let options = LookupResourcesOptions::new;
    // Default: the page size, capped at 1,000.
    assert_eq!(requested(options()).await, 512);
    assert_eq!(requested(options().with_page_size(nz(50))).await, 50);
    assert_eq!(requested(options().with_page_size(nz(5000))).await, 1000);
    // A single request: the limit capped at 1,000, or 1,000 without one.
    let single = || options().with_paging(Paging::SingleRequest);
    assert_eq!(requested(single()).await, 1000);
    assert_eq!(requested(single().with_limit(20)).await, 20);
    assert_eq!(requested(single().with_limit(5000)).await, 1000);
    // An explicit size is sent as given: the server, not the client,
    // rejects one above its maximum.
    assert_eq!(
        requested(options().with_response_batch_size(nz(7))).await,
        7
    );
    assert_eq!(
        requested(options().with_response_batch_size(nz(4000))).await,
        4000
    );
    // Disabled sends none, whatever size is configured.
    assert_eq!(
        requested(options().with_batching(LookupBatching::Disabled)).await,
        0
    );
    assert_eq!(
        requested(
            options()
                .with_response_batch_size(nz(7))
                .with_batching(LookupBatching::Disabled)
        )
        .await,
        0
    );
}

#[tokio::test]
async fn a_batched_stream_yields_every_result_in_order_with_batch_end_cursors() {
    let server = Server::start(vec![vec![
        batch(
            vec![
                result("a", HAS),
                proto::LookupResourcesResult {
                    resource_object_id: "b".into(),
                    permissionship: CONDITIONAL,
                    partial_caveat_info: Some(proto::PartialCaveatInfo {
                        missing_required_context: vec!["day".into()],
                    }),
                },
                result("c", 99),
            ],
            "after-c",
        ),
        // An empty batch yields nothing and is not an error.
        proto::LookupResourcesResponse {
            batched: Some(proto::LookupResourcesBatch::default()),
            ..Default::default()
        },
        granted(&["d", "e"], "after-e"),
    ]])
    .await;
    let items = server.lookup_ok(LookupResourcesOptions::new()).await;
    assert_eq!(ids(&items), ["a", "b", "c", "d", "e"]);
    assert_eq!(
        item_cursors(&items),
        [None, None, Some("after-c"), None, Some("after-e")]
    );
    assert!(items.iter().all(|r| r.looked_up_at.as_ref() == "rev-1"));
    assert_eq!(items[0].permissionship, Permissionship::HasPermission);
    assert_eq!(
        items[1].permissionship,
        Permissionship::ConditionalPermission
    );
    assert_eq!(
        items[1]
            .partial_caveat
            .as_ref()
            .unwrap()
            .missing_required_context,
        ["day"]
    );
    assert!(items[0].partial_caveat.is_none());
    // A value this client does not know is the safe unknown, never a grant.
    assert_ne!(items[2].permissionship, Permissionship::HasPermission);
    assert_ne!(
        items[2].permissionship,
        Permissionship::ConditionalPermission
    );
    // Five results are fewer than a page, so no second page is requested.
    assert_eq!(server.cursors(), [None]);
}

#[tokio::test]
async fn paging_over_batches_counts_results_and_follows_the_batch_cursor() {
    let server = Server::start(vec![
        // A full page of four results in two messages.
        vec![granted(&["a", "b", "c"], "3"), granted(&["d"], "4")],
        // A short page (three results) ends the read.
        vec![granted(&["e", "f", "g"], "7")],
    ])
    .await;
    let items = server
        .lookup_ok(
            LookupResourcesOptions::new()
                .with_page_size(nz(4))
                .with_response_batch_size(nz(3)),
        )
        .await;
    assert_eq!(ids(&items), ["a", "b", "c", "d", "e", "f", "g"]);
    assert_eq!(
        item_cursors(&items),
        [None, None, Some("3"), Some("4"), None, None, Some("7")]
    );
    assert_eq!(server.cursors(), [None, Some("4".into())]);
    assert_eq!(server.limits(), [4, 4]);
    assert_eq!(server.batch_sizes(), [3, 3]);
}

#[tokio::test]
async fn a_full_batched_page_whose_cursor_does_not_advance_fails() {
    let server = Server::start(vec![
        vec![granted(&["a", "b"], "stuck")],
        vec![granted(&["a", "b"], "stuck")],
    ])
    .await;
    let results = server
        .lookup(LookupResourcesOptions::new().with_page_size(nz(2)))
        .await;
    assert_eq!(results.len(), 5);
    let err = results[4].as_ref().unwrap_err();
    assert_eq!(err.code(), tonic::Code::Internal, "{err}");
    assert_eq!(server.cursors().len(), 2);
}

#[tokio::test]
async fn singular_responses_to_a_batched_request_keep_per_item_cursors() {
    let server = Server::start(vec![
        vec![singular("a", "1"), singular("b", "2")],
        vec![singular("c", "3")],
    ])
    .await;
    let items = server
        .lookup_ok(LookupResourcesOptions::new().with_page_size(nz(2)))
        .await;
    assert_eq!(ids(&items), ["a", "b", "c"]);
    assert_eq!(item_cursors(&items), [Some("1"), Some("2"), Some("3")]);
    assert!(items.iter().all(|r| r.looked_up_at.as_ref() == "rev-1"));
    assert_eq!(server.batch_sizes(), [2, 2]);
    assert_eq!(server.cursors(), [None, Some("2".into())]);
}

#[tokio::test]
async fn the_filter_moves_a_batch_cursor_to_the_last_kept_item() {
    let server = Server::start(vec![
        vec![
            // Trailing results dropped: "b" is the last kept.
            batch(
                vec![
                    result("a", HAS),
                    result("b", HAS),
                    result("c", CONDITIONAL),
                    result("d", CONDITIONAL),
                ],
                "4",
            ),
            // Wholly dropped: nothing carries its cursor.
            batch(vec![result("e", CONDITIONAL), result("f", 99)], "6"),
        ],
        vec![batch(vec![result("g", CONDITIONAL), result("h", HAS)], "8")],
    ])
    .await;
    let items = server
        .lookup_ok(
            LookupResourcesOptions::new()
                .with_page_size(nz(6))
                .with_permissionship_filter(LookupPermissionshipFilter::HasPermissionOnly),
        )
        .await;
    assert_eq!(ids(&items), ["a", "b", "h"]);
    assert_eq!(item_cursors(&items), [None, Some("4"), Some("8")]);
    // The paging cursor still advances past the dropped batch.
    assert_eq!(server.cursors(), [None, Some("6".into())]);
}

#[tokio::test]
async fn a_limited_call_ends_on_a_cursor_by_shrinking_the_last_pages_batch() {
    // Limit 6, page size 4, batch size 3: page two has 2 left, fewer than a
    // batch, so it asks for batches of 2. The server still fills the page.
    let server = Server::start(vec![
        vec![granted(&["a", "b", "c"], "3"), granted(&["d"], "4")],
        vec![granted(&["e", "f"], "6"), granted(&["g", "h"], "8")],
    ])
    .await;
    let items = server
        .lookup_ok(
            LookupResourcesOptions::new()
                .with_page_size(nz(4))
                .with_response_batch_size(nz(3))
                .with_limit(6),
        )
        .await;
    assert_eq!(ids(&items), ["a", "b", "c", "d", "e", "f"]);
    assert_eq!(items[5].after_result_cursor.as_deref(), Some("6"));
    assert_eq!(server.batch_sizes(), [3, 2]);
    // The wire limit is the page size on every page.
    assert_eq!(server.limits(), [4, 4]);
    assert_eq!(server.cursors(), [None, Some("4".into())]);
}

#[tokio::test]
async fn a_limit_that_is_a_multiple_of_the_batch_size_keeps_it() {
    // Limit 10, page size 8, batch size 2: page two has 2 left, one batch.
    let server = Server::start(vec![
        vec![
            granted(&["a", "b"], "2"),
            granted(&["c", "d"], "4"),
            granted(&["e", "f"], "6"),
            granted(&["g", "h"], "8"),
        ],
        vec![granted(&["i", "j"], "10"), granted(&["k", "l"], "12")],
    ])
    .await;
    let items = server
        .lookup_ok(
            LookupResourcesOptions::new()
                .with_page_size(nz(8))
                .with_response_batch_size(nz(2))
                .with_limit(10),
        )
        .await;
    assert_eq!(items.len(), 10);
    assert_eq!(items[9].after_result_cursor.as_deref(), Some("10"));
    assert_eq!(server.batch_sizes(), [2, 2]);
}

#[tokio::test]
async fn a_limit_that_would_fall_mid_batch_turns_batching_off_for_that_page() {
    // Limit 13, page size 8, batch size 3: page two has 5 left, neither
    // fewer than a batch nor a multiple of one.
    let server = Server::start(vec![
        vec![
            granted(&["a", "b", "c"], "3"),
            granted(&["d", "e", "f"], "6"),
            granted(&["g", "h"], "8"),
        ],
        ["i", "j", "k", "l", "m", "n", "o", "p"]
            .iter()
            .enumerate()
            .map(|(i, id)| singular(id, &(9 + i).to_string()))
            .collect(),
    ])
    .await;
    let items = server
        .lookup_ok(
            LookupResourcesOptions::new()
                .with_page_size(nz(8))
                .with_response_batch_size(nz(3))
                .with_limit(13),
        )
        .await;
    assert_eq!(items.len(), 13);
    assert_eq!(items[12].resource_id, "m");
    assert_eq!(items[12].after_result_cursor.as_deref(), Some("13"));
    assert_eq!(server.batch_sizes(), [3, 0]);
}

#[tokio::test]
async fn a_limit_with_the_unconditional_only_filter_does_not_batch() {
    let server = Server::start(vec![vec![singular("a", "1"), singular("b", "2")]]).await;
    let items = server
        .lookup_ok(
            LookupResourcesOptions::new()
                .with_limit(2)
                .with_permissionship_filter(LookupPermissionshipFilter::HasPermissionOnly),
        )
        .await;
    assert_eq!(item_cursors(&items), [Some("1"), Some("2")]);
    assert_eq!(server.batch_sizes(), [0]);
    // Without a limit the filter does not affect batching.
    let server = Server::start(vec![]).await;
    server
        .lookup_ok(
            LookupResourcesOptions::new()
                .with_permissionship_filter(LookupPermissionshipFilter::HasPermissionOnly),
        )
        .await;
    assert_eq!(server.batch_sizes(), [512]);
}

#[tokio::test]
async fn a_single_request_with_a_limit_ends_on_the_servers_last_batch() {
    // The server stops at the limit and flushes its final partial batch.
    let server = Server::start(vec![vec![
        granted(&["a", "b", "c"], "3"),
        granted(&["d", "e"], "5"),
    ]])
    .await;
    let items = server
        .lookup_ok(
            LookupResourcesOptions::new()
                .with_paging(Paging::SingleRequest)
                .with_response_batch_size(nz(3))
                .with_limit(5),
        )
        .await;
    assert_eq!(items.len(), 5);
    assert_eq!(items[4].after_result_cursor.as_deref(), Some("5"));
    assert_eq!(server.batch_sizes(), [3]);
    assert_eq!(server.limits(), [5]);
}

#[tokio::test]
async fn a_malformed_batch_result_fails_as_a_malformed_response() {
    for broken in [result("", HAS), result("b", 0)] {
        let server = Server::start(vec![vec![batch(
            vec![result("a", HAS), broken.clone(), result("c", HAS)],
            "3",
        )]])
        .await;
        let results = server.lookup(LookupResourcesOptions::new()).await;
        assert_eq!(results.len(), 2, "{broken:?}");
        assert_eq!(results[0].as_ref().unwrap().resource_id, "a");
        let err = results[1].as_ref().unwrap_err();
        assert_eq!(err.code(), tonic::Code::Internal, "{err}");
        assert!(err.is_client_detected(), "{err:?}");
    }
}
