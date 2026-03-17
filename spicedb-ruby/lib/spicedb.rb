# frozen_string_literal: true

require_relative "spicedb/consistency"
require_relative "spicedb/relationship"
require_relative "spicedb/filter"
require_relative "spicedb/transaction"
require_relative "spicedb/errors"
require_relative "spicedb/client"

# SpiceDB is the idiomatic Ruby client for SpiceDB, a database for
# fine-grained authorization.
module SpiceDB
  VERSION = "0.1.0"
end
