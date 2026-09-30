defmodule SpiceDB.Wire do
  @moduledoc false

  alias Authzed.Api.Materialize.V0, as: M
  alias Authzed.Api.V1

  alias SpiceDB.{
    CaveatContext,
    CheckResult,
    Consistency,
    Filter,
    ObjectRef,
    PartialCaveatInfo,
    Relationship,
    ResolvedSubject,
    SubjectRef
  }

  @spec consistency(Consistency.t()) :: V1.Consistency.t()
  def consistency(%Consistency{type: :full}),
    do: %V1.Consistency{requirement: {:fully_consistent, true}}

  def consistency(%Consistency{type: :min_latency}),
    do: %V1.Consistency{requirement: {:minimize_latency, true}}

  def consistency(%Consistency{type: :at_least, revision: rev}),
    do: %V1.Consistency{requirement: {:at_least_as_fresh, %V1.ZedToken{token: rev}}}

  def consistency(%Consistency{type: :snapshot, revision: rev}),
    do: %V1.Consistency{requirement: {:at_exact_snapshot, %V1.ZedToken{token: rev}}}

  def consistency(other) do
    raise SpiceDB.InvalidArgumentError,
      message:
        "expected a SpiceDB.Consistency, got: #{inspect(other)}; use SpiceDB.Consistency.full/0 and friends"
  end

  @spec token(String.t() | nil) :: V1.ZedToken.t() | nil
  def token(nil), do: nil
  def token(""), do: nil
  def token(rev) when is_binary(rev), do: %V1.ZedToken{token: rev}

  @spec token_string(V1.ZedToken.t() | nil) :: String.t()
  def token_string(%V1.ZedToken{token: token}), do: token
  def token_string(nil), do: ""

  @spec object(ObjectRef.t()) :: V1.ObjectReference.t()
  def object(%ObjectRef{object_type: type, object_id: id}),
    do: %V1.ObjectReference{object_type: type, object_id: id}

  @spec subject(SubjectRef.t()) :: V1.SubjectReference.t()
  def subject(%SubjectRef{} = s) do
    %V1.SubjectReference{
      object: %V1.ObjectReference{object_type: s.subject_type, object_id: s.subject_id},
      optional_relation: s.optional_relation || ""
    }
  end

  @spec relationship(Relationship.t()) :: V1.Relationship.t()
  def relationship(%Relationship{} = rel) do
    %V1.Relationship{
      resource: %V1.ObjectReference{object_type: rel.resource_type, object_id: rel.resource_id},
      relation: rel.resource_relation,
      subject: %V1.SubjectReference{
        object: %V1.ObjectReference{object_type: rel.subject_type, object_id: rel.subject_id},
        optional_relation: rel.subject_relation || ""
      },
      optional_caveat: caveat(rel.caveat_name, rel.caveat_context),
      optional_expires_at: timestamp(rel.expiration)
    }
  end

  defp caveat(name, _context) when name in [nil, ""], do: nil

  defp caveat(name, context),
    do: %V1.ContextualizedCaveat{caveat_name: name, context: CaveatContext.to_struct(context)}

  @spec relationship_from_proto(V1.Relationship.t()) :: Relationship.t()
  def relationship_from_proto(%V1.Relationship{} = rel) do
    resource = rel.resource || %V1.ObjectReference{}
    subject = rel.subject || %V1.SubjectReference{}
    subject_object = subject.object || %V1.ObjectReference{}
    {caveat_name, caveat_context} = caveat_from_proto(rel.optional_caveat)

    %Relationship{
      resource_type: resource.object_type,
      resource_id: resource.object_id,
      resource_relation: rel.relation,
      subject_type: subject_object.object_type,
      subject_id: subject_object.object_id,
      subject_relation: subject.optional_relation,
      caveat_name: caveat_name,
      caveat_context: caveat_context,
      expiration: datetime(rel.optional_expires_at)
    }
  end

  defp caveat_from_proto(nil), do: {nil, nil}
  defp caveat_from_proto(%V1.ContextualizedCaveat{caveat_name: ""}), do: {nil, nil}

  defp caveat_from_proto(%V1.ContextualizedCaveat{caveat_name: name, context: context}),
    do: {name, CaveatContext.from_struct(context)}

  @spec filter(Filter.t()) :: V1.RelationshipFilter.t()
  def filter(%Filter{} = f) do
    %V1.RelationshipFilter{
      resource_type: f.resource_type,
      optional_resource_id: f.resource_id || "",
      optional_resource_id_prefix: f.resource_id_prefix || "",
      optional_relation: f.relation || "",
      optional_subject_filter: subject_filter(f)
    }
  end

  def filter(other) do
    raise SpiceDB.InvalidArgumentError,
      message: "expected a SpiceDB.Filter, got: #{inspect(other)}"
  end

  defp subject_filter(%Filter{subject_type: type} = f) when type in [nil, ""] do
    for {field, value} <- [subject_id: f.subject_id, subject_relation: f.subject_relation],
        value not in [nil, ""] do
      raise SpiceDB.InvalidArgumentError,
        message:
          "Filter has #{field} set without subject_type -- call with_subject_type before with_#{field}."
    end

    nil
  end

  defp subject_filter(%Filter{} = f) do
    %V1.SubjectFilter{
      subject_type: f.subject_type,
      optional_subject_id: f.subject_id || "",
      optional_relation:
        if(f.subject_relation, do: %V1.SubjectFilter.RelationFilter{relation: f.subject_relation})
    }
  end

  @spec precondition({:must_match | :must_not_match, Filter.t()}) :: V1.Precondition.t()
  def precondition({:must_match, f}),
    do: %V1.Precondition{operation: :OPERATION_MUST_MATCH, filter: filter(f)}

  def precondition({:must_not_match, f}),
    do: %V1.Precondition{operation: :OPERATION_MUST_NOT_MATCH, filter: filter(f)}

  @spec update({:create | :touch | :delete, Relationship.t()}) :: V1.RelationshipUpdate.t()
  def update({op, rel}) do
    operation =
      case op do
        :create -> :OPERATION_CREATE
        :touch -> :OPERATION_TOUCH
        :delete -> :OPERATION_DELETE
      end

    %V1.RelationshipUpdate{operation: operation, relationship: relationship(rel)}
  end

  @check_permissionship %{
    PERMISSIONSHIP_NO_PERMISSION: :no_permission,
    PERMISSIONSHIP_HAS_PERMISSION: :has_permission,
    PERMISSIONSHIP_CONDITIONAL_PERMISSION: :conditional_permission
  }

  @lookup_permissionship %{
    LOOKUP_PERMISSIONSHIP_HAS_PERMISSION: :has_permission,
    LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION: :conditional_permission
  }

  @spec check_permissionship(atom() | integer()) :: CheckResult.permissionship()
  def check_permissionship(value), do: Map.get(@check_permissionship, value, :unspecified)

  @spec lookup_permissionship(atom() | integer()) :: SpiceDB.LookupResource.permissionship()
  def lookup_permissionship(value), do: Map.get(@lookup_permissionship, value, :unspecified)

  @spec missing_context(V1.PartialCaveatInfo.t() | nil) :: [String.t()]
  def missing_context(nil), do: []
  def missing_context(%V1.PartialCaveatInfo{missing_required_context: missing}), do: missing

  @spec partial_caveat(V1.PartialCaveatInfo.t() | nil) :: PartialCaveatInfo.t() | nil
  def partial_caveat(nil), do: nil

  def partial_caveat(%V1.PartialCaveatInfo{missing_required_context: missing}),
    do: %PartialCaveatInfo{missing_required_context: missing}

  @spec resolved_subject(V1.ResolvedSubject.t() | nil) :: ResolvedSubject.t() | nil
  def resolved_subject(nil), do: nil

  def resolved_subject(%V1.ResolvedSubject{} = s) do
    %ResolvedSubject{
      subject_id: s.subject_object_id,
      permissionship: lookup_permissionship(s.permissionship),
      partial_caveat: partial_caveat(s.partial_caveat_info)
    }
  end

  @spec subject_from_proto(V1.SubjectReference.t()) :: SubjectRef.t()
  def subject_from_proto(%V1.SubjectReference{} = s) do
    object = s.object || %V1.ObjectReference{}
    SubjectRef.new(object.object_type, object.object_id, s.optional_relation)
  end

  @spec object_from_proto(V1.ObjectReference.t() | nil) :: ObjectRef.t() | nil
  def object_from_proto(nil), do: nil

  def object_from_proto(%V1.ObjectReference{object_type: t, object_id: id}),
    do: ObjectRef.new(t, id)

  @spec timestamp(DateTime.t() | nil) :: Google.Protobuf.Timestamp.t() | nil
  def timestamp(nil), do: nil

  def timestamp(%DateTime{} = dt) do
    micros = DateTime.to_unix(dt, :microsecond)

    %Google.Protobuf.Timestamp{
      seconds: Integer.floor_div(micros, 1_000_000),
      nanos: Integer.mod(micros, 1_000_000) * 1_000
    }
  end

  def timestamp(other) do
    raise SpiceDB.InvalidArgumentError,
      message: "expiration must be a DateTime, got: #{inspect(other)}"
  end

  @spec datetime(Google.Protobuf.Timestamp.t() | nil) :: DateTime.t() | nil
  def datetime(nil), do: nil

  def datetime(%Google.Protobuf.Timestamp{seconds: s, nanos: n}),
    do: DateTime.from_unix!(s * 1_000_000 + div(n, 1_000), :microsecond)

  @spec set_reference(M.SetReference.t() | nil) :: SpiceDB.SetReference.t() | nil
  def set_reference(nil), do: nil

  def set_reference(%M.SetReference{} = s) do
    %SpiceDB.SetReference{
      object_type: s.object_type,
      object_id: s.object_id,
      permission_or_relation: s.permission_or_relation
    }
  end
end
