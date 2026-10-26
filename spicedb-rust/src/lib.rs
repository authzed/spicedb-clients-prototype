//! Native asynchronous SpiceDB client. See DESIGN.md for operation contracts.
pub mod client;
pub mod consistency;
pub mod error;
pub mod types;
pub use client::{SpiceDBClient, SpiceDBClientBuilder};
pub use error::SpiceDBError;
/// Authenticated protocol escape hatch; raw calls have caller-owned policies.
pub use spicedb_proto;
pub use types::*;
