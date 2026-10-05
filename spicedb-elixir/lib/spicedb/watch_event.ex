defmodule SpiceDB.WatchEvent do
  @moduledoc """
  One message from `SpiceDB.watch/3`.

  `changes_through` is the ZedToken to resume from: pass it as
  `start_revision:` to a new watch to continue after this event.
  A checkpoint event (`is_checkpoint: true`) carries no updates and only
  advances `changes_through`.
  """

  defstruct updates: [], changes_through: "", is_checkpoint: false

  @type t :: %__MODULE__{
          updates: [SpiceDB.Update.t()],
          changes_through: SpiceDB.zed_token(),
          is_checkpoint: boolean()
        }
end

defmodule SpiceDB.Update do
  @moduledoc "One relationship change inside a `SpiceDB.WatchEvent`."

  defstruct [:relationship, operation: :unspecified]

  @type operation :: :create | :touch | :delete | :unspecified

  @type t :: %__MODULE__{operation: operation(), relationship: SpiceDB.Relationship.t()}
end
