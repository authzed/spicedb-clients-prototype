# frozen_string_literal: true

require 'timeout'

require_relative '../spec_helper'
require 'spicedb_proto'
# Struct.from_hash lives in protobuf's well-known-types helpers, which the
# generated code does not pull in on its own.
require 'google/protobuf/well_known_types'

# Example: reaching past the idiomatic API with #proto_client.
#
# Every wrapper eventually meets a request the wrapper does not express. This
# gem's answer is SpiceDB::Client#proto_client: the underlying proto client,
# whose permissions/schema/watch/experimental stubs are the ones this client
# makes its own calls through -- a workaround short of forking the gem. Root
# DESIGN.md, "What NOT To Do", allows exactly this as "clearly marked secondary
# API".
#
# The gaps demonstrated here are real, not hypothetical:
#
#   1. WriteRelationshipsRequest#optional_transaction_metadata is a proto field
#      this client does not surface anywhere. Applications use it to stamp an
#      audit correlation ID onto a write, which comes back out of the Watch
#      stream.
#   2. CheckPermission -- the single-check RPC. The idiomatic #check_permission
#      routes every check through CheckBulkPermissions, so the raw stub is how
#      you drive the unary RPC itself.
#
# What you give up on the raw path, and why the idiomatic methods stay the
# default: no SpiceDB::Error mapping (you rescue GRPC::BadStatus), no retry on a
# transient failure, and no default_timeout -- pass `deadline:` yourself.
# Bounds the wait for the raw Watch read-back below.
RAW_WATCH_TIMEOUT = 30

RSpec.describe 'RawEscapeHatch' do
  it 'sends a proto field the idiomatic API does not expose, and reads it back' do
    # A seed write fixes the revision the Watch below starts from, so it sees
    # the metadata write and nothing that came before it. (The shared hook
    # clears `document` before each example, so the raw write below is a real
    # change: a TOUCH of an already-identical relationship produces no watch
    # event.)
    seed = SpiceDB::Transaction.new
    seed.touch(SpiceDB::Relationship.from_triple('document', 'ledger', 'viewer', 'user', 'seed'))
    seed_revision = client.write(seed)

    # The stubs behind #proto_client already carry this client's bearer token
    # (composed call credentials on the secure path, an interceptor on the
    # plaintext one), so there is nothing extra to attach.
    metadata = Google::Protobuf::Struct.from_hash(
      'correlation_id' => 'example-42',
      'actor' => 'billing-job'
    )

    written = client.proto_client.permissions.write_relationships(
      Authzed::Api::V1::WriteRelationshipsRequest.new(
        updates: [
          Authzed::Api::V1::RelationshipUpdate.new(
            operation: :OPERATION_TOUCH,
            relationship: Authzed::Api::V1::Relationship.new(
              resource: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: 'ledger'),
              relation: 'viewer',
              subject: Authzed::Api::V1::SubjectReference.new(
                object: Authzed::Api::V1::ObjectReference.new(object_type: 'user', object_id: 'jimmy')
              )
            )
          )
        ],
        optional_transaction_metadata: metadata
      )
    )

    revision = written.written_at.token
    puts "raw write committed at revision #{revision}"
    expect(revision).not_to be_empty

    # Read the metadata back. Sending it proves nothing on its own: a client
    # that dropped the field would look identical from here, because
    # WriteRelationships does not echo it back. The only place it becomes
    # observable is the Watch stream -- and optional_transaction_metadata is
    # not on the idiomatic SpiceDB::WatchEvent either, so the read-back is a
    # second use of the same escape hatch.
    seen_metadata = nil
    Timeout.timeout(RAW_WATCH_TIMEOUT, nil,
                    "no watch event carried optional_transaction_metadata within #{RAW_WATCH_TIMEOUT}s: " \
                    'the metadata sent on the raw write never reached the server') do
      client.proto_client.watch.watch(
        Authzed::Api::V1::WatchRequest.new(
          optional_object_types: ['document'],
          optional_start_cursor: Authzed::Api::V1::ZedToken.new(token: seed_revision)
        )
      ).each do |resp|
        next if resp.optional_transaction_metadata.nil?

        seen_metadata = resp.optional_transaction_metadata.to_h
        # Abandoning a raw stream is the caller's job on this path -- see
        # ../../DESIGN.md, "Stream lifecycle: abandoning an Enumerator
        # releases it", for why `break` is what releases it here too.
        break
      end
    end

    puts "watch reported transaction metadata: #{seen_metadata}"
    expect(seen_metadata).to eq('correlation_id' => 'example-42', 'actor' => 'billing-job')

    # The idiomatic API picks up right where the raw call left off -- same
    # client, same connection, including read-your-writes on the raw revision.
    rel = SpiceDB::Relationship.from_triple('document', 'ledger', 'view', 'user', 'jimmy')
    result = client.check_permission(SpiceDB::Consistency.at_least(revision), 'view', rel)
    puts "user:jimmy can view document:ledger: #{result.has_permission?}"
    expect(result.has_permission?).to be true
  end

  it 'calls an RPC the idiomatic API routes around' do
    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'ledger', 'viewer', 'user', 'jimmy'))
    client.write(txn)

    # A raw call gets no client default deadline -- pass one yourself.
    response = client.proto_client.permissions.check_permission(
      Authzed::Api::V1::CheckPermissionRequest.new(
        consistency: Authzed::Api::V1::Consistency.new(fully_consistent: true),
        resource: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: 'ledger'),
        permission: 'view',
        subject: Authzed::Api::V1::SubjectReference.new(
          object: Authzed::Api::V1::ObjectReference.new(object_type: 'user', object_id: 'jimmy')
        )
      ),
      deadline: Time.now + 30
    )

    puts "raw CheckPermission permissionship: #{response.permissionship}"
    expect(response.permissionship).to eq(:PERMISSIONSHIP_HAS_PERMISSION)

    # Close the CLIENT, never the object #proto_client returned -- it holds this
    # client's connection, and SpiceDB::Client#close is what releases it. The
    # shared spec_helper block does that here.
  end
end
