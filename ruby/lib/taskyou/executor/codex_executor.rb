# frozen_string_literal: true

module Taskyou
  module Executor
    # CodexExecutor implements TaskExecutorInterface for OpenAI Codex CLI.
    class CodexExecutor
      include TaskExecutorInterface

      attr_reader :executor

      def initialize(executor)
        @executor = executor
        @running_tasks = {}
      end

      def name
        DB::Task::EXECUTOR_CODEX
      end

      def available?
        system("which codex > /dev/null 2>&1")
      end

      def execute(task, work_dir, prompt)
        executor.run_codex(task, work_dir, prompt)
      end

      def resume(task, work_dir, prompt, feedback)
        # Codex doesn't support resume, so just execute with combined prompt
        combined_prompt = "#{prompt}\n\nAdditional feedback:\n#{feedback}"
        execute(task, work_dir, combined_prompt)
      end

      def process_id(task_id)
        executor.get_codex_pid(task_id)
      end

      def kill(task_id)
        executor.kill_codex_process(task_id)
      end

      def suspend(task_id)
        executor.suspend_task(task_id)
      end

      def suspended?(task_id)
        executor.suspended?(task_id)
      end
    end
  end
end
