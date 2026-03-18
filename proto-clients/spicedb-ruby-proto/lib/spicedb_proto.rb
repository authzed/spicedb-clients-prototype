# frozen_string_literal: true

# Add gen/ to the load path so generated files' relative requires resolve.
$LOAD_PATH.unshift(File.join(__dir__, "gen")) unless $LOAD_PATH.include?(File.join(__dir__, "gen"))

# Load generated protobuf and gRPC service files.
# After running `buf generate`, the lib/gen/ directory contains all proto
# definitions and service stubs for the Authzed SpiceDB API.
Dir[File.join(__dir__, "gen", "**", "*.rb")].sort.each { |f| require f }

require_relative "spicedb_proto/client"
