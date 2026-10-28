# frozen_string_literal: true

require_relative 'spicedb/zed_token'
require_relative 'spicedb/options'
require_relative 'spicedb/values'
require_relative 'spicedb/consistency'
require_relative 'spicedb/relationship'
require_relative 'spicedb/filter'
require_relative 'spicedb/transaction'
require_relative 'spicedb/error_details'
require_relative 'spicedb/errors'
require_relative 'spicedb/call_options'
require_relative 'spicedb/caveat_context'
require_relative 'spicedb/stream'
require_relative 'spicedb/retrying'
require_relative 'spicedb/watch_mapping'
require_relative 'spicedb/connecting'
require_relative 'spicedb/transport'
require_relative 'spicedb/operations'
require_relative 'spicedb/debug'
require_relative 'spicedb/materialize'
require_relative 'spicedb/results'
require_relative 'spicedb/paged_items'
require_relative 'spicedb/protocol'
require_relative 'spicedb/schema_protocol'
require_relative 'spicedb/response_mapping'
require_relative 'spicedb/protoconv'
require_relative 'spicedb/client'

# SpiceDB is the idiomatic Ruby client for SpiceDB, a database for
# fine-grained authorization.
#
# Integrating with an AI coding agent (or as one)? Start with
# AI_INTEGRATION_GUIDE.md in this gem: connecting, fail-closed checks,
# atomic writes, paging, tooling options, errors and the test double.
# Moving from the `authzed` gem: MIGRATION.md.
module SpiceDB
  VERSION = '0.1.0'
end
