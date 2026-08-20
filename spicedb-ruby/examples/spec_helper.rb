# frozen_string_literal: true

require 'spicedb'

SPICEDB_ENDPOINT = ENV.fetch('SPICEDB_ENDPOINT', 'localhost:50051')
SPICEDB_TOKEN    = ENV.fetch('SPICEDB_TOKEN', 'somerandomkeyhere')

TEST_SCHEMA = <<~SCHEMA
  definition user {}

  definition document {
  	relation viewer: user
  	relation editor: user
  	relation owner: user
  	permission view = viewer + editor + owner
  	permission edit = editor + owner
  	permission delete = owner
  }
SCHEMA

RSpec.configure do |config|
  config.formatter = :documentation

  # Create a fresh plaintext client for each test, with clean state.
  config.around(:each) do |example|
    # An example tagged :no_spicedb brings its own server and must not get the
    # shared plaintext one -- custom_tls/ is the case: a plaintext SpiceDB has
    # nothing to demonstrate about TLS trust material, so that example stands
    # up its own TLS-terminated endpoint instead. Without this, the schema
    # write below would make it depend on a SpiceDB it never talks to.
    next example.run if example.metadata[:no_spicedb]

    SpiceDB::Client.new_plaintext(SPICEDB_ENDPOINT, SPICEDB_TOKEN) do |client|
      @client = client
      # Clear first, then write the schema -- not the other way round. Every
      # example runs against one SpiceDB, and SpiceDB refuses a WriteSchema
      # that drops a relation while a relationship still exists under it, so
      # what a previous example left behind has to go before this one's schema
      # write. TEST_SCHEMA happens to be a superset that drops nothing today,
      # which is the only reason the old order survived; narrow it and the
      # ordering becomes load-bearing with no warning.
      #
      # Exactly one error is tolerated: on a fresh server there is no
      # `document` definition yet, which SpiceDB reports as FAILED_PRECONDITION
      # (ERROR_REASON_UNKNOWN_DEFINITION). Anything else must still fail.
      begin
        client.delete_relationships(SpiceDB::Filter.new(resource_type: 'document'))
      rescue SpiceDB::FailedPreconditionError # rubocop:disable Lint/SuppressedException
      end
      client.write_schema(TEST_SCHEMA)
      example.run
    end
  end
end

# Helper to access the client inside examples.
# attr_reader is not available at the top level in Ruby 3.2+ strict mode,
# so define an explicit method instead.
def client
  @client
end
