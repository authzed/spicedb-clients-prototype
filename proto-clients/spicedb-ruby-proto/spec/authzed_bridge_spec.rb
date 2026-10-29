# frozen_string_literal: true

require "rspec"
require "fileutils"
require "json"
require "open3"
require "rbconfig"
require "tmpdir"
require_relative "../bridge/authzed_bridge"

# Root DESIGN.md, "Be the single copy of the protocol bindings in any process":
# each proto tier ships the bridge for its legacy library and a test that loads
# both in one process. Everything here runs in subprocesses, because loading
# the real generated bindings would collide with the fake stub modules
# spec/client_spec.rb defines in this process.
RSpec.describe AuthzedBridge do
  PROTO_LIB = File.expand_path("../lib", __dir__)

  # Loads the gems in the given order and reports what the process ended up with.
  probe = <<~'RUBY'
    require "json"
    order = ARGV.fetch(0).split(",")
    begin
      order.each { |feature| require feature }
      pool = Google::Protobuf::DescriptorPool.generated_pool
      legacy = Authzed::Api::V1::Client.new(target: "localhost:1", credentials: :this_channel_is_insecure)
      canonical = SpiceDBProto::Client.new("localhost:1", "token", insecure: true)
      check_input = Authzed::Api::V1::PermissionsService::Service.rpc_descs[:CheckPermission].input
      required = ARGV.drop(1).map { |feature| [feature, (require(feature) ? "loaded now" : "already loaded")] }.to_h
      puts JSON.generate(
        ok: true,
        core_files: $LOADED_FEATURES.grep(%r{/authzed/api/v1/core_pb\.rb\z}),
        status_files: $LOADED_FEATURES.grep(%r{/google/rpc/status_pb\.rb\z}),
        pool_file: pool.lookup("authzed.api.v1.CheckPermissionRequest").file_descriptor.name,
        same_message_class: check_input.equal?(pool.lookup("authzed.api.v1.CheckPermissionRequest").msgclass),
        same_stub_class: legacy.permissions_service.class.equal?(canonical.permissions.class),
        authzed_entry: $LOADED_FEATURES.grep(%r{/authzed\.rb\z}),
        required: required
      )
    rescue Exception => e # rubocop:disable Lint/RescueException
      puts JSON.generate(ok: false, error: "#{e.class}: #{e.message[0, 200]}")
    end
  RUBY

  let(:workdir) { File.realpath(Dir.mktmpdir("authzed-bridge")) }
  let(:checkout) { File.join(workdir, "authzed") }
  let(:probe_path) { File.join(workdir, "probe.rb").tap { |path| File.write(path, probe) } }

  before do
    source = Gem::Specification.find_by_name("authzed").gem_dir
    FileUtils.cp_r(source, checkout)
    File.write(File.join(checkout, "authzed.gemspec"), <<~GEMSPEC)
      Gem::Specification.new do |s|
        s.name = "authzed"
        s.version = "1.5.0"
        s.add_dependency "grpc", "~> 1.41"
      end
    GEMSPEC
  end

  after { FileUtils.remove_entry(workdir) }

  def load_both(order, *features)
    out, err, = Open3.capture3(RbConfig.ruby, "-I", File.join(checkout, "lib"), "-I", PROTO_LIB, probe_path, order, *features)
    JSON.parse(out.lines.last || "{}")
  rescue JSON::ParserError
    raise "probe printed no result: #{out}#{err}"
  end

  it "reproduces the conflict without the bridge (the control)" do
    result = load_both("authzed,spicedb_proto")
    expect(result["ok"]).to be(false)
    expect(result["error"]).to match(/BindingConflictError|duplicate file name/)
  end

  it "bridges the gem so both load in one process, in either order, onto one pool entry" do
    report = described_class.run(checkout)
    expect(report.deleted).to include("authzed/api/v1/core_pb.rb", "authzed/api/v1/permission_service_services_pb.rb",
                                      "google/rpc/status_pb.rb", "buf/validate/validate_pb.rb")
    expect(report.entrypoint_rewritten).to be(true)
    expect(Dir[File.join(checkout, "lib", "**", "*_pb.rb")]).to be_empty
    expect(File.exist?(File.join(checkout, "lib", "authzed", "api", "v1", "client.rb"))).to be(true)
    expect(File.read(File.join(checkout, "authzed.gemspec"))).to include("s.add_dependency 'spicedb-proto'")

    # Every require path the legacy gem's users could name still resolves.
    features = report.deleted.map { |path| path.delete_suffix(".rb") }
    %w[authzed,spicedb_proto spicedb_proto,authzed].each do |order|
      result = load_both(order, *features)
      expect(result["ok"]).to be(true), result["error"]
      expect(result["core_files"].length).to eq(1)
      expect(result["core_files"].first).to start_with(File.join(PROTO_LIB, "gen"))
      expect(result["status_files"].length).to be <= 1
      expect(result["pool_file"]).to eq("authzed/api/v1/permission_service.proto")
      expect(result["same_message_class"]).to be(true)
      expect(result["same_stub_class"]).to be(true)
      expect(result["authzed_entry"]).to eq([File.join(checkout, "lib", "authzed.rb")])
      expect(result["required"].keys).to match_array(features)
    end
  end

  it "is idempotent" do
    described_class.run(checkout)
    second = described_class.run(checkout)
    expect(second.deleted).to be_empty
    expect(second.entrypoint_rewritten).to be(false)
    expect(second.gemspecs_rewritten).to be_empty
  end

  it "refuses, changing nothing, when a require path would not resolve" do
    extra = File.join(checkout, "lib", "authzed", "api", "v1", "from_the_future_pb.rb")
    File.write(extra, "# Generated by the protocol buffer compiler.  DO NOT EDIT!\n")
    expect { described_class.run(checkout) }.to raise_error(AuthzedBridge::Error, /from_the_future_pb/)
    expect(File.exist?(File.join(checkout, "lib", "authzed", "api", "v1", "core_pb.rb"))).to be(true)
  end
end
