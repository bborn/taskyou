# frozen_string_literal: true

module Taskyou
  module Executor
    # ExecResult represents the result of executing a task
    class ExecResult
      attr_accessor :success, :needs_input, :interrupted, :message

      def initialize(success: false, needs_input: false, interrupted: false, message: "")
        @success = success
        @needs_input = needs_input
        @interrupted = interrupted
        @message = message
      end

      def success?
        @success
      end

      def needs_input?
        @needs_input
      end

      def interrupted?
        @interrupted
      end
    end

    # TaskExecutor defines the interface for task execution backends.
    # Implementations handle the actual running of tasks using different CLI tools.
    module TaskExecutorInterface
      # Name returns the executor name (e.g., "claude", "codex")
      def name
        raise NotImplementedError
      end

      # Execute runs a task with the given prompt and returns the result.
      # The work_dir is the directory where the executor should run.
      def execute(task, work_dir, prompt)
        raise NotImplementedError
      end

      # Resume resumes a previous session with additional feedback.
      def resume(task, work_dir, prompt, feedback)
        raise NotImplementedError
      end

      # Check if the executor CLI is installed and available.
      def available?
        raise NotImplementedError
      end

      # Returns the PID of the executor process for a task, or 0 if not running.
      def process_id(task_id)
        raise NotImplementedError
      end

      # Terminates the executor process for a task.
      def kill(task_id)
        raise NotImplementedError
      end

      # Pauses the executor process for a task (to save memory).
      def suspend(task_id)
        raise NotImplementedError
      end

      # Checks if a task's executor process is suspended.
      def suspended?(task_id)
        raise NotImplementedError
      end
    end

    # ExecutorFactory manages creation of task executors.
    class ExecutorFactory
      def initialize
        @executors = {}
      end

      # Register adds an executor to the factory.
      def register(executor)
        @executors[executor.name] = executor
      end

      # Get returns the executor for the given name, or nil if not found.
      def get(name)
        name = DB::Task.default_executor if name.nil? || name.empty?
        @executors[name]
      end

      # Returns the names of all registered executors that are available.
      def available
        @executors.select { |_, exec| exec.available? }.keys
      end

      # Returns all registered executor names.
      def all
        @executors.keys
      end
    end
  end
end
