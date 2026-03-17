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

    // Collect all unique parent directories for include paths
    let include_dirs: Vec<_> = vec![proto_dir.to_path_buf()];

    tonic_build::configure()
        .build_server(false)
        .compile_protos(&proto_files, &include_dirs)?;

    Ok(())
}
