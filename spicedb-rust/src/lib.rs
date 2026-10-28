//! Native asynchronous SpiceDB client. See DESIGN.md for operation contracts.
pub mod client;
pub mod consistency;
pub mod error;
pub mod options;
pub mod protoconv;
#[cfg(feature = "testing")]
pub mod testing;
pub mod types;
pub use client::{SpiceDBChannelBuilder, SpiceDBClient, SpiceDBClientBuilder};
pub use error::SpiceDBError;
pub use options::{
    BulkItemErrors, CallOptions, DefaultDeadline, DeletePartial, ResponseMetadata, RetryEvent,
    RetryMode, RetryPolicy, StreamRetryMode,
};
/// Authenticated protocol escape hatch; raw calls have caller-owned policies.
pub use spicedb_proto;
/// The tonic version this crate is built on, for `tonic::Code`, metadata and channels.
pub use tonic;
pub use types::*;
