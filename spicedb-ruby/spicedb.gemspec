# frozen_string_literal: true

Gem::Specification.new do |spec|
  spec.name          = "spicedb"
  spec.version       = "0.1.0"
  spec.authors       = ["Authzed"]
  spec.email         = ["support@authzed.com"]

  spec.summary       = "Idiomatic Ruby client for SpiceDB"
  spec.description   = "A Ruby client library for SpiceDB, the database for " \
                        "fine-grained authorization. Provides idiomatic Ruby " \
                        "types and patterns — no protobuf knowledge required."
  spec.homepage      = "https://github.com/authzed/spicedb-clients"
  spec.license       = "Apache-2.0"
  spec.required_ruby_version = ">= 3.2"

  spec.metadata["homepage_uri"]    = spec.homepage
  spec.metadata["source_code_uri"] = "https://github.com/authzed/spicedb-clients/tree/main/spicedb-ruby"
  spec.metadata["changelog_uri"]   = "https://github.com/authzed/spicedb-clients/blob/main/spicedb-ruby/DESIGN.md"

  spec.files         = Dir["lib/**/*.rb", "DESIGN.md", "CLAUDE.md", "LICENSE"]
  spec.require_paths = ["lib"]

  spec.add_dependency "grpc", "~> 1.60"

  spec.add_development_dependency "rspec", "~> 3.13"
  spec.add_development_dependency "rubocop", "~> 1.60"
end
