# frozen_string_literal: true

module SpiceDB
  DebugInformation = Values.record(:schema_used, :check)
  CaveatEvaluation = Values.record(:expression, :result, :context, :missing_context, :caveat_name)
  CheckDebugTrace = Values.record(:resource, :permission, :permission_type, :subject, :result,
                                  :caveat_evaluation, :duration, :resolution, :was_cached_result, :subproblems,
                                  :expiration, :trace_operation_id, :source)

  module DebugMapping
    private

    def debug_information(info)
      return nil unless info

      DebugInformation.new(schema_used: info.schema_used, check: debug_trace(info.check))
    end

    def debug_trace(trace)
      return nil unless trace

      caveat = trace.caveat_evaluation_info
      evaluation = if caveat
                     CaveatEvaluation.new(expression: caveat.expression,
                                          result: { RESULT_UNEVALUATED: :unevaluated, RESULT_FALSE: :evaluated_false, RESULT_TRUE: :evaluated_true,
                                                    RESULT_MISSING_SOME_CONTEXT: :missing_context }.fetch(caveat.result, :unspecified),
                                          context: caveat.context && struct_to_caveat_context(caveat.context),
                                          missing_context: missing_context_from_proto(caveat.partial_caveat_info), caveat_name: caveat.caveat_name)
                   end
      CheckDebugTrace.new(resource: trace.resource && object_from_proto(trace.resource), permission: trace.permission,
                          permission_type: { PERMISSION_TYPE_RELATION: :relation,
                                             PERMISSION_TYPE_PERMISSION: :permission }.fetch(trace.permission_type, :unspecified),
                          subject: trace.subject && subject_ref_from_proto(trace.subject), result: check_permissionship_from_proto(trace.result),
                          caveat_evaluation: evaluation, duration: trace.duration && (trace.duration.seconds + Rational(trace.duration.nanos, 1_000_000_000)),
                          resolution: { was_cached_result: :cached, sub_problems: :subproblems }.fetch(trace.resolution, :unspecified),
                          was_cached_result: trace.resolution == :was_cached_result ? trace.was_cached_result : nil,
                          subproblems: trace.resolution == :sub_problems ? trace.sub_problems.traces.map { |child| debug_trace(child) } : [],
                          expiration: trace.optional_expires_at && Time.at(trace.optional_expires_at.seconds, trace.optional_expires_at.nanos, :nsec),
                          trace_operation_id: trace.trace_operation_id, source: trace.source)
    end
  end
end
