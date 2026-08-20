# frozen_string_literal: true

require_relative '../spec_helper'

# Demonstrates the opt-in a plaintext connection to a remote host requires -- see
# root DESIGN.md, "RULE: Credentials over insecure transport require an explicit
# opt-in".
#
# The failure this rule exists to prevent is mundane and common: a developer
# copies a plaintext constructor out of a localhost example into a staging
# config, and a long-lived SpiceDB token -- a complete authorization bypass in
# anyone else's hands -- goes onto the wire in cleartext with nothing signalling
# that it happened. So `new_plaintext` is loopback-only, and reaching a remote
# host over plaintext takes a second, separately-named keyword the caller cannot
# supply by accident: `allow_insecure_remote_credentials:`.
#
# On the error type: this guard raises a plain +ArgumentError+, not
# +SpiceDB::InvalidArgumentError+. The seven clients currently give six different
# answers here (Go a plain error, Python InvalidArgumentError, TypeScript a plain
# Error, Java IllegalArgumentException, C# InvalidOperationException, Ruby
# ArgumentError), so this example asserts what THIS client does rather than
# inventing a seventh -- the divergence is recorded, not papered over.
#
# The sharpest cases are the last ones. Ruby hands its target to gRPC's C-core,
# which parses it in C++ out of this client's reach, so the rule requires failing
# closed on any endpoint whose authority could move under URI parsing rather than
# guessing: given `127.0.0.1:443@evil.com`, a last-colon split reads the host as
# `127.0.0.1` while the real authority is `evil.com`.
RSpec.describe 'Insecure transport opt-in' do
  it 'allows loopback plaintext with no opt-in, and the client works' do
    # The case the rule deliberately leaves ergonomic: a token on a loopback
    # socket never leaves the machine, so requiring ceremony here would only
    # train developers to reach for the opt-in reflexively.
    client = SpiceDB::Client.new_plaintext(SPICEDB_ENDPOINT, SPICEDB_TOKEN)

    # Prove the client is usable, not merely constructed: the channel connects
    # lazily, so a constructor returning a client that could not talk to
    # anything would still satisfy a "did not raise" assertion.
    expect(client.write_schema(TEST_SCHEMA)).not_to be_empty
  end

  it 'refuses a remote plaintext host without the opt-in', :no_spicedb do
    # No connection is attempted: the refusal happens during construction, so
    # the token never reaches a socket. This is not about whether the host
    # exists -- example.com is refused because it is not loopback, full stop.
    expect { SpiceDB::Client.new_plaintext('example.com:50051', SPICEDB_TOKEN) }
      .to raise_error(ArgumentError, /example\.com:50051/)
  end

  it 'allows a remote plaintext host with the named opt-in', :no_spicedb do
    # Two keywords, not one, and that separation is the point: selecting the
    # plaintext transport and accepting the credential exposure that follows are
    # different decisions, and clause 1 forbids one boolean from doing both jobs.
    client = SpiceDB::Client.new_plaintext(
      'example.com:50051', SPICEDB_TOKEN, allow_insecure_remote_credentials: true
    )
    expect(client).not_to be_nil
  end

  [
    '127.0.0.1:443@evil.com',
    '127.0.0.1:50051/../evil.com',
    '127.0.0.1:50051?x=evil.com',
    '127.0.0.1:50051#evil.com',
    '127.0.0.1 :50051'
  ].each do |endpoint|
    it "refuses #{endpoint}, whose authority could move under URI parsing", :no_spicedb do
      # Fail closed. A client that split on the last colon would call
      # 127.0.0.1:443@evil.com loopback and hand the token to evil.com.
      expect { SpiceDB::Client.new_plaintext(endpoint, SPICEDB_TOKEN) }
        .to raise_error(ArgumentError)
    end
  end
end
