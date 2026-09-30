defmodule SpiceDB.PermissionTree do
  @moduledoc """
  One node of the tree returned by `SpiceDB.expand_permission_tree/5`.

  Exactly one of `intermediate` and `leaf` is set on a well-formed node.
  """

  defstruct [:expanded_object, expanded_relation: "", intermediate: nil, leaf: nil]

  @type t :: %__MODULE__{
          expanded_object: SpiceDB.ObjectRef.t() | nil,
          expanded_relation: String.t(),
          intermediate: SpiceDB.IntermediateNode.t() | nil,
          leaf: SpiceDB.LeafNode.t() | nil
        }
end

defmodule SpiceDB.IntermediateNode do
  @moduledoc "A set operation over child trees."

  defstruct operation: :unspecified, children: []

  @type operation :: :union | :intersection | :exclusion | :unspecified

  @type t :: %__MODULE__{operation: operation(), children: [SpiceDB.PermissionTree.t()]}
end

defmodule SpiceDB.LeafNode do
  @moduledoc "The subjects found directly on a relation."

  defstruct subjects: []

  @type t :: %__MODULE__{subjects: [SpiceDB.SubjectRef.t()]}
end

defmodule SpiceDB.ExpandResult do
  @moduledoc "An expanded permission tree and the ZedToken it was read at."

  defstruct [:tree, revision: ""]

  @type t :: %__MODULE__{tree: SpiceDB.PermissionTree.t() | nil, revision: SpiceDB.zed_token()}
end
