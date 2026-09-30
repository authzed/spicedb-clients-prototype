defmodule SpiceDB.RoaringLookupResourcesResult do
  @moduledoc """
  Experimental. The result of `SpiceDB.experimental_roaring_lookup_resources/6`.

  `bitmap` is a roaring64 bitmap (portable 64-bit format) of resource object
  ids, returned as raw bytes for a consumer such as a search index. This
  client does not decode it.
  """

  defstruct bitmap: "", cardinality: 0, at_revision: ""

  @type t :: %__MODULE__{
          bitmap: binary(),
          cardinality: non_neg_integer(),
          at_revision: SpiceDB.zed_token()
        }
end

defmodule SpiceDB.PermissionChange do
  @moduledoc """
  Experimental. A computed permission changing, from
  `SpiceDB.experimental_watch_permissions/4`.
  """

  defstruct [:resource, :permission, :subject, revision: "", permissionship: :unspecified]

  @type t :: %__MODULE__{
          revision: SpiceDB.zed_token(),
          resource: SpiceDB.ObjectRef.t(),
          permission: String.t(),
          subject: SpiceDB.SubjectRef.t(),
          permissionship: SpiceDB.CheckResult.permissionship()
        }
end

defmodule SpiceDB.WatchedPermission do
  @moduledoc """
  Experimental. A permission to watch with
  `SpiceDB.experimental_watch_permissions/4`.
  """

  @enforce_keys [:resource_type, :permission, :subject_type]
  defstruct [:resource_type, :permission, :subject_type, optional_subject_relation: ""]

  @type t :: %__MODULE__{
          resource_type: String.t(),
          permission: String.t(),
          subject_type: String.t(),
          optional_subject_relation: String.t()
        }
end

defmodule SpiceDB.SetReference do
  @moduledoc "Experimental. A permission set: an object and a permission or relation on it."

  defstruct [:object_type, :object_id, :permission_or_relation]

  @type t :: %__MODULE__{
          object_type: String.t(),
          object_id: String.t(),
          permission_or_relation: String.t()
        }
end

defmodule SpiceDB.MemberReference do
  @moduledoc "Experimental. A member of a permission set."

  defstruct [:object_type, :object_id, optional_permission_or_relation: ""]

  @type t :: %__MODULE__{
          object_type: String.t(),
          object_id: String.t(),
          optional_permission_or_relation: String.t()
        }
end

defmodule SpiceDB.PermissionSetChange do
  @moduledoc """
  Experimental. A permission set gaining or losing a child set or member.

  Exactly one of `child_set` and `child_member` is set.
  """

  defstruct [
    :parent_set,
    at_revision: "",
    operation: :unspecified,
    child_set: nil,
    child_member: nil
  ]

  @type operation :: :added | :removed | :unspecified

  @type t :: %__MODULE__{
          at_revision: SpiceDB.zed_token(),
          operation: operation(),
          parent_set: SpiceDB.SetReference.t() | nil,
          child_set: SpiceDB.SetReference.t() | nil,
          child_member: SpiceDB.MemberReference.t() | nil
        }
end

defmodule SpiceDB.PermissionSetsCursor do
  @moduledoc """
  Experimental. An opaque resumption point for
  `SpiceDB.experimental_lookup_permission_sets/3`. Pass it back unchanged as
  `after:`.
  """

  defstruct [:proto]

  @type t :: %__MODULE__{proto: Authzed.Api.Materialize.V0.Cursor.t()}
end

defmodule SpiceDB.PermissionSetsDownload do
  @moduledoc "Experimental. Snapshot files from `SpiceDB.experimental_download_permission_sets/2`."

  defstruct files: [], timestamp: nil, at_revision: ""

  @type file :: %{name: String.t(), url: String.t()}

  @type t :: %__MODULE__{
          files: [file()],
          timestamp: DateTime.t() | nil,
          at_revision: SpiceDB.zed_token()
        }
end
