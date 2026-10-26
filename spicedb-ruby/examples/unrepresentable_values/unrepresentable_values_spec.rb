# frozen_string_literal: true

require 'grpc'
require 'spicedb_proto'

require_relative '../spec_helper'

# Demonstrates both directions of root DESIGN.md, "RULE: A conversion that cannot
# preserve meaning must fail".
#
# The rule has two clauses that point opposite ways, and confusing them is the
# failure mode either way:
#
# 1. Data the CALLER supplied that the client cannot represent must raise a typed
#    error *naming what could not be converted*. The caller can see the failure
#    and fix their input, so the client neither approximates the value nor drops
#    it -- silently discarding it turns a caller's mistake into a silent wrong
#    answer.
# 2. Values the SERVER supplied that the client does not recognise must NOT
#    raise, and must map to the safe, non-permissive default -- never a grant.
#    Raising would turn a routine SpiceDB upgrade that adds an enum value into a
#    client-side outage.
#
# The last example covers clause 2 and needs a server that emits a
# permissionship this client has never heard of, so it stands up a stand-in.
# Answers with a permissionship value from a SpiceDB newer than this client.
class FutureService < Authzed::Api::V1::PermissionsService::Service
  def check_permission(_request, _call)
    Authzed::Api::V1::CheckPermissionResponse.new(permissionship: 4242)
  end
end

RSpec.describe 'Unrepresentable values' do
  it 'refuses unconvertible caveat context, naming the key' do
    # A value with no protobuf representation fails loudly, naming the key.
    # Dropping it would leave a caveat evaluating against context the caller
    # believes it sent, and a caller with a large context map should not have to
    # bisect it to find the bad entry.
    expect do
      SpiceDB::Caveat.new(name: 'only_on_tuesday', context: { 'day' => 'tuesday', 'impostor' => Object.new })
    end.to raise_error(SpiceDB::InvalidArgumentError, /impostor/)
  end

  it 'refuses a subject filter the wire cannot express, rather than widening it' do
    # subject_id with no subject_type is not a narrower filter -- the wire format
    # simply drops it, so the filter silently WIDENS. Applied to
    # delete_relationships that is the difference between deleting alice's
    # relationships and deleting every relationship on every document.
    expect { client.delete_all_relationships_non_atomic(SpiceDB::Filter.new(resource_type: 'document').with_subject(SpiceDB::SubjectFilter.new(subject_type: '', subject_id: 'alice'))) }
      .to raise_error(SpiceDB::InvalidArgumentError, /subject_type/)

    # The same filter with the missing piece supplied converts fine, which is
    # what makes the check above a real constraint rather than a blanket ban.
    expect do
      client.delete_all_relationships_non_atomic(SpiceDB::Filter.new(resource_type: 'document').with_subject(SpiceDB::SubjectFilter.new(subject_type: 'user',
                                                                                                                                        subject_id: 'alice')))
    end.not_to raise_error
  end

  it 'neither raises nor grants on a permissionship it has never seen', :no_spicedb do
    server = GRPC::RpcServer.new(pool_size: 4)
    port = server.add_http2_port('127.0.0.1:0', :this_port_is_insecure)
    server.handle(FutureService)
    thread = Thread.new { server.run }
    server.wait_till_running

    begin
      SpiceDB::Client.new_plaintext("127.0.0.1:#{port}", 'some-token') do |c|
        result = c.check(
          SpiceDB::PermissionCheck.new(
            resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'readme'), permission: 'view',
            subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'))
          ), consistency: SpiceDB::Consistency.full
        )
        expect(result.has_permission?)
          .to be(false), 'SECURITY: an unrecognised permissionship was treated as a grant'
      end
    ensure
      server.stop
      thread.join(5)
    end
  end
end
