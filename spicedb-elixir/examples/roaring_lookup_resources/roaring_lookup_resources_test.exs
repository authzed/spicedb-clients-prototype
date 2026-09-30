defmodule SpiceDB.Examples.RoaringLookupResourcesTest do
  use SpiceDB.ExampleCase, async: false

  alias Authzed.Api.Materialize.V0, as: M
  alias Authzed.Api.V1
  alias SpiceDB.Examples.StandIn

  defmodule StandInService do
    use GRPC.Server, service: Authzed.Api.Materialize.V0.RoaringLookupResourcesService.Service

    def experimental_roaring_lookup_resources(request, _stream) do
      :persistent_term.put(__MODULE__, request)

      if request.permission == "non-canonical-ids" do
        raise GRPC.RPCError.exception(
                status: :failed_precondition,
                message: "resource object id \"007\" is not a canonical 44-bit integer"
              )
      end

      %M.ExperimentalRoaringLookupResourcesResponse{
        bitmap: <<1, 2, 3, 4>>,
        cardinality: 2,
        at_revision: %V1.ZedToken{token: "rev-1"}
      }
    end

    def last_request, do: :persistent_term.get(__MODULE__)
  end

  defp stand_in_client do
    port = StandIn.start!([StandInService])
    client = SpiceDB.new_plaintext!("127.0.0.1:#{port}", token())
    on_exit(fn -> SpiceDB.close(client) end)
    client
  end

  @tag :no_spicedb
  test "returns the bitmap, cardinality, and revision, and sends the request it built" do
    client = stand_in_client()

    assert {:ok, %SpiceDB.RoaringLookupResourcesResult{} = result} =
             SpiceDB.experimental_roaring_lookup_resources(
               client,
               Consistency.full(),
               "document",
               "view",
               SubjectRef.new("user", "alice")
             )

    assert result.bitmap == <<1, 2, 3, 4>>
    assert result.cardinality == 2
    assert result.at_revision == "rev-1"

    request = StandInService.last_request()
    assert request.resource_object_type == "document"
    assert request.permission == "view"
    assert request.subject.object.object_type == "user"
    assert request.subject.object.object_id == "alice"
    assert request.consistency.requirement == {:fully_consistent, true}
  end

  @tag :no_spicedb
  test "maps a non-canonical resource id to FailedPreconditionError" do
    client = stand_in_client()

    assert {:error, %SpiceDB.FailedPreconditionError{message: message}} =
             SpiceDB.experimental_roaring_lookup_resources(
               client,
               Consistency.full(),
               "document",
               "non-canonical-ids",
               SubjectRef.new("user", "alice")
             )

    assert message =~ "canonical"
  end

  test "surfaces a SpiceDB that does not serve the materialize tier as a typed error",
       %{client: client} do
    assert {:error, error} =
             SpiceDB.experimental_roaring_lookup_resources(
               client,
               Consistency.full(),
               "document",
               "view",
               SubjectRef.new("user", "alice")
             )

    assert %SpiceDB.Error{code: 12} = error
  end
end
