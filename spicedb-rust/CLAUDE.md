# spicedb-rust

This is the idiomatic Rust client for SpiceDB.

## What to do

1. Read `../DESIGN.md` (root) for overall vision and backwards compat rules
2. Read `./DESIGN.md` for Rust-specific goals -- this takes precedence
3. When updating after a proto change: check the proto client diff to understand
   what changed
4. NEVER remove or rename public API methods/types -- add new methods instead
5. Propagate deprecation using `#[deprecated(note = "Use XYZ instead")]`
6. Update `examples/` to cover new functionality
7. Never delete an example -- mark deprecated ones with a note instead
8. Append to the Changelog section of DESIGN.md after making changes
9. Run `cargo test` and `cargo clippy` after making changes
10. All public types should derive `Debug, Clone, PartialEq, Eq` where possible
11. Transaction methods take `&Relationship` (borrow, not move)
12. Check results must be marked `#[must_use]`
