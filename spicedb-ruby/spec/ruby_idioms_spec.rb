# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative '../lib/spicedb/testing'
require 'open3'
require 'rbconfig'

# Ruby conventions applications already rely on: Symbol names, Integer
# primary keys, nested to_h hashes, and grpc's per-code rescue clauses.
RSpec.describe 'Ruby idioms' do
  let(:alice) { SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: :user, object_id: 1)) }

  describe 'Symbol names and Integer IDs' do
    it 'converts Symbols for names and Integers for object IDs with to_s' do
      check = SpiceDB::PermissionCheck.new(resource: SpiceDB::ObjectRef.new(object_type: :document, object_id: 42), permission: :view,
                                           subject: alice)
      expect(check.permission).to eq('view')
      expect(check.resource).to eq(SpiceDB::ObjectRef.new(object_type: 'document', object_id: '42'))
      expect(alice.object.object_id).to eq('1')
      relationship = SpiceDB::Relationship.new(resource: check.resource, relation: :reader,
                                               subject: SpiceDB::SubjectRef.new(object: alice.object, relation: :member))
      expect(relationship.to_s).to eq('document:42#reader@user:1#member')
      expect(SpiceDB::Caveat.new(name: :weekdays).name).to eq('weekdays')
    end

    it 'converts them in filters, so filters match the stored strings' do
      filter = SpiceDB::Filter.new(resource_type: :document, resource_id: 42, relation: :reader,
                                   subject: SpiceDB::SubjectFilter.new(subject_type: :user, subject_id: 1,
                                                                       relation: SpiceDB::SubjectRelationFilter.named(:member)))
      expect(filter.to_s).to eq('document:42#reader@user:1#member')
      expect(filter.matches?(SpiceDB::Relationship.from_tuple('document:42#reader@user:1#member'))).to be(true)
    end

    it 'names the class it received when rejecting a value' do
      expect { SpiceDB::ObjectRef.new(object_type: 'user', object_id: 1.5) }
        .to raise_error(SpiceDB::InvalidArgumentError, 'object_id: expected a nonempty String or Integer, got Float (1.5)')
      expect { SpiceDB::ObjectRef.new(object_type: 7, object_id: '1') }
        .to raise_error(SpiceDB::InvalidArgumentError, 'object_type: expected a nonempty String or Symbol, got Integer (7)')
      expect { SpiceDB::PermissionCheck.new(resource: SpiceDB::ObjectRef.new(object_type: 'doc', object_id: '1'), permission: nil, subject: alice) }
        .to raise_error(SpiceDB::InvalidArgumentError, 'permission: expected a nonempty String or Symbol, got NilClass (nil)')
      expect { SpiceDB::ObjectRef.new(object_type: :'', object_id: '1') }.to raise_error(SpiceDB::InvalidArgumentError, /got Symbol/)
    end

    # Root DESIGN.md: a dynamically typed client checks the types of names
    # and IDs before use. Integers are the one deliberate extra ID type.
    [nil, 1.5, :alice, { id: 1 }, [1], true].each do |bad|
      it "rejects the object ID #{bad.inspect} with LocalValidationError" do
        expect { SpiceDB::ObjectRef.new(object_type: 'user', object_id: bad) }
          .to raise_error(SpiceDB::LocalValidationError, /\Aobject_id: expected a nonempty String or Integer, got #{bad.class}/)
      end
    end

    [1.5, :alice, { id: 1 }, [1], true].each do |bad|
      it "rejects the filter ID #{bad.inspect} with LocalValidationError" do
        expect { SpiceDB::Filter.new(resource_type: 'document', resource_id: bad) }
          .to raise_error(SpiceDB::LocalValidationError, /\Aresource_id: expected a String or Integer, got #{bad.class}/)
        expect { SpiceDB::Filter.new(resource_type: 'document', resource_id_prefix: bad) }
          .to raise_error(SpiceDB::LocalValidationError, /\Aresource_id_prefix: expected a String or Integer/)
        expect { SpiceDB::SubjectFilter.new(subject_type: 'user', subject_id: bad) }
          .to raise_error(SpiceDB::LocalValidationError, /\Asubject_id: expected a String or Integer/)
      end
    end

    [7, 1.5, { name: 'x' }, ['x'], true].each do |bad|
      it "rejects the name #{bad.inspect} with LocalValidationError" do
        expect { SpiceDB::ObjectRef.new(object_type: bad, object_id: '1') }
          .to raise_error(SpiceDB::LocalValidationError, /\Aobject_type: expected a nonempty String or Symbol, got #{bad.class}/)
        expect { SpiceDB::Filter.new(resource_type: bad) }
          .to raise_error(SpiceDB::LocalValidationError, /\Aresource_type: expected a String or Symbol, got #{bad.class}/)
        expect { SpiceDB::Filter.new(resource_type: 'document', relation: bad) }
          .to raise_error(SpiceDB::LocalValidationError, /\Arelation: expected a String or Symbol/)
        expect { SpiceDB::SchemaFilter.new(definition_name: bad) }
          .to raise_error(SpiceDB::LocalValidationError, /\Adefinition_name: expected a String or Symbol/)
      end
    end

    it 'rejects a wrong-typed schema reflection name before any request' do
      SpiceDB::Client.new_plaintext('localhost:50051', 'key') do |client|
        expect(client.proto_client.schema).not_to receive(:computable_permissions)
        expect { client.computable_permissions(definition_name: 7, relation_name: 'viewer', consistency: SpiceDB::Consistency.full) }
          .to raise_error(SpiceDB::LocalValidationError, /\Adefinition_name: expected a String or Symbol, got Integer/)
      end
    end

    it 'sends Symbol names from the lookup operations' do
      store = SpiceDB::Testing::Store.new([SpiceDB::Relationship.from_tuple('document:42#view@user:1')])
      found = store.lookup_resources(resource_type: :document, permission: :view, subject: alice, consistency: SpiceDB::Consistency.full)
      expect(found.map(&:resource_id)).to eq(['42'])
    end
  end

  describe 'deep to_h' do
    it 'walks nested native values, as a protobuf message hash does' do
      relationship = SpiceDB::Relationship.from_tuple('document:readme#viewer@group:eng#member[weekdays:{"tz":"UTC"}]')
      item = SpiceDB::ReadRelationship.new(relationship: relationship, cursor: 'c1', read_at: SpiceDB::ZedToken.new('t1'))
      hash = item.to_h
      expect(hash[:relationship][:subject][:object][:object_type]).to eq('group')
      expect(hash[:relationship][:subject][:object][:object_id]).to eq('eng')
      expect(hash[:relationship][:subject][:relation]).to eq('member')
      expect(hash[:relationship][:resource]).to eq(object_type: 'document', object_id: 'readme')
      expect(hash[:relationship][:caveat]).to eq(name: 'weekdays', context: { 'tz' => 'UTC' })
      expect(hash[:cursor]).to eq('c1')
      expect(hash[:read_at]).to eq(SpiceDB::ZedToken.new('t1'))
      expect(item.to_h { |key, value| [key.to_s, value] }.keys).to eq(%w[relationship cursor read_at])
    end

    it 'is deep for results too' do
      result = SpiceDB::LookupSubject.new(subject: SpiceDB::ResolvedSubject.new(subject_id: 'a', permissionship: :has_permission, partial_caveat: nil),
                                          excluded_subjects: [], looked_up_at: nil)
      expect(result.to_h[:subject]).to eq(subject_id: 'a', permissionship: :has_permission, partial_caveat: nil)
    end
  end

  describe 'grpc per-code rescue clauses' do
    it 'catch the native error for the same code' do
      caught = begin
        raise SpiceDB::AlreadyExistsError, 'exists'
      rescue GRPC::NotFound
        :wrong
      rescue GRPC::AlreadyExists => e
        e
      end
      expect(caught).to be_a(SpiceDB::AlreadyExistsError)
      expect(GRPC::Aborted === SpiceDB.to_spicedb_error(GRPC::Aborted.new('again'))).to be(true) # rubocop:disable Style/CaseEquality
      SpiceDB::GRPC_CODE_TO_ERROR.each do |code, klass|
        grpc_class = GRPC::BadStatus.new_status_exception(code).class
        expect(grpc_class === klass.new('x')).to be(true), klass.name # rubocop:disable Style/CaseEquality
      end
    end

    it 'leave raw grpc matching and the hierarchy unchanged' do
      expect(GRPC::AlreadyExists === GRPC::AlreadyExists.new('x')).to be(true) # rubocop:disable Style/CaseEquality
      expect(GRPC::AlreadyExists === GRPC::NotFound.new('x')).to be(false) # rubocop:disable Style/CaseEquality
      expect(GRPC::AlreadyExists === SpiceDB::NotFoundError.new('x')).to be(false) # rubocop:disable Style/CaseEquality
      expect(SpiceDB::AlreadyExistsError.ancestors).to include(SpiceDB::Error, GRPC::BadStatus)
      expect(SpiceDB::AlreadyExistsError.new('x')).not_to be_a(GRPC::AlreadyExists)
    end

    it 'see a server message on every error' do
      expect(SpiceDB::InvalidArgumentError.new('bad input').server_message).to eq('bad input')
      expect(SpiceDB::Error.from_code(GRPC::Core::StatusCodes::NOT_FOUND, 'gone').server_message).to eq('gone')
      expect(SpiceDB::Error.new.server_message).to eq('SpiceDB::Error')
    end
  end

  describe 'ProtoConv in a fresh process' do
    it 'converts results right after require, before any client exists' do
      lib = File.expand_path('../lib', __dir__)
      script = <<~RUBY
        require 'spicedb'
        rel = SpiceDB::Relationship.from_tuple('document:readme#viewer@user:alice')
        item = SpiceDB::ReadRelationship.new(relationship: rel, cursor: 'c', read_at: SpiceDB::ZedToken.new('t'))
        read = SpiceDB::ProtoConv.read_relationship_to_proto(item)
        write = SpiceDB::ProtoConv.write_result_to_proto(SpiceDB::WriteResult.new(revision: SpiceDB::ZedToken.new('t')))
        expand = SpiceDB::ProtoConv.expand_result_to_proto(SpiceDB::ExpandResult.new(tree: nil, revision: SpiceDB::ZedToken.new('t')))
        print [read.to_h[:relationship][:subject][:object][:object_id], write.written_at.token, expand.class.name].join(' ')
      RUBY
      out, err, status = Open3.capture3(RbConfig.ruby, '-W0', '-I', lib, '-e', script)
      expect(status.success?).to be(true), err
      expect(out).to eq('alice t Authzed::Api::V1::ExpandPermissionTreeResponse')
    end
  end
end
