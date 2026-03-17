Gem::Specification.new do |spec|
  spec.name          = "spicedb-proto"
  spec.version       = "0.1.0"
  spec.authors       = ["Authzed"]
  spec.email         = ["support@authzed.com"]

  spec.summary       = "SpiceDB gRPC proto client for Ruby"
  spec.description   = "Buf-generated gRPC stubs and a thin client wrapper for SpiceDB."
  spec.homepage      = "https://github.com/authzed/spicedb-clients"
  spec.license       = "Apache-2.0"
  spec.required_ruby_version = ">= 3.2"

  spec.files         = Dir["lib/**/*.rb"]
  spec.require_paths = ["lib"]

  spec.add_dependency "grpc", "~> 1.67"
  spec.add_dependency "google-protobuf", "~> 4.29"

  spec.add_development_dependency "rspec", "~> 3.13"
end
