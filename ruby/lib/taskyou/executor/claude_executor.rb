# frozen_string_literal: true

module Taskyou
  module Executor
    # ClaudeExecutor implements TaskExecutorInterface for Claude Code CLI.
    class ClaudeExecutor
      include TaskExecutorInterface

      attr_reader :executor

      def initialize(executor)
        @executor = executor
        @running_tasks = {}
      end

      def name
        DB::Task::EXECUTOR_CLAUDE
      end

      def available?
        system("which claude > /dev/null 2>&1")
      end

      def execute(task, work_dir, prompt)
        executor.run_claude(task, work_dir, prompt)
      end

      def resume(task, work_dir, prompt, feedback)
        executor.run_claude_resume(task, work_dir, prompt, feedback)
      end

      def process_id(task_id)
        executor.get_claude_pid(task_id)
      end

      def kill(task_id)
        executor.kill_claude_process(task_id)
      end

      def suspend(task_id)
        executor.suspend_task(task_id)
      end

      def suspended?(task_id)
        executor.suspended?(task_id)
      end

      def resume_process(task_id)
        executor.resume_task(task_id)
      end
    end
  end
end
