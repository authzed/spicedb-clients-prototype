# frozen_string_literal: true

require 'open3'
require 'rbconfig'

# Other specs load google/protobuf/well_known_types themselves, which hid that
# the library did not: without it Any#unpack is undefined, the failure was
# swallowed, and every server reason was silently dropped. This runs the
# library alone in a fresh process.
RSpec.describe 'error reasons with only the library loaded' do
  it 'decodes ErrorInfo from a status without the caller loading protobuf well-known types' do
    lib = File.expand_path('../lib/spicedb', __dir__)
    script = <<~RUBY
      require #{lib.inspect}
      require 'spicedb_proto'
      info = Google::Rpc::ErrorInfo.new(reason: 'ERROR_REASON_X', domain: 'authzed.com')
      error = SpiceDB::Error.from_code(9, 'm', server_message: 'm', reason: info.reason, reason_domain: info.domain)
      status = SpiceDB::ProtoConv.status_to_proto(error)
      print SpiceDB.to_spicedb_error(status).reason
    RUBY
    out, err, status = Open3.capture3(RbConfig.ruby, '-e', script)
    expect(status).to be_success, err
    expect(out).to eq('ERROR_REASON_X')
  end
end
