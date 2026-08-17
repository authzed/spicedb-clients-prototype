//! Idiomatic Rust client for [SpiceDB](https://authzed.com/spicedb).
//!
//! This crate provides a high-level, Rust-native API for SpiceDB. It wraps the
//! generated proto client (`spicedb-proto`) and exposes idiomatic types,
//! builders, and async methods.
//!
//! # Quick Start
//!
//! ```rust,no_run
//! use spicedb::client::SpiceDBClient;
//! use spicedb::consistency;
//! use spicedb::types::{Relationship, Transaction, Filter};
//!
//! # async fn example() -> Result<(), Box<dyn std::error::Error>> {
//! // Create a client (plaintext for testing)
//! let client = SpiceDBClient::new_plaintext("localhost:50051", "testtoken").await?;
//!
//! // Write a schema
//! let revision = client.write_schema("definition user {}
//! definition document {
//!     relation viewer: user
//!     permission view = viewer
//! }").await?;
//!
//! // Write a relationship
//! let rel = Relationship::new(
//!     "document", "doc1", "viewer",
//!     "user", "alice", "",
//! )?;
//! let mut txn = Transaction::new();
//! txn.touch(&rel);
//! let revision = client.write(&txn).await?;
//!
//! // Check a permission
//! let cs = consistency::at_least(&revision);
//! let result = client.check_permission(&cs, "view", &rel).await?;
//! assert!(result.has_permission());
//! # Ok(())
//! # }
//! ```
//!
//! # Modules
//!
//! - [`client`] — The [`SpiceDBClient`](client::SpiceDBClient) struct and all
//!   operations.
//! - [`consistency`] — Consistency strategy constructors for read operations.
//! - [`types`] — Relationship, Filter, Transaction, and schema reflection types.
//! - [`error`] — The [`SpiceDBError`](error::SpiceDBError) enum.
//!
//! # Design Principles
//!
//! - **No protobuf in the public API** — all types are native Rust structs/enums.
//! - **Explicit consistency** — every read takes a [`consistency::Strategy`].
//! - **Transparent pagination** — streaming methods handle cursors internally.
//! - **Automatic retry** — transient gRPC errors use exponential backoff.
//! - **`#[must_use]`** — check results cannot be silently ignored.

pub mod client;
pub mod consistency;
pub mod error;
pub mod types;
