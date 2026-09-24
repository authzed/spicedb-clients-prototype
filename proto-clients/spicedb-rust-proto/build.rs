use std::path::Path;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let proto_dir = Path::new("proto");

    if !proto_dir.exists() {
        println!(
            "cargo:warning=proto/ directory not found. Run `buf export buf.build/authzed/api -o proto` first. Skipping code generation."
        );
        return Ok(());
    }

    let proto_files: Vec<_> = glob::glob("proto/**/*.proto")?
        .filter_map(|entry| entry.ok())
        .collect();

    if proto_files.is_empty() {
        println!("cargo:warning=No .proto files found under proto/. Skipping code generation.");
        return Ok(());
    }

    // `buf export` writes the authzed protos and their non-well-known deps into
    // proto/, but deliberately omits the google/protobuf well-known types
    // (Timestamp, Struct, Duration, ...) -- buf treats them as built into its
    // image, so they never land on disk. protoc still has to *parse* them to
    // resolve `import "google/protobuf/..."` (prost maps them to prost-types
    // rather than generating code, but the descriptors must resolve), so it
    // needs the .proto files from somewhere.
    //
    // Relying on a system protoc to supply them from its ambient include path
    // is not portable: CI installs protoc via `apt install protobuf-compiler`,
    // which ships only the binary, not the well-known-type .proto files (those
    // live in libprotobuf-dev). When the runner image stopped providing them,
    // every regen that touched a proto file failed with
    // "google/protobuf/descriptor.proto: File not found". Pin both the compiler
    // and its bundled well-known types via protoc-bin-vendored so the build is
    // hermetic and does not depend on whatever protoc the host happens to have.
    std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);
    let wkt_include = protoc_bin_vendored::include_path()?;

    let include_dirs = vec![proto_dir.to_path_buf(), wkt_include];

    // Build both client and server stubs. The server stubs are required so
    // that the idiomatic clients can stand up in-process mock gRPC servers in
    // their test suites (e.g. a mock WatchService to exercise streaming).
    tonic_build::configure()
        .build_server(true)
        .compile_protos(&proto_files, &include_dirs)?;

    Ok(())
}
