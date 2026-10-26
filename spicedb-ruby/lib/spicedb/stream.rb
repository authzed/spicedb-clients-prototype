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
      raise StopIteration if closed?

      value = @enumerator.next
      raise StopIteration if closed?

      value
    rescue Exception # rubocop:disable Lint/RescueException -- release transport on Interrupt as well as application errors
      was_closed = closed?
      close
      raise StopIteration if was_closed

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
  end

  # One monotonic budget is shared by every attempt and page of an operation.
  class Budget
    def initialize(timeout)
      raise InvalidArgumentError, 'timeout must be finite and positive' unless timeout.nil? || (timeout.is_a?(Numeric) && timeout.finite? && timeout.positive?)

      @end = timeout && (monotonic + timeout)
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
end
