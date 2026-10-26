# frozen_string_literal: true

# Fixture helpers construct only canonical public request types.
module NativeFixtures
  def permission_check(relationship, permission = 'view')
    return relationship.with(permission: permission) if relationship.is_a?(SpiceDB::PermissionCheck)

    SpiceDB::PermissionCheck.new(resource: relationship.resource, permission: permission, subject: relationship.subject)
  end
end
RSpec.configure { |config| config.include NativeFixtures }

class FixtureOperation
  attr_reader :cancelled

  def initialize(responses) = @responses = responses
  def execute = @responses
  def cancel = @cancelled = true
end
