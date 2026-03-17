# frozen_string_literal: true

# Load generated protobuf and gRPC service files.
# After running `buf generate`, the lib/gen/ directory contains all proto
# definitions and service stubs for the Authzed SpiceDB API.
Dir[File.join(__dir__, "gen", "**", "*.rb")].sort.each { |f| require f }

require_relative "spicedb_proto/client"
