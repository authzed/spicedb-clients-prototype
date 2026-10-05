defmodule SpiceDB do
  @moduledoc """
  The idiomatic Elixir client for [SpiceDB](https://authzed.com/spicedb).

      {:ok, client} = SpiceDB.new_system_tls("grpc.authzed.com:443", token)

      rel = SpiceDB.Relationship.from_tuple!("document:readme#viewer@user:alice")
      {:ok, result} = SpiceDB.check_permission(client, SpiceDB.Consistency.full(), "view", rel)
      SpiceDB.CheckResult.has_permission?(result)

  ## Conventions

    * Every call returns `{:ok, value}` or `{:error, exception}`, where the
      exception is one of the kinds listed by `SpiceDB.Error.kinds/0`. Each
      function has a `!` sibling that returns the value or raises.
    * Every read takes an explicit `SpiceDB.Consistency` as its second
      argument. There is no default.
    * Every call takes a trailing keyword list. Unary calls accept
      `timeout:` in milliseconds (or `:infinity`), which bounds each attempt
      separately; the default is the client's `default_timeout:` (30 seconds).
    * Streaming calls (`read_relationships/4`, `lookup_resources/6`,
      `lookup_subjects/6`, `export_relationships/3`, `watch/3`) return
      `{:ok, stream}`, a lazy `Stream`. They have no timeout. Halting the
      stream early (`Enum.take/2`, `Stream.take_while/2`, an exception)
      cancels the underlying HTTP/2 stream. A server error after the stream
      was established raises the typed error from the enumerating process.
      Enumerating the same stream again issues the request again.
    * Reads retry on `UNAVAILABLE` and `ABORTED`, up to 3 times with full
      jitter. Writes and deletes never retry, because a lost response to a
      committed write would come back as a confident, wrong error.
      `RESOURCE_EXHAUSTED` is never retried.

  ## Experimental calls

  Functions prefixed `experimental_` wrap RPCs SpiceDB marks experimental or
  serves from its materialize tier (`authzed.api.materialize.v0`). Their
  shape may change in any release, and a server that does not serve them
  answers with `SpiceDB.Error` carrying code 12 (`UNIMPLEMENTED`).

  ## Escape hatch

  `proto_client/1` returns the `SpicedbProto.Client` this client wraps, for
  any RPC or field this module does not cover.
  """

  alias Authzed.Api.Materialize.V0, as: M
  alias Authzed.Api.V1

  alias SpiceDB.{
    CaveatContext,
    CheckResult,
    Client,
    Consistency,
    Filter,
    ObjectRef,
    Relationship,
    Retry,
    Streaming,
    SubjectRef,
    Transaction,
    Wire
  }

  @typedoc "An opaque revision token. Store it as-is; never parse it."
  @type zed_token :: String.t()

  @type client :: Client.t()
  @type error :: SpiceDB.Error.any_error()
  @type result(value) :: {:ok, value} | {:error, error()}

  @check_batch_size 1_000
  @read_page_size 512
  @lookup_page_size 512
  @export_page_size 512
  @delete_page_size 1_000
  @import_batch_size 1_000

  @connect_opts [:default_timeout]

  ## Construction

  @doc """
  Connects over plaintext HTTP/2. Meant for a local SpiceDB.

  A non-loopback endpoint is refused with `SpiceDB.InvalidArgumentError`,
  since the bearer token would cross the network in cleartext. Pass
  `allow_insecure_remote_credentials: true` to connect anyway.

  Options:

    * `:default_timeout` - per-attempt timeout for unary calls, in
      milliseconds. Defaults to 30 000.
    * `:allow_insecure_remote_credentials` - permit a non-loopback endpoint.

  Connecting sets `trap_exit` on the calling process, a side effect of the
  underlying HTTP/2 client; the connection is linked to that process.
  """
  @spec new_plaintext(String.t(), String.t(), keyword()) :: result(client())
  def new_plaintext(endpoint, token, opts \\ []) do
    opts = Keyword.validate!(opts, [:allow_insecure_remote_credentials | @connect_opts])

    connect(endpoint, token, opts,
      insecure: true,
      allow_insecure_remote_credentials:
        Keyword.get(opts, :allow_insecure_remote_credentials, false)
    )
  end

  @doc "Like `new_plaintext/3`, but raises."
  @spec new_plaintext!(String.t(), String.t(), keyword()) :: client()
  def new_plaintext!(endpoint, token, opts \\ []), do: bang(new_plaintext(endpoint, token, opts))

  @doc """
  Connects over TLS, trusting the operating system's certificate store.

  Options: `:default_timeout`, as for `new_plaintext/3`.
  """
  @spec new_system_tls(String.t(), String.t(), keyword()) :: result(client())
  def new_system_tls(endpoint, token, opts \\ []) do
    opts = Keyword.validate!(opts, @connect_opts)
    connect(endpoint, token, opts, [])
  end

  @doc "Like `new_system_tls/3`, but raises."
  @spec new_system_tls!(String.t(), String.t(), keyword()) :: client()
  def new_system_tls!(endpoint, token, opts \\ []),
    do: bang(new_system_tls(endpoint, token, opts))

  @doc """
  Connects over TLS with caller-supplied trust material, for a SpiceDB behind
  a private CA or one that requires mutual TLS.

  Options:

    * `:ca_cert` (required) - PEM certificate(s) to trust in place of the
      system store.
    * `:client_cert` and `:client_key` - PEM client identity for mutual TLS.
      Supply both or neither.
    * `:default_timeout` - as for `new_plaintext/3`.
  """
  @spec new_custom_tls(String.t(), String.t(), keyword()) :: result(client())
  def new_custom_tls(endpoint, token, opts) do
    opts = Keyword.validate!(opts, [:ca_cert, :client_cert, :client_key | @connect_opts])
    ca_cert = Keyword.get(opts, :ca_cert)
    client_cert = Keyword.get(opts, :client_cert)
    client_key = Keyword.get(opts, :client_key)

    cond do
      not (is_binary(ca_cert) and certificates?(ca_cert)) ->
        {:error,
         %SpiceDB.InvalidArgumentError{
           message:
             "new_custom_tls requires ca_cert: a PEM string holding at least one certificate"
         }}

      is_nil(client_cert) != is_nil(client_key) ->
        {present, missing} =
          if is_nil(client_key),
            do: {:client_cert, :client_key},
            else: {:client_key, :client_cert}

        {:error,
         %SpiceDB.InvalidArgumentError{
           message:
             "new_custom_tls: #{present} was supplied without #{missing}: mutual TLS needs both halves of the client identity"
         }}

      true ->
        tls = Keyword.take(opts, [:ca_cert, :client_cert, :client_key])
        connect(endpoint, token, opts, tls)
    end
  end

  @doc "Like `new_custom_tls/3`, but raises."
  @spec new_custom_tls!(String.t(), String.t(), keyword()) :: client()
  def new_custom_tls!(endpoint, token, opts), do: bang(new_custom_tls(endpoint, token, opts))

  @doc "Closes the connection. The client must not be used afterwards."
  @spec close(client()) :: :ok
  def close(%Client{proto_client: nil}), do: :ok
  def close(%Client{proto_client: proto}), do: SpicedbProto.Client.close(proto)

  @doc """
  Returns the `SpicedbProto.Client` this client wraps, so any RPC can be
  called directly:

      proto = SpiceDB.proto_client(client)
      Authzed.Api.V1.SchemaService.Stub.read_schema(proto.channel, %Authzed.Api.V1.ReadSchemaRequest{})

  Calls made this way bypass this module's retry, timeout, and error
  mapping; map a failure with `SpiceDB.Error.from_grpc_status/1`.
  """
  @spec proto_client(client()) :: SpicedbProto.Client.t() | nil
  def proto_client(%Client{proto_client: proto}), do: proto

  defp connect(endpoint, token, opts, proto_opts) do
    case SpicedbProto.Client.connect(endpoint, token, proto_opts) do
      {:ok, proto} ->
        {:ok,
         %Client{
           conn: proto.channel,
           proto_client: proto,
           default_timeout: Keyword.get(opts, :default_timeout, 30_000)
         }}

      {:error, %SpicedbProto.InsecureRemoteHostError{} = e} ->
        {:error, %SpiceDB.InvalidArgumentError{message: Exception.message(e)}}

      {:error, %SpicedbProto.InvalidTlsMaterialError{} = e} ->
        {:error, %SpiceDB.InvalidArgumentError{message: Exception.message(e)}}

      {:error, reason} ->
        {:error, Retry.normalize(reason)}
    end
  end

  defp certificates?(pem) do
    pem |> :public_key.pem_decode() |> Enum.any?(&match?({:Certificate, _, _}, &1))
  end

  ## Checks

  @doc """
  Checks whether `relationship`'s subject has `permission` on its resource.

  The relationship's `resource_relation` is ignored; `permission` names what
  is checked. Check-time caveat context comes from `context:` merged with the
  relationship's `check_context`, the relationship's value winning per key.

  Options: `:context`, `:timeout`.
  """
  @spec check_permission(client(), Consistency.t(), String.t(), Relationship.t(), keyword()) ::
          result(CheckResult.t())
  def check_permission(
        %Client{} = client,
        consistency,
        permission,
        %Relationship{} = rel,
        opts \\ []
      ) do
    opts = Keyword.validate!(opts, [:context, :timeout])

    with {:ok, proto_consistency} <- Wire.consistency(consistency),
         {:ok, context} <- check_context(opts, rel) do
      request = %V1.CheckPermissionRequest{
        consistency: proto_consistency,
        resource: %V1.ObjectReference{object_type: rel.resource_type, object_id: rel.resource_id},
        permission: permission,
        subject: subject_of(rel),
        context: context
      }

      with {:ok, resp} <-
             unary(client, :read, V1.PermissionsService.Service, :CheckPermission, request, opts) do
        {:ok,
         %CheckResult{
           permissionship: Wire.check_permissionship(resp.permissionship),
           missing_context: Wire.missing_context(resp.partial_caveat_info),
           checked_at: Wire.token_string(resp.checked_at)
         }}
      end
    end
  end

  @doc "Like `check_permission/5`, but raises."
  @spec check_permission!(client(), Consistency.t(), String.t(), Relationship.t(), keyword()) ::
          CheckResult.t()
  def check_permission!(client, consistency, permission, rel, opts \\ []),
    do: bang(check_permission(client, consistency, permission, rel, opts))

  @doc """
  Checks `permission` for every relationship, returning results in input
  order.

  Sent as `CheckBulkPermissions` in batches of 1 000. A failure for any single
  item fails the whole call with that item's error. Options as for
  `check_permission/5`.
  """
  @spec check_permissions(client(), Consistency.t(), String.t(), [Relationship.t()], keyword()) ::
          result([CheckResult.t()])
  def check_permissions(%Client{} = client, consistency, permission, relationships, opts \\ [])
      when is_list(relationships) do
    opts = Keyword.validate!(opts, [:context, :timeout])

    with {:ok, proto_consistency} <- Wire.consistency(consistency) do
      relationships
      |> Enum.chunk_every(@check_batch_size)
      |> Enum.reduce_while(
        {:ok, []},
        &check_next_chunk(&1, &2, client, proto_consistency, permission, opts)
      )
      |> case do
        {:ok, chunks} -> {:ok, chunks |> Enum.reverse() |> Enum.concat()}
        error -> error
      end
    end
  end

  defp check_next_chunk(chunk, {:ok, acc}, client, consistency, permission, opts) do
    case check_chunk(client, consistency, permission, chunk, opts) do
      {:ok, results} -> {:cont, {:ok, [results | acc]}}
      {:error, _} = error -> {:halt, error}
    end
  end

  @doc "Like `check_permissions/5`, but raises."
  @spec check_permissions!(client(), Consistency.t(), String.t(), [Relationship.t()], keyword()) ::
          [CheckResult.t()]
  def check_permissions!(client, consistency, permission, relationships, opts \\ []),
    do: bang(check_permissions(client, consistency, permission, relationships, opts))

  @doc """
  True when at least one relationship has `permission`. An empty list is
  `false`. A conditional result does not count.
  """
  @spec check_any(client(), Consistency.t(), String.t(), [Relationship.t()], keyword()) ::
          result(boolean())
  def check_any(client, consistency, permission, relationships, opts \\ []) do
    with {:ok, results} <- check_permissions(client, consistency, permission, relationships, opts) do
      {:ok, Enum.any?(results, &CheckResult.has_permission?/1)}
    end
  end

  @doc "Like `check_any/5`, but raises."
  @spec check_any!(client(), Consistency.t(), String.t(), [Relationship.t()], keyword()) ::
          boolean()
  def check_any!(client, consistency, permission, relationships, opts \\ []),
    do: bang(check_any(client, consistency, permission, relationships, opts))

  @doc """
  True when every relationship has `permission`. An empty list is `false`,
  never vacuously true. A conditional result does not count.
  """
  @spec check_all(client(), Consistency.t(), String.t(), [Relationship.t()], keyword()) ::
          result(boolean())
  def check_all(client, consistency, permission, relationships, opts \\ [])

  def check_all(_client, _consistency, _permission, [], _opts), do: {:ok, false}

  def check_all(client, consistency, permission, relationships, opts) do
    with {:ok, results} <- check_permissions(client, consistency, permission, relationships, opts) do
      {:ok, Enum.all?(results, &CheckResult.has_permission?/1)}
    end
  end

  @doc "Like `check_all/5`, but raises."
  @spec check_all!(client(), Consistency.t(), String.t(), [Relationship.t()], keyword()) ::
          boolean()
  def check_all!(client, consistency, permission, relationships, opts \\ []),
    do: bang(check_all(client, consistency, permission, relationships, opts))

  defp check_chunk(client, consistency, permission, chunk, opts) do
    with {:ok, items} <- build_check_items(chunk, permission, opts) do
      request = %V1.CheckBulkPermissionsRequest{consistency: consistency, items: items}
      Retry.run(client, :read, fn -> send_check_chunk(client, request, opts, length(items)) end)
    end
  end

  defp send_check_chunk(client, request, opts, expected) do
    with {:ok, resp} <-
           call(client, V1.PermissionsService.Service, :CheckBulkPermissions, request, opts) do
      bulk_results(resp, expected)
    end
  end

  defp build_check_items(chunk, permission, opts) do
    map_ok(chunk, fn rel ->
      with {:ok, context} <- check_context(opts, rel) do
        {:ok,
         %V1.CheckBulkPermissionsRequestItem{
           resource: %V1.ObjectReference{
             object_type: rel.resource_type,
             object_id: rel.resource_id
           },
           permission: permission,
           subject: subject_of(rel),
           context: context
         }}
      end
    end)
  end

  defp bulk_results(%V1.CheckBulkPermissionsResponse{pairs: pairs}, expected)
       when length(pairs) != expected do
    {:error,
     %SpiceDB.Error{
       message: "CheckBulkPermissions returned #{length(pairs)} result(s) for #{expected} item(s)"
     }}
  end

  defp bulk_results(
         %V1.CheckBulkPermissionsResponse{pairs: pairs, checked_at: checked_at},
         _expected
       ) do
    token = Wire.token_string(checked_at)

    Enum.reduce_while(pairs, {:ok, []}, fn
      %{response: {:item, item}}, {:ok, acc} ->
        result = %CheckResult{
          permissionship: Wire.check_permissionship(item.permissionship),
          missing_context: Wire.missing_context(item.partial_caveat_info),
          checked_at: token
        }

        {:cont, {:ok, [result | acc]}}

      %{response: {:error, status}}, _acc ->
        {:halt, {:error, status}}

      _malformed, _acc ->
        {:halt,
         {:error,
          %SpiceDB.Error{message: "CheckBulkPermissions pair carried neither item nor error"}}}
    end)
    |> case do
      {:ok, results} -> {:ok, Enum.reverse(results)}
      error -> error
    end
  end

  defp check_context(opts, %Relationship{check_context: item}) do
    with {:ok, merged} <- opts |> Keyword.get(:context) |> CaveatContext.merge(item) do
      CaveatContext.to_struct(merged)
    end
  end

  defp subject_of(%Relationship{} = rel) do
    %V1.SubjectReference{
      object: %V1.ObjectReference{object_type: rel.subject_type, object_id: rel.subject_id},
      optional_relation: rel.subject_relation || ""
    }
  end

  ## Relationships

  @doc """
  Applies a `SpiceDB.Transaction` atomically and returns the ZedToken it was
  written at. Never retried.

  Options: `:timeout`.
  """
  @spec write_relationships(client(), Transaction.t(), keyword()) :: result(zed_token())
  def write_relationships(%Client{} = client, %Transaction{} = txn, opts \\ []) do
    opts = Keyword.validate!(opts, [:timeout])

    with {:ok, updates} <- map_ok(txn.updates, &Wire.update/1),
         {:ok, preconditions} <- map_ok(txn.preconditions, &Wire.precondition/1) do
      request = %V1.WriteRelationshipsRequest{
        updates: updates,
        optional_preconditions: preconditions
      }

      with {:ok, resp} <-
             unary(
               client,
               :mutation,
               V1.PermissionsService.Service,
               :WriteRelationships,
               request,
               opts
             ) do
        {:ok, Wire.token_string(resp.written_at)}
      end
    end
  end

  @doc "Like `write_relationships/3`, but raises."
  @spec write_relationships!(client(), Transaction.t(), keyword()) :: zed_token()
  def write_relationships!(client, txn, opts \\ []),
    do: bang(write_relationships(client, txn, opts))

  @doc """
  Streams every relationship matching `filter`, fetching pages of 512.
  See the moduledoc for streaming semantics.
  """
  @spec read_relationships(client(), Consistency.t(), Filter.t(), keyword()) ::
          result(Enumerable.t(Relationship.t()))
  def read_relationships(%Client{} = client, consistency, %Filter{} = filter, opts \\ []) do
    Keyword.validate!(opts, [])

    with {:ok, proto_consistency} <- Wire.consistency(consistency),
         {:ok, proto_filter} <- Wire.filter(filter) do
      base = %V1.ReadRelationshipsRequest{
        consistency: proto_consistency,
        relationship_filter: proto_filter,
        optional_limit: @read_page_size
      }

      Streaming.open(client, %{
        service: V1.PermissionsService.Service,
        rpc: :ReadRelationships,
        request: &%{base | optional_cursor: &1},
        map: &[Wire.relationship_from_proto(&1.relationship)],
        cursor: & &1.after_result_cursor,
        page_size: @read_page_size
      })
    end
  end

  @doc "Like `read_relationships/4`, but raises."
  @spec read_relationships!(client(), Consistency.t(), Filter.t(), keyword()) ::
          Enumerable.t(Relationship.t())
  def read_relationships!(client, consistency, filter, opts \\ []),
    do: bang(read_relationships(client, consistency, filter, opts))

  @doc """
  Deletes every relationship matching `filter` and returns the ZedToken of
  the last deletion. Never retried.

  Deletes in pages of `limit:` (default 1 000) until none remain; the
  preconditions are re-checked on every page. A failure part-way leaves the
  pages already deleted deleted.

  Options:

    * `:must_match` - filters that must each match a relationship
    * `:must_not_match` - filters that must each match nothing
    * `:limit` - relationships deleted per request
    * `:timeout` - per request
  """
  @spec delete_relationships(client(), Filter.t(), keyword()) :: result(zed_token())
  def delete_relationships(%Client{} = client, %Filter{} = filter, opts \\ []) do
    opts = Keyword.validate!(opts, [:timeout, :limit, must_match: [], must_not_match: []])

    with {:ok, proto_filter} <- Wire.filter(filter),
         {:ok, must_match} <- map_ok(opts[:must_match], &Wire.precondition({:must_match, &1})),
         {:ok, must_not_match} <-
           map_ok(opts[:must_not_match], &Wire.precondition({:must_not_match, &1})) do
      request = %V1.DeleteRelationshipsRequest{
        relationship_filter: proto_filter,
        optional_preconditions: must_match ++ must_not_match,
        optional_limit: opts[:limit] || @delete_page_size,
        optional_allow_partial_deletions: true
      }

      delete_pages(client, request, opts)
    end
  end

  @doc "Like `delete_relationships/3`, but raises."
  @spec delete_relationships!(client(), Filter.t(), keyword()) :: zed_token()
  def delete_relationships!(client, filter, opts \\ []),
    do: bang(delete_relationships(client, filter, opts))

  defp delete_pages(client, request, opts) do
    case unary(
           client,
           :mutation,
           V1.PermissionsService.Service,
           :DeleteRelationships,
           request,
           opts
         ) do
      {:ok,
       %{
         deletion_progress: :DELETION_PROGRESS_PARTIAL,
         after_result_cursor: cursor
       }} ->
        delete_pages(client, %{request | optional_cursor: cursor}, opts)

      {:ok, resp} ->
        {:ok, Wire.token_string(resp.deleted_at)}

      error ->
        error
    end
  end

  @doc """
  Streams the resources of `resource_type` on which `subject` has
  `permission`, fetching pages of 512.

  Options: `:context`, check-time caveat context.
  """
  @spec lookup_resources(
          client(),
          Consistency.t(),
          String.t(),
          String.t(),
          SubjectRef.t(),
          keyword()
        ) ::
          result(Enumerable.t(SpiceDB.LookupResource.t()))
  def lookup_resources(
        %Client{} = client,
        consistency,
        resource_type,
        permission,
        %SubjectRef{} = subject,
        opts \\ []
      ) do
    opts = Keyword.validate!(opts, [:context])

    with {:ok, proto_consistency} <- Wire.consistency(consistency),
         {:ok, context} <- CaveatContext.to_struct(opts[:context]) do
      base = %V1.LookupResourcesRequest{
        consistency: proto_consistency,
        resource_object_type: resource_type,
        permission: permission,
        subject: Wire.subject(subject),
        context: context,
        optional_limit: @lookup_page_size
      }

      Streaming.open(client, %{
        service: V1.PermissionsService.Service,
        rpc: :LookupResources,
        request: &%{base | optional_cursor: &1},
        map: &[lookup_resource(&1)],
        cursor: & &1.after_result_cursor,
        page_size: @lookup_page_size
      })
    end
  end

  @doc "Like `lookup_resources/6`, but raises."
  @spec lookup_resources!(
          client(),
          Consistency.t(),
          String.t(),
          String.t(),
          SubjectRef.t(),
          keyword()
        ) ::
          Enumerable.t(SpiceDB.LookupResource.t())
  def lookup_resources!(client, consistency, resource_type, permission, subject, opts \\ []),
    do: bang(lookup_resources(client, consistency, resource_type, permission, subject, opts))

  defp lookup_resource(%V1.LookupResourcesResponse{} = resp) do
    %SpiceDB.LookupResource{
      resource_id: resp.resource_object_id,
      permissionship: Wire.lookup_permissionship(resp.permissionship),
      partial_caveat: Wire.partial_caveat(resp.partial_caveat_info),
      looked_up_at: Wire.token_string(resp.looked_up_at)
    }
  end

  @doc """
  Streams the subjects of `subject_type` that have `permission` on
  `resource`.

  Options:

    * `:context` - check-time caveat context
    * `:subject_relation` - only subjects reached through this relation
  """
  @spec lookup_subjects(
          client(),
          Consistency.t(),
          ObjectRef.t(),
          String.t(),
          String.t(),
          keyword()
        ) ::
          result(Enumerable.t(SpiceDB.LookupSubject.t()))
  def lookup_subjects(
        %Client{} = client,
        consistency,
        %ObjectRef{} = resource,
        permission,
        subject_type,
        opts \\ []
      ) do
    opts = Keyword.validate!(opts, [:context, :subject_relation])

    with {:ok, proto_consistency} <- Wire.consistency(consistency),
         {:ok, context} <- CaveatContext.to_struct(opts[:context]) do
      request = %V1.LookupSubjectsRequest{
        consistency: proto_consistency,
        resource: Wire.object(resource),
        permission: permission,
        subject_object_type: subject_type,
        optional_subject_relation: opts[:subject_relation] || "",
        context: context
      }

      Streaming.open(client, %{
        service: V1.PermissionsService.Service,
        rpc: :LookupSubjects,
        request: fn _cursor -> request end,
        map: &[lookup_subject(&1)]
      })
    end
  end

  @doc "Like `lookup_subjects/6`, but raises."
  @spec lookup_subjects!(
          client(),
          Consistency.t(),
          ObjectRef.t(),
          String.t(),
          String.t(),
          keyword()
        ) ::
          Enumerable.t(SpiceDB.LookupSubject.t())
  def lookup_subjects!(client, consistency, resource, permission, subject_type, opts \\ []),
    do: bang(lookup_subjects(client, consistency, resource, permission, subject_type, opts))

  defp lookup_subject(%V1.LookupSubjectsResponse{} = resp) do
    subject =
      case Wire.resolved_subject(resp.subject) do
        %SpiceDB.ResolvedSubject{subject_id: id} = resolved when id not in [nil, ""] ->
          resolved

        _absent ->
          %SpiceDB.ResolvedSubject{
            subject_id: resp.subject_object_id,
            permissionship: Wire.lookup_permissionship(resp.permissionship),
            partial_caveat: Wire.partial_caveat(resp.partial_caveat_info)
          }
      end

    excluded =
      case resp do
        %{excluded_subjects: [_ | _] = subjects} ->
          Enum.map(subjects, &Wire.resolved_subject/1)

        %{excluded_subject_ids: ids} ->
          Enum.map(ids, &%SpiceDB.ResolvedSubject{subject_id: &1})
      end

    %SpiceDB.LookupSubject{
      subject: subject,
      excluded_subjects: excluded,
      looked_up_at: Wire.token_string(resp.looked_up_at)
    }
  end

  @doc """
  Expands `permission` on `resource` into the tree of subjects that grant it.

  Options: `:timeout`.
  """
  @spec expand_permission_tree(client(), Consistency.t(), ObjectRef.t(), String.t(), keyword()) ::
          result(SpiceDB.ExpandResult.t())
  def expand_permission_tree(
        %Client{} = client,
        consistency,
        %ObjectRef{} = resource,
        permission,
        opts \\ []
      ) do
    opts = Keyword.validate!(opts, [:timeout])

    with {:ok, proto_consistency} <- Wire.consistency(consistency) do
      request = %V1.ExpandPermissionTreeRequest{
        consistency: proto_consistency,
        resource: Wire.object(resource),
        permission: permission
      }

      with {:ok, resp} <-
             unary(
               client,
               :read,
               V1.PermissionsService.Service,
               :ExpandPermissionTree,
               request,
               opts
             ) do
        {:ok,
         %SpiceDB.ExpandResult{
           tree: permission_tree(resp.tree_root),
           revision: Wire.token_string(resp.expanded_at)
         }}
      end
    end
  end

  @doc "Like `expand_permission_tree/5`, but raises."
  @spec expand_permission_tree!(client(), Consistency.t(), ObjectRef.t(), String.t(), keyword()) ::
          SpiceDB.ExpandResult.t()
  def expand_permission_tree!(client, consistency, resource, permission, opts \\ []),
    do: bang(expand_permission_tree(client, consistency, resource, permission, opts))

  @tree_operations %{
    OPERATION_UNION: :union,
    OPERATION_INTERSECTION: :intersection,
    OPERATION_EXCLUSION: :exclusion
  }

  defp permission_tree(nil), do: nil

  defp permission_tree(%V1.PermissionRelationshipTree{} = tree) do
    node = %SpiceDB.PermissionTree{
      expanded_object: Wire.object_from_proto(tree.expanded_object),
      expanded_relation: tree.expanded_relation
    }

    case tree.tree_type do
      {:intermediate, set} ->
        %{
          node
          | intermediate: %SpiceDB.IntermediateNode{
              operation: Map.get(@tree_operations, set.operation, :unspecified),
              children: Enum.map(set.children, &permission_tree/1)
            }
        }

      {:leaf, set} ->
        %{
          node
          | leaf: %SpiceDB.LeafNode{subjects: Enum.map(set.subjects, &Wire.subject_from_proto/1)}
        }

      nil ->
        node
    end
  end

  ## Bulk

  @doc """
  Loads relationships in one client-streaming call, in batches of 1 000, and
  returns how many the server loaded. `relationships` may be any enumerable,
  including a lazy one. Never retried.

  The import is all-or-nothing: an existing relationship fails the whole
  call with `SpiceDB.AlreadyExistsError`.

  Options: `:timeout`, in milliseconds. Unbounded by default, since a large
  import legitimately runs longer than any unary default.
  """
  @spec import_relationships(client(), Enumerable.t(Relationship.t()), keyword()) ::
          result(non_neg_integer())
  def import_relationships(%Client{} = client, relationships, opts \\ []) do
    opts = Keyword.validate!(opts, timeout: :infinity)

    batches =
      relationships
      |> Stream.map(&Wire.relationship/1)
      |> Stream.chunk_every(@import_batch_size)

    Retry.run(client, :mutation, fn -> import_batches(client, batches, opts[:timeout]) end)
  end

  defp import_batches(client, batches, timeout) do
    with {:ok, resp} <-
           client_stream(
             client.conn,
             V1.PermissionsService.Service,
             :ImportBulkRelationships,
             batches,
             timeout: timeout
           ) do
      {:ok, resp.num_loaded}
    end
  end

  defp client_stream(conn, service, rpc, chunks, opts) do
    {timeout, stub_opts} = Keyword.pop(opts, :timeout, :infinity)
    deadline = deadline(timeout)
    stub_opts = if timeout == :infinity, do: stub_opts, else: [{:timeout, timeout} | stub_opts]
    stream = apply(stub_module(service), stub_function(rpc), [conn, stub_opts])

    case send_chunks(stream, chunks) do
      :ok ->
        GRPC.Stub.end_stream(stream)
        await_client_stream(stream, deadline)

      {:error, _} = error ->
        GRPC.Stub.cancel(stream)
        error
    end
  end

  defp send_chunks(stream, chunks) do
    Enum.reduce_while(chunks, :ok, fn chunk, :ok ->
      case map_ok(chunk, & &1) do
        {:ok, relationships} ->
          GRPC.Stub.send_request(stream, %V1.ImportBulkRelationshipsRequest{
            relationships: relationships
          })

          {:cont, :ok}

        {:error, _} = error ->
          {:halt, error}
      end
    end)
  end

  defp await_client_stream(stream, :infinity), do: GRPC.Stub.recv(stream)

  defp await_client_stream(stream, deadline) do
    task = Task.async(fn -> GRPC.Stub.recv(stream) end)
    remaining = max(deadline - System.monotonic_time(:millisecond), 0)

    case Task.yield(task, remaining) || Task.shutdown(task, :brutal_kill) do
      {:ok, result} ->
        result

      _timed_out ->
        GRPC.Stub.cancel(stream)

        {:error,
         GRPC.RPCError.exception(GRPC.Status.deadline_exceeded(), "client-side deadline exceeded")}
    end
  end

  defp deadline(:infinity), do: :infinity
  defp deadline(ms) when is_integer(ms), do: System.monotonic_time(:millisecond) + ms

  @doc "Like `import_relationships/3`, but raises."
  @spec import_relationships!(client(), Enumerable.t(Relationship.t()), keyword()) ::
          non_neg_integer()
  def import_relationships!(client, relationships, opts \\ []),
    do: bang(import_relationships(client, relationships, opts))

  @doc """
  Streams every relationship, or those matching `filter:`, in pages of 512.

  Options: `:filter`, a `SpiceDB.Filter`.
  """
  @spec export_relationships(client(), Consistency.t(), keyword()) ::
          result(Enumerable.t(Relationship.t()))
  def export_relationships(%Client{} = client, consistency, opts \\ []) do
    opts = Keyword.validate!(opts, [:filter])

    with {:ok, proto_consistency} <- Wire.consistency(consistency),
         {:ok, proto_filter} <- optional_filter(opts[:filter]) do
      base = %V1.ExportBulkRelationshipsRequest{
        consistency: proto_consistency,
        optional_limit: @export_page_size,
        optional_relationship_filter: proto_filter
      }

      Streaming.open(client, %{
        service: V1.PermissionsService.Service,
        rpc: :ExportBulkRelationships,
        request: &%{base | optional_cursor: &1},
        map: &Enum.map(&1.relationships, fn rel -> Wire.relationship_from_proto(rel) end),
        cursor: & &1.after_result_cursor,
        page_size: @export_page_size
      })
    end
  end

  defp optional_filter(nil), do: {:ok, nil}
  defp optional_filter(filter), do: Wire.filter(filter)

  @doc "Like `export_relationships/3`, but raises."
  @spec export_relationships!(client(), Consistency.t(), keyword()) ::
          Enumerable.t(Relationship.t())
  def export_relationships!(client, consistency, opts \\ []),
    do: bang(export_relationships(client, consistency, opts))

  ## Watch

  @doc """
  Streams relationship changes on `object_types` (all types when `[]`).

  The stream is open-ended: it ends only when halted or when the server
  closes it. It is never retried or resumed automatically; to resume, start a
  new watch at the last event's `changes_through`.

  Unlike the other streams, a watch returns `{:ok, stream}` without waiting
  for the server, which sends nothing until the first change. A request the
  server rejects outright (a bad revision, say) comes back as `{:error, _}`
  from this call instead; a failure the server discovers only after accepting
  the stream raises on first enumeration.

  Options:

    * `:start_revision` - a ZedToken to start after
    * `:include_checkpoints` - also emit checkpoint events
    * `:filters` - `SpiceDB.Filter`s, in place of or alongside `object_types`
  """
  @spec watch(client(), [String.t()], keyword()) :: result(Enumerable.t(SpiceDB.WatchEvent.t()))
  def watch(%Client{} = client, object_types, opts \\ []) when is_list(object_types) do
    opts = Keyword.validate!(opts, [:start_revision, include_checkpoints: false, filters: []])

    with {:ok, filters} <- map_ok(opts[:filters], &Wire.filter/1) do
      request = %V1.WatchRequest{
        optional_object_types: object_types,
        optional_start_cursor: Wire.token(opts[:start_revision]),
        optional_relationship_filters: filters,
        optional_update_kinds:
          if(opts[:include_checkpoints],
            do: [:WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES, :WATCH_KIND_INCLUDE_CHECKPOINTS],
            else: []
          )
      }

      Streaming.open(client, %{
        service: V1.WatchService.Service,
        rpc: :Watch,
        request: fn _cursor -> request end,
        map: &[watch_event(&1)],
        retry: false,
        prefetch: false
      })
    end
  end

  @doc "Like `watch/3`, but raises."
  @spec watch!(client(), [String.t()], keyword()) :: Enumerable.t(SpiceDB.WatchEvent.t())
  def watch!(client, object_types, opts \\ []), do: bang(watch(client, object_types, opts))

  @update_operations %{
    OPERATION_CREATE: :create,
    OPERATION_TOUCH: :touch,
    OPERATION_DELETE: :delete
  }

  defp watch_event(%V1.WatchResponse{} = resp) do
    %SpiceDB.WatchEvent{
      updates:
        Enum.map(resp.updates, fn update ->
          %SpiceDB.Update{
            operation: Map.get(@update_operations, update.operation, :unspecified),
            relationship: Wire.relationship_from_proto(update.relationship)
          }
        end),
      changes_through: Wire.token_string(resp.changes_through),
      is_checkpoint: resp.is_checkpoint
    }
  end

  ## Schema

  @doc """
  Returns the current schema text and the ZedToken it was read at.

  Options: `:timeout`.
  """
  @spec read_schema(client(), keyword()) :: result({String.t(), zed_token()})
  def read_schema(%Client{} = client, opts \\ []) do
    opts = Keyword.validate!(opts, [:timeout])

    with {:ok, resp} <-
           unary(
             client,
             :read,
             V1.SchemaService.Service,
             :ReadSchema,
             %V1.ReadSchemaRequest{},
             opts
           ) do
      {:ok, {resp.schema_text, Wire.token_string(resp.read_at)}}
    end
  end

  @doc "Like `read_schema/2`, but raises."
  @spec read_schema!(client(), keyword()) :: {String.t(), zed_token()}
  def read_schema!(client, opts \\ []), do: bang(read_schema(client, opts))

  @doc """
  Replaces the schema and returns the ZedToken it was written at. Never
  retried.

  Options: `:timeout`.
  """
  @spec write_schema(client(), String.t(), keyword()) :: result(zed_token())
  def write_schema(%Client{} = client, schema, opts \\ []) when is_binary(schema) do
    opts = Keyword.validate!(opts, [:timeout])
    request = %V1.WriteSchemaRequest{schema: schema}

    with {:ok, resp} <-
           unary(client, :mutation, V1.SchemaService.Service, :WriteSchema, request, opts) do
      {:ok, Wire.token_string(resp.written_at)}
    end
  end

  @doc "Like `write_schema/3`, but raises."
  @spec write_schema!(client(), String.t(), keyword()) :: zed_token()
  def write_schema!(client, schema, opts \\ []), do: bang(write_schema(client, schema, opts))

  @doc """
  Returns the schema as structured definitions and caveats.

  Options: `:timeout`.
  """
  @spec reflect_schema(client(), Consistency.t(), keyword()) ::
          result(SpiceDB.ReflectSchemaResult.t())
  def reflect_schema(%Client{} = client, consistency, opts \\ []) do
    opts = Keyword.validate!(opts, [:timeout])

    with {:ok, proto_consistency} <- Wire.consistency(consistency) do
      request = %V1.ReflectSchemaRequest{consistency: proto_consistency}

      with {:ok, resp} <-
             unary(client, :read, V1.SchemaService.Service, :ReflectSchema, request, opts) do
        {:ok,
         %SpiceDB.ReflectSchemaResult{
           definitions: Enum.map(resp.definitions, &schema_definition/1),
           caveats: Enum.map(resp.caveats, &schema_caveat/1),
           revision: Wire.token_string(resp.read_at)
         }}
      end
    end
  end

  @doc "Like `reflect_schema/3`, but raises."
  @spec reflect_schema!(client(), Consistency.t(), keyword()) :: SpiceDB.ReflectSchemaResult.t()
  def reflect_schema!(client, consistency, opts \\ []),
    do: bang(reflect_schema(client, consistency, opts))

  defp schema_definition(%V1.ReflectionDefinition{} = d) do
    %SpiceDB.SchemaDefinition{
      name: d.name,
      comment: d.comment,
      relations:
        Enum.map(d.relations, fn r ->
          %SpiceDB.SchemaRelation{
            name: r.name,
            comment: r.comment,
            parent_definition_name: r.parent_definition_name
          }
        end),
      permissions:
        Enum.map(d.permissions, fn p ->
          %SpiceDB.SchemaPermission{
            name: p.name,
            comment: p.comment,
            parent_definition_name: p.parent_definition_name
          }
        end)
    }
  end

  defp schema_caveat(%V1.ReflectionCaveat{} = c) do
    %SpiceDB.SchemaCaveat{
      name: c.name,
      comment: c.comment,
      expression: c.expression,
      parameters:
        Enum.map(c.parameters, fn p ->
          %SpiceDB.SchemaCaveatParameter{
            name: p.name,
            type: p.type,
            parent_caveat_name: p.parent_caveat_name
          }
        end)
    }
  end

  @doc """
  Lists the permissions and relations that `relation_name` on
  `definition_name` can contribute to.

  Options: `:definition_filter`, only report permissions on this
  definition; `:timeout`.
  """
  @spec computable_permissions(client(), Consistency.t(), String.t(), String.t(), keyword()) ::
          result([SpiceDB.RelationReference.t()])
  def computable_permissions(
        %Client{} = client,
        consistency,
        definition_name,
        relation_name,
        opts \\ []
      ) do
    opts = Keyword.validate!(opts, [:timeout, :definition_filter])

    with {:ok, proto_consistency} <- Wire.consistency(consistency) do
      request = %V1.ComputablePermissionsRequest{
        consistency: proto_consistency,
        definition_name: definition_name,
        relation_name: relation_name,
        optional_definition_name_filter: opts[:definition_filter] || ""
      }

      with {:ok, resp} <-
             unary(
               client,
               :read,
               V1.SchemaService.Service,
               :ComputablePermissions,
               request,
               opts
             ) do
        {:ok, Enum.map(resp.permissions, &relation_reference/1)}
      end
    end
  end

  @doc "Like `computable_permissions/5`, but raises."
  @spec computable_permissions!(client(), Consistency.t(), String.t(), String.t(), keyword()) ::
          [SpiceDB.RelationReference.t()]
  def computable_permissions!(client, consistency, definition_name, relation_name, opts \\ []),
    do: bang(computable_permissions(client, consistency, definition_name, relation_name, opts))

  @doc """
  Lists the relations and permissions that `permission_name` on
  `definition_name` depends on.

  Options: `:timeout`.
  """
  @spec dependent_relations(client(), Consistency.t(), String.t(), String.t(), keyword()) ::
          result([SpiceDB.RelationReference.t()])
  def dependent_relations(
        %Client{} = client,
        consistency,
        definition_name,
        permission_name,
        opts \\ []
      ) do
    opts = Keyword.validate!(opts, [:timeout])

    with {:ok, proto_consistency} <- Wire.consistency(consistency) do
      request = %V1.DependentRelationsRequest{
        consistency: proto_consistency,
        definition_name: definition_name,
        permission_name: permission_name
      }

      with {:ok, resp} <-
             unary(client, :read, V1.SchemaService.Service, :DependentRelations, request, opts) do
        {:ok, Enum.map(resp.relations, &relation_reference/1)}
      end
    end
  end

  @doc "Like `dependent_relations/5`, but raises."
  @spec dependent_relations!(client(), Consistency.t(), String.t(), String.t(), keyword()) ::
          [SpiceDB.RelationReference.t()]
  def dependent_relations!(client, consistency, definition_name, permission_name, opts \\ []),
    do: bang(dependent_relations(client, consistency, definition_name, permission_name, opts))

  defp relation_reference(%V1.ReflectionRelationReference{} = r) do
    %SpiceDB.RelationReference{
      definition_name: r.definition_name,
      relation_name: r.relation_name,
      is_permission: r.is_permission
    }
  end

  @doc """
  Diffs the current schema against `comparison_schema`.

  Options: `:timeout`.
  """
  @spec diff_schema(client(), Consistency.t(), String.t(), keyword()) ::
          result([SpiceDB.SchemaDiff.t()])
  def diff_schema(%Client{} = client, consistency, comparison_schema, opts \\ []) do
    opts = Keyword.validate!(opts, [:timeout])

    with {:ok, proto_consistency} <- Wire.consistency(consistency) do
      request = %V1.DiffSchemaRequest{
        consistency: proto_consistency,
        comparison_schema: comparison_schema
      }

      with {:ok, resp} <-
             unary(client, :read, V1.SchemaService.Service, :DiffSchema, request, opts) do
        {:ok, Enum.map(resp.diffs, &schema_diff/1)}
      end
    end
  end

  @doc "Like `diff_schema/4`, but raises."
  @spec diff_schema!(client(), Consistency.t(), String.t(), keyword()) :: [SpiceDB.SchemaDiff.t()]
  def diff_schema!(client, consistency, comparison_schema, opts \\ []),
    do: bang(diff_schema(client, consistency, comparison_schema, opts))

  @definition_diffs [:definition_added, :definition_removed, :definition_doc_comment_changed]
  @relation_diffs [:relation_added, :relation_removed, :relation_doc_comment_changed]
  @subject_type_diffs [:relation_subject_type_added, :relation_subject_type_removed]
  @permission_diffs [
    :permission_added,
    :permission_removed,
    :permission_doc_comment_changed,
    :permission_expr_changed
  ]
  @caveat_diffs [
    :caveat_added,
    :caveat_removed,
    :caveat_doc_comment_changed,
    :caveat_expr_changed
  ]
  @parameter_diffs [:caveat_parameter_added, :caveat_parameter_removed]

  defp schema_diff(%V1.ReflectionSchemaDiff{diff: {kind, v}}) when kind in @definition_diffs,
    do: %SpiceDB.SchemaDiff{kind: kind, definition_name: v.name}

  defp schema_diff(%V1.ReflectionSchemaDiff{diff: {kind, v}}) when kind in @relation_diffs,
    do: %SpiceDB.SchemaDiff{
      kind: kind,
      definition_name: v.parent_definition_name,
      relation_name: v.name
    }

  defp schema_diff(%V1.ReflectionSchemaDiff{diff: {kind, v}}) when kind in @subject_type_diffs do
    %SpiceDB.SchemaDiff{
      kind: kind,
      definition_name: v.relation.parent_definition_name,
      relation_name: v.relation.name
    }
  end

  defp schema_diff(%V1.ReflectionSchemaDiff{diff: {kind, v}}) when kind in @permission_diffs,
    do: %SpiceDB.SchemaDiff{
      kind: kind,
      definition_name: v.parent_definition_name,
      permission_name: v.name
    }

  defp schema_diff(%V1.ReflectionSchemaDiff{diff: {kind, v}}) when kind in @caveat_diffs,
    do: %SpiceDB.SchemaDiff{kind: kind, caveat_name: v.name}

  defp schema_diff(%V1.ReflectionSchemaDiff{diff: {kind, v}}) when kind in @parameter_diffs,
    do: %SpiceDB.SchemaDiff{kind: kind, caveat_name: v.parent_caveat_name}

  defp schema_diff(%V1.ReflectionSchemaDiff{diff: {:caveat_parameter_type_changed, v}}),
    do: %SpiceDB.SchemaDiff{
      kind: :caveat_parameter_type_changed,
      caveat_name: v.parameter.parent_caveat_name
    }

  defp schema_diff(_unknown), do: %SpiceDB.SchemaDiff{kind: :unknown}

  ## Experimental: relationship counters

  @doc """
  Experimental. Registers a named counter of the relationships matching
  `filter`. Never retried.

  Options: `:timeout`.
  """
  @spec experimental_register_relationship_counter(client(), String.t(), Filter.t(), keyword()) ::
          :ok | {:error, error()}
  def experimental_register_relationship_counter(
        %Client{} = client,
        name,
        %Filter{} = filter,
        opts \\ []
      ) do
    opts = Keyword.validate!(opts, [:timeout])

    with {:ok, proto_filter} <- Wire.filter(filter) do
      request = %V1.ExperimentalRegisterRelationshipCounterRequest{
        name: name,
        relationship_filter: proto_filter
      }

      with {:ok, _resp} <-
             unary(
               client,
               :mutation,
               V1.ExperimentalService.Service,
               :ExperimentalRegisterRelationshipCounter,
               request,
               opts
             ),
           do: :ok
    end
  end

  @doc "Like `experimental_register_relationship_counter/4`, but raises."
  @spec experimental_register_relationship_counter!(client(), String.t(), Filter.t(), keyword()) ::
          :ok
  def experimental_register_relationship_counter!(client, name, filter, opts \\ []),
    do: bang(experimental_register_relationship_counter(client, name, filter, opts))

  @doc """
  Experimental. Reads a registered counter.

  Options: `:timeout`.
  """
  @spec experimental_count_relationships(client(), String.t(), keyword()) ::
          result(SpiceDB.CountResult.t())
  def experimental_count_relationships(%Client{} = client, name, opts \\ []) do
    opts = Keyword.validate!(opts, [:timeout])
    request = %V1.ExperimentalCountRelationshipsRequest{name: name}

    with {:ok, resp} <-
           unary(
             client,
             :read,
             V1.ExperimentalService.Service,
             :ExperimentalCountRelationships,
             request,
             opts
           ) do
      case resp.counter_result do
        {:read_counter_value, value} ->
          {:ok,
           %SpiceDB.CountResult{
             relationship_count: value.relationship_count,
             revision: Wire.token_string(value.read_at)
           }}

        _still_calculating ->
          {:ok, %SpiceDB.CountResult{still_calculating: true}}
      end
    end
  end

  @doc "Like `experimental_count_relationships/3`, but raises."
  @spec experimental_count_relationships!(client(), String.t(), keyword()) ::
          SpiceDB.CountResult.t()
  def experimental_count_relationships!(client, name, opts \\ []),
    do: bang(experimental_count_relationships(client, name, opts))

  @doc """
  Experimental. Unregisters a counter. Never retried.

  Options: `:timeout`.
  """
  @spec experimental_unregister_relationship_counter(client(), String.t(), keyword()) ::
          :ok | {:error, error()}
  def experimental_unregister_relationship_counter(%Client{} = client, name, opts \\ []) do
    opts = Keyword.validate!(opts, [:timeout])
    request = %V1.ExperimentalUnregisterRelationshipCounterRequest{name: name}

    with {:ok, _resp} <-
           unary(
             client,
             :mutation,
             V1.ExperimentalService.Service,
             :ExperimentalUnregisterRelationshipCounter,
             request,
             opts
           ),
         do: :ok
  end

  @doc "Like `experimental_unregister_relationship_counter/3`, but raises."
  @spec experimental_unregister_relationship_counter!(client(), String.t(), keyword()) :: :ok
  def experimental_unregister_relationship_counter!(client, name, opts \\ []),
    do: bang(experimental_unregister_relationship_counter(client, name, opts))

  ## Experimental: materialize

  @doc """
  Experimental, materialize tier. Counts the relationships matching `filter`
  without registering a counter.

  Options: `:timeout`.
  """
  @spec experimental_count_relationships_by_filter(client(), Filter.t(), keyword()) ::
          result(SpiceDB.CountResult.t())
  def experimental_count_relationships_by_filter(
        %Client{} = client,
        %Filter{} = filter,
        opts \\ []
      ) do
    opts = Keyword.validate!(opts, [:timeout])

    with {:ok, proto_filter} <- Wire.filter(filter) do
      request = %M.ExperimentalCountRelationshipsByFilterRequest{
        relationship_filter: proto_filter
      }

      with {:ok, resp} <-
             unary(
               client,
               :read,
               M.RelationshipsService.Service,
               :ExperimentalCountRelationshipsByFilter,
               request,
               opts
             ) do
        {:ok,
         %SpiceDB.CountResult{
           relationship_count: resp.relationship_count,
           revision: Wire.token_string(resp.read_at)
         }}
      end
    end
  end

  @doc "Like `experimental_count_relationships_by_filter/3`, but raises."
  @spec experimental_count_relationships_by_filter!(client(), Filter.t(), keyword()) ::
          SpiceDB.CountResult.t()
  def experimental_count_relationships_by_filter!(client, filter, opts \\ []),
    do: bang(experimental_count_relationships_by_filter(client, filter, opts))

  @doc """
  Experimental, materialize tier. Returns the resources of `resource_type`
  on which `subject` has `permission`, as a roaring64 bitmap.

  Options: `:timeout`.
  """
  @spec experimental_roaring_lookup_resources(
          client(),
          Consistency.t(),
          String.t(),
          String.t(),
          SubjectRef.t(),
          keyword()
        ) :: result(SpiceDB.RoaringLookupResourcesResult.t())
  def experimental_roaring_lookup_resources(
        %Client{} = client,
        consistency,
        resource_type,
        permission,
        %SubjectRef{} = subject,
        opts \\ []
      ) do
    opts = Keyword.validate!(opts, [:timeout])

    with {:ok, proto_consistency} <- Wire.consistency(consistency) do
      request = %M.ExperimentalRoaringLookupResourcesRequest{
        consistency: proto_consistency,
        resource_object_type: resource_type,
        permission: permission,
        subject: Wire.subject(subject)
      }

      with {:ok, resp} <-
             unary(
               client,
               :read,
               M.RoaringLookupResourcesService.Service,
               :ExperimentalRoaringLookupResources,
               request,
               opts
             ) do
        {:ok,
         %SpiceDB.RoaringLookupResourcesResult{
           bitmap: resp.bitmap,
           cardinality: resp.cardinality,
           at_revision: Wire.token_string(resp.at_revision)
         }}
      end
    end
  end

  @doc "Like `experimental_roaring_lookup_resources/6`, but raises."
  @spec experimental_roaring_lookup_resources!(
          client(),
          Consistency.t(),
          String.t(),
          String.t(),
          SubjectRef.t(),
          keyword()
        ) :: SpiceDB.RoaringLookupResourcesResult.t()
  def experimental_roaring_lookup_resources!(
        client,
        consistency,
        resource_type,
        permission,
        subject,
        opts \\ []
      ),
      do:
        bang(
          experimental_roaring_lookup_resources(
            client,
            consistency,
            resource_type,
            permission,
            subject,
            opts
          )
        )

  @doc """
  Experimental, materialize tier. Streams changes to computed permissions.

  Items are `{:change, %SpiceDB.PermissionChange{}}` or
  `{:completed_revision, zed_token}`. Open-ended and never retried, like
  `watch/3`.

  Options: `:start_revision`.
  """
  @spec experimental_watch_permissions(client(), [SpiceDB.WatchedPermission.t()], keyword()) ::
          result(
            Enumerable.t(
              {:change, SpiceDB.PermissionChange.t()}
              | {:completed_revision, zed_token()}
            )
          )
  def experimental_watch_permissions(%Client{} = client, permissions, opts \\ [])
      when is_list(permissions) do
    opts = Keyword.validate!(opts, [:start_revision])

    request = %M.WatchPermissionsRequest{
      permissions:
        Enum.map(permissions, fn %SpiceDB.WatchedPermission{} = p ->
          %M.WatchedPermission{
            resource_type: p.resource_type,
            permission: p.permission,
            subject_type: p.subject_type,
            optional_subject_relation: p.optional_subject_relation
          }
        end),
      optional_starting_after: Wire.token(opts[:start_revision])
    }

    Streaming.open(client, %{
      service: M.WatchPermissionsService.Service,
      rpc: :WatchPermissions,
      request: fn _cursor -> request end,
      map: &[permission_change(&1)],
      retry: false,
      prefetch: false
    })
  end

  @doc "Like `experimental_watch_permissions/3`, but raises."
  @spec experimental_watch_permissions!(client(), [SpiceDB.WatchedPermission.t()], keyword()) ::
          Enumerable.t()
  def experimental_watch_permissions!(client, permissions, opts \\ []),
    do: bang(experimental_watch_permissions(client, permissions, opts))

  defp permission_change(%M.WatchPermissionsResponse{response: {:change, change}}) do
    {:change,
     %SpiceDB.PermissionChange{
       revision: Wire.token_string(change.revision),
       resource: Wire.object_from_proto(change.resource),
       permission: change.permission,
       subject: change.subject && Wire.subject_from_proto(change.subject),
       permissionship: Wire.check_permissionship(change.permissionship)
     }}
  end

  defp permission_change(%M.WatchPermissionsResponse{response: {:completed_revision, token}}),
    do: {:completed_revision, Wire.token_string(token)}

  defp permission_change(%M.WatchPermissionsResponse{}), do: {:unknown, nil}

  @doc """
  Experimental, materialize tier. Streams changes to permission sets.

  Items are `{:change, %SpiceDB.PermissionSetChange{}}`,
  `{:completed_revision, zed_token}`,
  `{:lookup_permission_sets_required, zed_token}` (rebuild from
  `experimental_lookup_permission_sets/2` at that revision) or
  `{:breaking_schema_change, zed_token}`. Open-ended and never retried.

  Options: `:start_revision`.
  """
  @spec experimental_watch_permission_sets(client(), keyword()) :: result(Enumerable.t())
  def experimental_watch_permission_sets(%Client{} = client, opts \\ []) do
    opts = Keyword.validate!(opts, [:start_revision])

    request = %M.WatchPermissionSetsRequest{
      optional_starting_after: Wire.token(opts[:start_revision])
    }

    Streaming.open(client, %{
      service: M.WatchPermissionSetsService.Service,
      rpc: :WatchPermissionSets,
      request: fn _cursor -> request end,
      map: &[permission_set_event(&1)],
      retry: false,
      prefetch: false
    })
  end

  @doc "Like `experimental_watch_permission_sets/2`, but raises."
  @spec experimental_watch_permission_sets!(client(), keyword()) :: Enumerable.t()
  def experimental_watch_permission_sets!(client, opts \\ []),
    do: bang(experimental_watch_permission_sets(client, opts))

  defp permission_set_event(%M.WatchPermissionSetsResponse{response: response}) do
    case response do
      {:change, change} ->
        {:change, permission_set_change(change)}

      {:completed_revision, token} ->
        {:completed_revision, Wire.token_string(token)}

      {:lookup_permission_sets_required, r} ->
        {:lookup_permission_sets_required, Wire.token_string(r.required_lookup_at)}

      {:breaking_schema_change, b} ->
        {:breaking_schema_change, Wire.token_string(b.change_at)}

      nil ->
        {:unknown, nil}
    end
  end

  @set_operations %{SET_OPERATION_ADDED: :added, SET_OPERATION_REMOVED: :removed}

  defp permission_set_change(%M.PermissionSetChange{} = change) do
    base = %SpiceDB.PermissionSetChange{
      at_revision: Wire.token_string(change.at_revision),
      operation: Map.get(@set_operations, change.operation, :unspecified),
      parent_set: Wire.set_reference(change.parent_set)
    }

    case change.child do
      {:child_set, set} ->
        %{base | child_set: Wire.set_reference(set)}

      {:child_member, member} ->
        %{
          base
          | child_member: %SpiceDB.MemberReference{
              object_type: member.object_type,
              object_id: member.object_id,
              optional_permission_or_relation: member.optional_permission_or_relation
            }
        }

      nil ->
        base
    end
  end

  @doc """
  Experimental, materialize tier. Streams the full set of permission-set
  memberships, each paired with the cursor to resume after it.

  Items are `{%SpiceDB.PermissionSetChange{}, %SpiceDB.PermissionSetsCursor{}}`.

  Options:

    * `:limit` - memberships per response page, as the server defines it
    * `:at_revision` - a ZedToken to read at
    * `:after` - a `SpiceDB.PermissionSetsCursor` to resume after
  """
  @spec experimental_lookup_permission_sets(client(), keyword()) ::
          result(
            Enumerable.t({SpiceDB.PermissionSetChange.t(), SpiceDB.PermissionSetsCursor.t()})
          )
  def experimental_lookup_permission_sets(%Client{} = client, opts \\ []) do
    opts = Keyword.validate!(opts, [:limit, :at_revision, :after])

    request = %M.LookupPermissionSetsRequest{
      limit: opts[:limit] || 0,
      optional_at_revision: Wire.token(opts[:at_revision]),
      optional_starting_after_cursor:
        case opts[:after] do
          %SpiceDB.PermissionSetsCursor{proto: proto} -> proto
          nil -> nil
        end
    }

    Streaming.open(client, %{
      service: M.WatchPermissionSetsService.Service,
      rpc: :LookupPermissionSets,
      request: fn _cursor -> request end,
      map: fn resp ->
        [{permission_set_change(resp.change), %SpiceDB.PermissionSetsCursor{proto: resp.cursor}}]
      end
    })
  end

  @doc "Like `experimental_lookup_permission_sets/2`, but raises."
  @spec experimental_lookup_permission_sets!(client(), keyword()) :: Enumerable.t()
  def experimental_lookup_permission_sets!(client, opts \\ []),
    do: bang(experimental_lookup_permission_sets(client, opts))

  @doc """
  Experimental, materialize tier. Returns download locations for a snapshot
  of every permission set.

  Options: `:at_revision`, `:timeout`.
  """
  @spec experimental_download_permission_sets(client(), keyword()) ::
          result(SpiceDB.PermissionSetsDownload.t())
  def experimental_download_permission_sets(%Client{} = client, opts \\ []) do
    opts = Keyword.validate!(opts, [:at_revision, :timeout])

    request = %M.DownloadPermissionSetsRequest{
      optional_at_revision: Wire.token(opts[:at_revision])
    }

    with {:ok, resp} <-
           unary(
             client,
             :read,
             M.WatchPermissionSetsService.Service,
             :DownloadPermissionSets,
             request,
             opts
           ) do
      {:ok,
       %SpiceDB.PermissionSetsDownload{
         files: Enum.map(resp.files, &%{name: &1.name, url: &1.url}),
         timestamp: Wire.datetime(resp.timestamp),
         at_revision: Wire.token_string(resp.at_revision)
       }}
    end
  end

  @doc "Like `experimental_download_permission_sets/2`, but raises."
  @spec experimental_download_permission_sets!(client(), keyword()) ::
          SpiceDB.PermissionSetsDownload.t()
  def experimental_download_permission_sets!(client, opts \\ []),
    do: bang(experimental_download_permission_sets(client, opts))

  ## Internals

  defp unary(client, kind, service, rpc, request, opts) do
    Retry.run(client, kind, fn -> call(client, service, rpc, request, opts) end)
  end

  defp call(client, service, rpc, request, opts) do
    timeout = Keyword.get(opts, :timeout) || client.default_timeout
    apply(stub_module(service), stub_function(rpc), [client.conn, request, [timeout: timeout]])
  end

  defp stub_module(service) do
    service
    |> Module.split()
    |> List.replace_at(-1, "Stub")
    |> Module.concat()
  end

  defp stub_function(rpc) do
    rpc
    |> Atom.to_string()
    |> Macro.underscore()
    |> String.to_existing_atom()
  end

  defp bang(:ok), do: :ok
  defp bang({:ok, value}), do: value
  defp bang({:error, error}), do: raise(error)

  defp map_ok(list, fun) do
    list
    |> Enum.reduce_while({:ok, []}, fn item, {:ok, acc} ->
      case fun.(item) do
        {:ok, value} -> {:cont, {:ok, [value | acc]}}
        {:error, _} = error -> {:halt, error}
      end
    end)
    |> case do
      {:ok, values} -> {:ok, Enum.reverse(values)}
      error -> error
    end
  end
end
