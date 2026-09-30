defmodule SpiceDB.PartialCaveatInfo do
  @moduledoc """
  The caveat parameters SpiceDB could not evaluate for a conditional result.
  """

  defstruct missing_required_context: []

  @type t :: %__MODULE__{missing_required_context: [String.t()]}
end

defmodule SpiceDB.LookupResource do
  @moduledoc """
  One resource returned by `SpiceDB.lookup_resources/6`.

  `permissionship` is `:has_permission`, `:conditional_permission` or
  `:unspecified`. Lookups never report `:no_permission`: a resource without
  the permission is simply absent. A conditional result is not a grant until
  the caveat is evaluated with the context named in `partial_caveat`.
  """

  defstruct [:resource_id, permissionship: :unspecified, partial_caveat: nil, looked_up_at: ""]

  @type permissionship :: :unspecified | :has_permission | :conditional_permission

  @type t :: %__MODULE__{
          resource_id: String.t(),
          permissionship: permissionship(),
          partial_caveat: SpiceDB.PartialCaveatInfo.t() | nil,
          looked_up_at: SpiceDB.zed_token()
        }
end

defmodule SpiceDB.ResolvedSubject do
  @moduledoc "A subject resolved by a lookup, with its permissionship."

  defstruct [:subject_id, permissionship: :unspecified, partial_caveat: nil]

  @type t :: %__MODULE__{
          subject_id: String.t(),
          permissionship: SpiceDB.LookupResource.permissionship(),
          partial_caveat: SpiceDB.PartialCaveatInfo.t() | nil
        }
end

defmodule SpiceDB.LookupSubject do
  @moduledoc """
  One subject returned by `SpiceDB.lookup_subjects/6`.

  When `subject.subject_id` is the wildcard `"*"`, `excluded_subjects` lists
  subjects carved out of that wildcard grant. Treat those as NOT having the
  permission, even though the wildcard suggests otherwise.
  """

  defstruct [:subject, excluded_subjects: [], looked_up_at: ""]

  @type t :: %__MODULE__{
          subject: SpiceDB.ResolvedSubject.t(),
          excluded_subjects: [SpiceDB.ResolvedSubject.t()],
          looked_up_at: SpiceDB.zed_token()
        }
end

defmodule SpiceDB.CountResult do
  @moduledoc """
  The value of a registered relationship counter.

  While the server is still computing a newly registered counter,
  `still_calculating` is true, `relationship_count` is 0 and `revision` is
  `""`.
  """

  defstruct relationship_count: 0, revision: "", still_calculating: false

  @type t :: %__MODULE__{
          relationship_count: non_neg_integer(),
          revision: SpiceDB.zed_token(),
          still_calculating: boolean()
        }
end
