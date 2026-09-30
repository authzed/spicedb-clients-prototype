defmodule SpiceDB.CheckResult do
  @moduledoc """
  The outcome of one permission check.

  `permissionship` is one of:

    * `:has_permission` - the subject has the permission
    * `:no_permission` - the subject does not have the permission
    * `:conditional_permission` - a caveat could not be evaluated because
      context was missing; `missing_context` names the absent parameters.
      This is not a grant.
    * `:unspecified` - the server sent no value, or one this client does not
      know. Also not a grant.

  Only `has_permission?/1` answers "is this allowed?". A result struct is
  always truthy, so never branch on the struct itself.

  `checked_at` is the ZedToken the check was evaluated at; pass it to
  `SpiceDB.Consistency.at_least/1` for read-your-writes.
  """

  defstruct permissionship: :unspecified, missing_context: [], checked_at: ""

  @type permissionship ::
          :unspecified | :no_permission | :has_permission | :conditional_permission

  @type t :: %__MODULE__{
          permissionship: permissionship(),
          missing_context: [String.t()],
          checked_at: SpiceDB.zed_token()
        }

  @doc "True only when `permissionship` is `:has_permission`."
  @spec has_permission?(t()) :: boolean()
  def has_permission?(%__MODULE__{permissionship: permissionship}),
    do: permissionship == :has_permission
end
