//! Regression coverage for Task 6: `import_relationships` used to require a
//! materialized `Vec<Relationship>`, forcing a caller streaming in a large
//! import from an iterator/generator (a lazy computation, a DB cursor) to
//! collect the whole thing into memory first just to call this method.
//!
//! Runs a real in-process gRPC server (`support::MockPermissionsService`) so
//! `num_loaded` reflects relationships the server actually received across
//! the real client-streaming call, not a mocked return value.

mod support;

use spicedb::client::SpiceDBClient;
use spicedb::types::Relationship;

use support::{spawn_permissions_server, MockPermissionsService};

fn rel(id: &str) -> Relationship {
    Relationship::new("document", id, "viewer", "user", "alice", "").expect("valid relationship")
}

#[tokio::test]
async fn import_relationships_accepts_a_plain_iterator_not_just_a_vec() {
    let mock = MockPermissionsService::new();
    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::builder(addr.to_string(), "token")
        .plaintext()
        .build()
        .await
        .expect("client should connect to mock server");

    // A plain iterator (not a Vec) -- proves the signature genuinely accepts
    // `impl IntoIterator`, not just something that happens to coerce to one.
    let ids = ["doc1", "doc2", "doc3", "doc4", "doc5"];
    let relationships = ids.into_iter().map(rel);

    let num_loaded = client
        .import_relationships(relationships)
        .await
        .expect("import should succeed");

    assert_eq!(
        num_loaded, 5,
        "the real server must see all 5 relationships from the iterator"
    );
}

#[tokio::test]
async fn import_relationships_accepts_a_one_shot_generator_without_materializing_it() {
    let mock = MockPermissionsService::new();
    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::builder(addr.to_string(), "token")
        .plaintext()
        .build()
        .await
        .expect("client should connect to mock server");

    // A struct-backed Iterator that panics if driven past its declared
    // length, standing in for a one-shot source (a DB cursor, a lazily
    // computed sequence) that cannot be rewound or iterated twice. If
    // import_relationships (or anything it calls) collected this into a Vec
    // up front by calling more than `remaining` times, or somehow drove it
    // twice, this would panic.
    struct OneShot {
        remaining: usize,
        next_id: usize,
    }

    impl Iterator for OneShot {
        type Item = Relationship;

        fn next(&mut self) -> Option<Relationship> {
            if self.remaining == 0 {
                return None;
            }
            self.remaining -= 1;
            let id = format!("doc{}", self.next_id);
            self.next_id += 1;
            Some(rel(&id))
        }
    }

    let source = OneShot {
        remaining: 2500, // several batches (DEFAULT_IMPORT_BATCH_SIZE == 1,000)
        next_id: 0,
    };

    let num_loaded = client
        .import_relationships(source)
        .await
        .expect("import should succeed");

    assert_eq!(
        num_loaded, 2500,
        "the real server must see every relationship from the one-shot source, across multiple batches"
    );
}
