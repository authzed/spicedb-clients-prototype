# frozen_string_literal: true

module SpiceDB
  # @api private
  #
  # Response batching for lookup_resources: the batch size each request asks
  # for, and reading a response in whichever form it arrives. The client and
  # the test double (SpiceDB::Testing::Store) share the sizing rules.
  module LookupBatches
    # @api private
    #
    # The response batch size a lookup_resources call asks for, before
    # ::page_batch_size aligns it to a limit: +response_batch_size+ as
    # given (never clamped; the server rejects one above its maximum), by
    # default the page size capped at Client::DEFAULT_LOOKUP_RESPONSE_BATCH_SIZE
    # (in single-request mode the limit, capped, or the cap without one), and
    # 0 (one resource per message) with LookupBatching::DISABLED.
    def self.batch_size(batching, response_batch_size, page_size:, limit:, single:)
      LookupBatching.validate(batching)
      unless response_batch_size.nil? || (response_batch_size.is_a?(Integer) && response_batch_size.positive?)
        raise LocalValidationError, 'response_batch_size must be a positive integer'
      end
      return 0 if batching == LookupBatching::DISABLED

      cap = Client::DEFAULT_LOOKUP_RESPONSE_BATCH_SIZE
      response_batch_size || [single ? (limit || cap) : page_size, cap].min
    end

    # @api private
    #
    # The response batch size for the next page request of a lookup_resources
    # call that has yielded +delivered+ results; 0 requests one resource per
    # message. A batch has one cursor, after its last result, and the last
    # item of a limited call must carry a cursor, so the limit has to fall on
    # a batch boundary. The wire limit stays the page size (it is hashed into
    # the cursor), so on the page where fewer results remain than a page
    # holds, the batch size becomes the remaining count when that is smaller,
    # stays when it divides the remaining count, and otherwise batching is
    # off for that page. The unconditional-only filter drops results after
    # the server batched them, which makes the boundary the limit lands on
    # unpredictable, so a limit with that filter is never batched. A single
    # request sends the limit itself, and the server ends a batch with it.
    def self.page_batch_size(batch_size, page_size:, limit:, delivered:, single:, has_permission_only:)
      return batch_size if batch_size.zero? || limit.nil? || single
      return 0 if has_permission_only

      remaining = limit - delivered
      return batch_size if remaining >= page_size || (remaining % batch_size).zero?

      remaining < batch_size ? remaining : 0
    end

    private

    # The items one page response yields, its after-result cursor and the
    # number of results it held. A one-resource response is one result
    # carrying its own cursor. A batch (read only when +batched+) expands to
    # its results in order, each validated like a one-resource response
    # before any of them is yielded. Its one cursor resumes after its last
    # result, so only the last item yielded from it carries the cursor: the
    # last one +include_result+ keeps, and none when +room+ (what is left of
    # the limit) cuts the batch short, since resuming there would skip the
    # rest of it.
    def page_response_items(response, mapper, include_result, batched:, room:)
      batch = batched ? response.batched : nil
      if batch.nil?
        after = response.after_result_cursor&.token
        item = mapper.call(response, after)
        return [include_result && !include_result.call(item) ? [] : [item], after, 1]
      end

      after = batch.after_cursor&.token
      kept = batch.results.filter_map do |result|
        item = mapper.call(result, nil, response)
        item unless include_result && !include_result.call(item)
      end
      kept[-1] = kept.last.with(cursor: after) unless kept.empty? || (room && kept.length > room)
      [room ? kept.first(room) : kept, after, batch.results.length]
    end
  end
end
