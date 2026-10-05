defmodule SpiceDB.Examples.RawEscapeHatchTest do
  use SpiceDB.ExampleCase, async: false

  alias Authzed.Api.V1
  alias Google.Protobuf.{Struct, Value}

  @watch_timeout 30_000

  test "sends a proto field the idiomatic API does not expose, and reads it back",
       %{client: client} do
    seed =
      Transaction.touch(
        Transaction.new(),
        Relationship.from_tuple!("document:ledger#viewer@user:seed")
      )

    seed_revision = SpiceDB.write_relationships!(client, seed)

    %SpicedbProto.Client{channel: channel} = SpiceDB.proto_client(client)

    metadata = %Struct{
      fields: %{
        "correlation_id" => %Value{kind: {:string_value, "example-42"}},
        "actor" => %Value{kind: {:string_value, "billing-job"}}
      }
    }

    {:ok, written} =
      V1.PermissionsService.Stub.write_relationships(channel, %V1.WriteRelationshipsRequest{
        updates: [
          %V1.RelationshipUpdate{
            operation: :OPERATION_TOUCH,
            relationship: %V1.Relationship{
              resource: %V1.ObjectReference{object_type: "document", object_id: "ledger"},
              relation: "viewer",
              subject: %V1.SubjectReference{
                object: %V1.ObjectReference{object_type: "user", object_id: "jimmy"}
              }
            }
          }
        ],
        optional_transaction_metadata: metadata
      })

    revision = written.written_at.token
    assert revision != ""

    seen =
      within(@watch_timeout, fn ->
        {:ok, replies} =
          V1.WatchService.Stub.watch(channel, %V1.WatchRequest{
            optional_object_types: ["document"],
            optional_start_cursor: %V1.ZedToken{token: seed_revision}
          })

        Enum.find_value(replies, fn
          {:ok, %V1.WatchResponse{optional_transaction_metadata: %Struct{fields: fields}}}
          when map_size(fields) > 0 ->
            Map.new(fields, fn {key, %Value{kind: {:string_value, v}}} -> {key, v} end)

          {:ok, _other} ->
            nil

          {:error, error} ->
            raise SpiceDB.Error.from_grpc_status(error)
        end)
      end)

    assert seen == %{"correlation_id" => "example-42", "actor" => "billing-job"}

    rel = Relationship.from_tuple!("document:ledger#viewer@user:jimmy")

    assert SpiceDB.check_permission!(client, Consistency.at_least(revision), "view", rel)
           |> CheckResult.has_permission?()
  end

  test "calls an RPC directly and maps its raw error with from_grpc_status/1", %{client: client} do
    SpiceDB.write_relationships!(
      client,
      Transaction.touch(
        Transaction.new(),
        Relationship.from_tuple!("document:ledger#viewer@user:jimmy")
      )
    )

    channel = SpiceDB.proto_client(client).channel

    {:ok, response} =
      V1.PermissionsService.Stub.check_permission(
        channel,
        %V1.CheckPermissionRequest{
          consistency: %V1.Consistency{requirement: {:fully_consistent, true}},
          resource: %V1.ObjectReference{object_type: "document", object_id: "ledger"},
          permission: "view",
          subject: %V1.SubjectReference{
            object: %V1.ObjectReference{object_type: "user", object_id: "jimmy"}
          }
        },
        timeout: 30_000
      )

    assert response.permissionship == :PERMISSIONSHIP_HAS_PERMISSION

    {:error, %GRPC.RPCError{} = raw} =
      V1.SchemaService.Stub.write_schema(channel, %V1.WriteSchemaRequest{
        schema: "definition user {"
      })

    assert %SpiceDB.InvalidArgumentError{code: 3, reason: "ERROR_REASON_SCHEMA_PARSE_ERROR"} =
             SpiceDB.Error.from_grpc_status(raw)
  end
end
