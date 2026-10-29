# frozen_string_literal: true

module SpiceDB
  # A pull stream owns its current RPC even while next is blocked in gRPC.
  class Stream
    include Enumerable

    def initialize(on_close: nil, &producer)
      @mutex = Mutex.new
      @closed = false
      @active = nil
      @on_close = on_close
      @enumerator = Enumerator.new { |out| producer.call(self, out) }
    end

    def attach(operation)
      cancelled = @mutex.synchronize do
        @active = operation unless @closed
        @closed
      end
      operation.cancel if cancelled
      raise StopIteration if cancelled
    end

    def detach(operation)
      @mutex.synchronize { @active = nil if @active.equal?(operation) }
    end

    def next
      finish if closed?

      value = @enumerator.next
      finish if closed?

      value
    rescue Exception # rubocop:disable Lint/RescueException -- release transport on Interrupt as well as application errors
      was_closed = closed?
      close
      finish if was_closed

      raise
    end

    def each
      return enum_for(:each) unless block_given?

      begin
        loop { yield self.next }
      ensure
        close
      end
      self
    end

    def close
      operation, callback = @mutex.synchronize do
        return if @closed

        @closed = true
        active = @active
        @active = nil
        [active, @on_close]
      end
      operation&.cancel
    ensure
      callback&.call(self)
    end

    def closed? = @mutex.synchronize { @closed }

    # Closes the stream on behalf of someone other than its consumer (the
    # client being closed): the consumer, including one blocked in #next,
    # then gets CancelledError with +message+ rather than an ordinary end of
    # iteration, so a cancelled read never looks complete.
    #
    # @api private
    def cancel(message)
      @mutex.synchronize { @cancelled ||= message unless @closed }
      close
    end

    private

    # Ends iteration: StopIteration after the consumer's own #close,
    # CancelledError after #cancel.
    def finish
      message = @mutex.synchronize { @cancelled }
      raise CancelledError, message if message

      raise StopIteration
    end
  end

  # One monotonic budget is shared by every attempt and page of an operation.
  # It also carries the operation's per-call CallOptions and RetryPolicy.
  #
  # A finite paged read also carries +page_timeout+, the client default
  # deadline, which bounds each page request on its own (see #page): a
  # server that stalls mid-read fails the read instead of hanging it.
  class Budget
    attr_reader :call_options, :retry_policy, :page_timeout

    def initialize(timeout, call_options: nil, retry_policy: nil, page_timeout: nil)
      raise LocalValidationError, 'call_options: expected SpiceDB::CallOptions' unless call_options.nil? || call_options.is_a?(CallOptions)
      raise LocalValidationError, 'retry_policy: expected SpiceDB::RetryPolicy' unless retry_policy.nil? || retry_policy.is_a?(RetryPolicy)

      @call_options = call_options
      @retry_policy = retry_policy
      raise LocalValidationError, 'timeout must be finite and positive' unless timeout.nil? || (timeout.is_a?(Numeric) && timeout.finite? && timeout.positive?)

      @end = timeout && (monotonic + timeout)
      @page_timeout = page_timeout
    end

    # The budget for one page request opened now: +page_timeout+ from now,
    # or the whole operation's remaining budget when that ends earlier.
    # Without a page timeout (watch, imports, a disabled client default) it
    # is this budget.
    def page
      return self unless @page_timeout

      page_end = monotonic + @page_timeout
      copy = dup
      copy.instance_variable_set(:@end, @end ? [@end, page_end].min : page_end)
      copy
    end

    def remaining
      return nil unless @end

      value = @end - monotonic
      raise DeadlineExceededError, 'operation deadline exceeded' unless value.positive?

      value
    end

    def deadline
      duration = remaining
      duration && (Time.now + duration)
    end

    def sleep(delay)
      left = remaining
      Kernel.sleep(left ? [delay, left].min : delay)
      remaining
    end

    private

    def monotonic = Process.clock_gettime(Process::CLOCK_MONOTONIC)
  end

  # Bounds each page of a single-RPC paged read (export, whose server streams
  # every page on one call, so a call deadline would bound the whole export):
  # the time from asking for the next response to receiving it may not exceed
  # +timeout+. On expiry it cancels the call; #expired? then tells the reader
  # to raise DeadlineExceededError rather than the cancellation. Time the
  # caller spends consuming a page does not count.
  class PageWatchdog
    def initialize(timeout, call)
      @timeout = timeout
      @call = call
      @mutex = Mutex.new
      @cond = ConditionVariable.new
      @waiting_since = monotonic
      @expired = false
      @done = false
      @thread = Thread.new { run }
    end

    def received = update(nil)
    def waiting = update(monotonic)
    def expired? = @mutex.synchronize { @expired }

    def stop
      @mutex.synchronize do
        @done = true
        @cond.signal
      end
      @thread.join
    end

    private

    def update(since)
      @mutex.synchronize do
        @waiting_since = since
        @cond.signal
      end
    end

    def run
      @mutex.synchronize do
        until @done
          if @waiting_since.nil?
            @cond.wait(@mutex)
            next
          end

          left = @waiting_since + @timeout - monotonic
          if left.positive?
            @cond.wait(@mutex, left)
            next
          end

          @expired = true
          break
        end
      end
      @call.cancel if @expired
    end

    def monotonic = Process.clock_gettime(Process::CLOCK_MONOTONIC)
  end
end
