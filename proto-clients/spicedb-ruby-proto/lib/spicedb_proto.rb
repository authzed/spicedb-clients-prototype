# frozen_string_literal: true

require "grpc"

module SpiceDBProto
  # Raised at load time when another library already loaded its own,
  # independently generated copy of an authzed.api file (typically an
  # unbridged `authzed` gem). Continuing would bind this gem's stubs to that
  # copy's messages, so it fails closed instead.
  class BindingConflictError < LoadError; end
end

# This gem is the canonical Ruby binding for authzed.api.v1 and
# authzed.api.materialize.v0. Its generated files keep the require paths the
# official `authzed` gem uses (authzed/api/v1/core_pb, ...), and gen/ goes to
# the front of the load path, so every require of those paths in the process
# -- including a bridged `authzed` gem's -- resolves to this one copy.
# Google::Protobuf::DescriptorPool.generated_pool admits one definition per
# proto file, so a second copy either raises "duplicate file name" or, when it
# loaded first under the same require path, silently replaces this one; both
# are reported as BindingConflictError.
#
# google/api/* and google/rpc/* are NOT generated here: they come from
# googleapis-common-protos-types (a dependency of grpc itself), so this gem
# never registers a second copy of those either.
gen = File.join(__dir__, "gen")
$LOAD_PATH.unshift(gen) unless $LOAD_PATH.include?(gen)

# Required by feature name, not absolute path, so a later require of the same
# feature from anywhere is a no-op.
Dir[File.join(gen, "**", "*_pb.rb")].sort.each do |file|
  feature = file.delete_prefix("#{gen}/").delete_suffix(".rb")
  begin
    require feature
  rescue Google::Protobuf::TypeError => e
    raise SpiceDBProto::BindingConflictError,
          "spicedb-proto: #{feature} conflicts with another copy of the same proto file (#{e.message}). " \
          "Another library generated its own authzed.api bindings; bridge it onto spicedb-proto " \
          "(for the authzed gem: bridge/authzed_bridge.rb)"
  end
  # Another file loaded under this exact require path: same feature name,
  # rooted at a load-path directory, but not this gem's file.
  other = $LOADED_FEATURES.find do |loaded|
    next false unless loaded.end_with?("/#{feature}.rb") && !File.identical?(loaded, file)

    root = loaded.delete_suffix("/#{feature}.rb")
    $LOAD_PATH.any? { |dir| File.identical?(dir.to_s, root) }
  end
  next unless other

  raise SpiceDBProto::BindingConflictError,
        "spicedb-proto: #{feature} was already loaded from #{other}, another library's copy of the authzed.api " \
        "bindings. Bridge that library onto spicedb-proto (for the authzed gem: bridge/authzed_bridge.rb) so one " \
        "copy is loaded"
end

require_relative "spicedb_proto/client"
