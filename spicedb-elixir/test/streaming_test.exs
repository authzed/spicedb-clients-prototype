defmodule SpiceDB.StreamingTest do
  # grpc-elixir's Mint adapter is unreliable under many concurrent short-lived
  # connections; serialize this file rather than risk flaky failures.
  use ExUnit.Case, async: false

  alias Authzed.Api.V1
  alias SpiceDB.{Consistency, Filter, ObjectRef, SubjectRef}
  alias SpiceDB.Test.Forwarder

  defp rel_msg(i, cursor \\ nil) do
    %V1.ReadRelationshipsResponse{
      relationship: %V1.Relationship{
        resource: %V1.ObjectReference{object_type: "document", object_id: "#{i}"},
        relation: "viewer",
        subject: %V1.SubjectReference{
          object: %V1.ObjectReference{object_type: "user", object_id: "u"}
        }
      },
      after_result_cursor: cursor && %V1.Cursor{token: cursor}
    }
  end

  defp paged(total, page) do
    fn :ReadRelationships, req ->
      offset = if req.optional_cursor, do: String.to_integer(req.optional_cursor.token), else: 0
      last = min(offset + page, total)
      {:stream, for(i <- (offset + 1)..last//1, do: rel_msg(i, "#{i}"))}
    end
  end

  defp read_client(handler) do
    Forwarder.client!(
      V1.PermissionsService.Service,
      %{ReadRelationships: :server_stream},
      handler
    )
  end

  defp watch_client(handler) do
    Forwarder.client!(V1.WatchService.Service, %{Watch: :server_stream}, handler)
  end

  defp lookup_resources_client(handler) do
    Forwarder.client!(
      V1.PermissionsService.Service,
      %{LookupResources: :server_stream},
      handler
    )
  end

  defp lookup_subjects_client(handler) do
    Forwarder.client!(
      V1.PermissionsService.Service,
      %{LookupSubjects: :server_stream},
      handler
    )
  end

  test "pages with the cursor at 512 until a short page" do
    client = read_client(paged(1100, 512))
    {:ok, stream} = SpiceDB.read_relationships(client, Consistency.full(), Filter.new("document"))
    ids = Enum.map(stream, & &1.resource_id)
    assert ids == Enum.map(1..1100, &to_string/1)

    cursors =
      for _ <- 1..3 do
        assert_received {:open_stream, :ReadRelationships, req}
        assert req.optional_limit == 512
        req.optional_cursor && req.optional_cursor.token
      end

    assert cursors == [nil, "512", "1024"]
    refute_received {:open_stream, _, _}
  end

  test "an exact multiple of the page size ends on an empty page" do
    client = read_client(paged(512, 512))
    {:ok, stream} = SpiceDB.read_relationships(client, Consistency.full(), Filter.new("document"))
    assert Enum.count(stream) == 512
  end

  # grpc-elixir's generated Stub gives the caller no handle to cancel a
  # server-streaming call once it's been returned as an enumerable, so
  # abandoning the stream early has no observable release signal; this test
  # only checks the part that is still observable: pulling stops, and no
  # second page gets opened. See spicedb-elixir/DESIGN.md's "Stream
  # lifecycle" section.
  test "halting early stops pulling further pages" do
    client = read_client(paged(1100, 512))
    {:ok, stream} = SpiceDB.read_relationships(client, Consistency.full(), Filter.new("document"))
    assert length(Enum.take(stream, 3)) == 3
    assert_received {:open_stream, :ReadRelationships, _}
    refute_received {:open_stream, _, _}
  end

  test "an error before the first message is returned, not raised" do
    client =
      read_client(fn _, _ ->
        {:stream, [{:error, %GRPC.RPCError{status: 9, message: "no"}}]}
      end)

    assert {:error, %SpiceDB.FailedPreconditionError{}} =
             SpiceDB.read_relationships(client, Consistency.full(), Filter.new("document"))
  end

  test "an error after establishment raises from enumeration" do
    client =
      read_client(fn _, _ ->
        {:stream, [rel_msg(1), {:error, %GRPC.RPCError{status: 11, message: "late"}}]}
      end)

    {:ok, stream} = SpiceDB.read_relationships(client, Consistency.full(), Filter.new("document"))
    assert_raise SpiceDB.OutOfRangeError, "late", fn -> Enum.to_list(stream) end
  end

  test "enumerating twice issues the request again" do
    client = read_client(paged(3, 512))
    {:ok, stream} = SpiceDB.read_relationships(client, Consistency.full(), Filter.new("document"))
    assert Enum.count(stream) == 3
    assert Enum.count(stream) == 3
    assert_received {:open_stream, :ReadRelationships, _}
    assert_received {:open_stream, :ReadRelationships, _}
  end

  test "watch returns the stream without reading, so a server error raises on enumeration" do
    client =
      watch_client(fn :Watch, _ ->
        {:stream, [{:error, %GRPC.RPCError{status: 3, message: "bad revision"}}]}
      end)

    assert {:ok, stream} = SpiceDB.watch(client, [])
    assert_raise SpiceDB.InvalidArgumentError, "bad revision", fn -> Enum.to_list(stream) end
  end

  test "watch sends revision, types and update kinds" do
    client =
      watch_client(fn :Watch, _ ->
        {:stream,
         [%V1.WatchResponse{changes_through: %V1.ZedToken{token: "t"}, is_checkpoint: true}]}
      end)

    {:ok, stream} =
      SpiceDB.watch(client, ["document"], start_revision: "r0", include_checkpoints: true)

    assert [%SpiceDB.WatchEvent{changes_through: "t", is_checkpoint: true}] = Enum.to_list(stream)
    assert_received {:open_stream, :Watch, req}
    assert req.optional_start_cursor == %V1.ZedToken{token: "r0"}
    assert req.optional_object_types == ["document"]
    assert :WATCH_KIND_INCLUDE_CHECKPOINTS in req.optional_update_kinds
  end

  test "lookup_resources maps results and pages" do
    client =
      lookup_resources_client(fn :LookupResources, req ->
        assert req.subject.object.object_id == "alice"

        {:stream,
         [
           %V1.LookupResourcesResponse{
             resource_object_id: "d1",
             permissionship: :LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
             partial_caveat_info: %V1.PartialCaveatInfo{missing_required_context: ["now"]},
             looked_up_at: %V1.ZedToken{token: "t"}
           }
         ]}
      end)

    {:ok, stream} =
      SpiceDB.lookup_resources(
        client,
        Consistency.full(),
        "document",
        "view",
        SubjectRef.new("user", "alice")
      )

    assert [
             %SpiceDB.LookupResource{
               resource_id: "d1",
               permissionship: :conditional_permission,
               looked_up_at: "t"
             } = r
           ] =
             Enum.to_list(stream)

    assert r.partial_caveat.missing_required_context == ["now"]
  end

  test "lookup_subjects falls back to the deprecated fields when the subject is absent" do
    client =
      lookup_subjects_client(fn :LookupSubjects, _ ->
        {:stream,
         [
           %V1.LookupSubjectsResponse{
             subject_object_id: "*",
             permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
             excluded_subject_ids: ["bob"]
           },
           %V1.LookupSubjectsResponse{
             subject: %V1.ResolvedSubject{
               subject_object_id: "carol",
               permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION
             },
             excluded_subjects: [%V1.ResolvedSubject{subject_object_id: "dan"}]
           }
         ]}
      end)

    {:ok, stream} =
      SpiceDB.lookup_subjects(
        client,
        Consistency.full(),
        ObjectRef.new("document", "d"),
        "view",
        "user"
      )

    [wild, carol] = Enum.to_list(stream)
    assert wild.subject.subject_id == "*"
    assert wild.subject.permissionship == :has_permission
    assert Enum.map(wild.excluded_subjects, & &1.subject_id) == ["bob"]
    assert carol.subject.subject_id == "carol"
    assert Enum.map(carol.excluded_subjects, & &1.subject_id) == ["dan"]
  end
end
