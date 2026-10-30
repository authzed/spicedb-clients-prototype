# frozen_string_literal: true

# MIGRATION.md opens with the floors the gemspecs declare; this keeps them in
# step.
RSpec.describe 'MIGRATION.md requirements' do
  let(:root) { File.expand_path('..', __dir__) }
  let(:migration) { File.read(File.join(root, 'MIGRATION.md')) }
  let(:requirements) { migration[/^## Requirements\n(.*?)^## /m, 1] }

  def floor(gemspec, name)
    spec = Gem::Specification.load(File.join(root, gemspec))
    return spec.required_ruby_version.requirements.first.last.to_s if name == 'Ruby'

    spec.dependencies.find { |dep| dep.name == name }.requirement.requirements.find { |op, _| op == '>=' }.last.to_s
  end

  it 'is the first section' do
    expect(migration.scan(/^## .*/).first).to eq('## Requirements')
  end

  {
    'Ruby' => 'spicedb.gemspec',
    'grpc' => 'spicedb.gemspec',
    'google-protobuf' => '../proto-clients/spicedb-ruby-proto/spicedb-proto.gemspec',
    'googleapis-common-protos-types' => '../proto-clients/spicedb-ruby-proto/spicedb-proto.gemspec'
  }.each do |name, gemspec|
    it "states the #{name} floor from #{File.basename(gemspec)}" do
      label = name == 'Ruby' ? 'Ruby' : "`#{name}`"
      expect(requirements).to include("| #{label} | >= #{floor(gemspec, name)} |")
    end
  end
end
