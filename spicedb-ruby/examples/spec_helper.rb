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
    SpiceDB::Client.new_plaintext(SPICEDB_ENDPOINT, SPICEDB_TOKEN) do |client|
      @client = client
      # Write the standard schema (idempotent)
      client.write_schema(TEST_SCHEMA)
      # Delete all existing relationships for test isolation
      client.delete_relationships(SpiceDB::Filter.new(resource_type: 'document'))
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
