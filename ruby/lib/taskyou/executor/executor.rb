# frozen_string_literal: true

require "open3"
require "logger"
require "json"
require "fileutils"

module Taskyou
  module Executor
    # TaskEvent represents a change to a task
    class TaskEvent
      attr_accessor :type, :task, :task_id

      def initialize(type:, task: nil, task_id:)
        @type = type
        @task = task
        @task_id = task_id
      end
    end

    # Executor manages background task execution.
    class Executor
      DEFAULT_SUSPEND_IDLE_TIMEOUT = 6 * 60 * 60 # 6 hours
      DONE_TASK_CLEANUP_TIMEOUT = 30 * 60 # 30 minutes
      DEFAULT_EXECUTOR_SLUG = "claude"
      DEFAULT_EXECUTOR_NAME = "Claude"

      EXECUTOR_ENV_KEYS = %w[TASK_EXECUTOR WORKFLOW_EXECUTOR TASKYOU_EXECUTOR WORKTREE_EXECUTOR].freeze

      attr_reader :db, :config, :logger, :executor_factory, :executor_slug, :executor_name

      def initialize(database, config, logger: nil)
        @db = database
        @config = config
        @logger = logger || Logger.new($stderr)
        @logger.progname = "executor"

        @executor_factory = ExecutorFactory.new
        @running_tasks = {}
        @cancel_procs = {}
        @running = false
        @stop_channel = Queue.new

        @suspended_tasks = {}
        @log_subscribers = {}
        @task_subscribers = []

        @mutex = Mutex.new

        @executor_slug, @executor_name = detect_executor_identity

        # Register available executors
        @executor_factory.register(ClaudeExecutor.new(self))
        @executor_factory.register(CodexExecutor.new(self))
      end

      def self.with_logging(database, config, output = $stderr)
        logger = Logger.new(output)
        logger.progname = "executor"
        new(database, config, logger: logger)
      end

      def display_name
        @executor_name || DEFAULT_EXECUTOR_NAME
      end

      def start
        @mutex.synchronize do
          return if @running

          @running = true
        end

        Thread.new { run_loop }
      end

      def stop
        @mutex.synchronize do
          return unless @running

          @running = false
          @stop_channel << :stop
        end
      end

      def running?
        @mutex.synchronize { @running }
      end

      # Subscribe to task events
      def subscribe_to_events(&block)
        @mutex.synchronize do
          @task_subscribers << block
        end
      end

      # Subscribe to logs for a specific task
      def subscribe_to_logs(task_id, &block)
        @mutex.synchronize do
          @log_subscribers[task_id] ||= []
          @log_subscribers[task_id] << block
        end
      end

      # Unsubscribe from logs for a specific task
      def unsubscribe_from_logs(task_id, block)
        @mutex.synchronize do
          @log_subscribers[task_id]&.delete(block)
        end
      end

      # Publish a task event to all subscribers
      def publish_task_event(event)
        subscribers = @mutex.synchronize { @task_subscribers.dup }
        subscribers.each { |sub| sub.call(event) }
      end

      # Publish a log entry to subscribers for a task
      def publish_log(task_id, log)
        subscribers = @mutex.synchronize { @log_subscribers[task_id]&.dup || [] }
        subscribers.each { |sub| sub.call(log) }
      end

      # Check if a task is currently running
      def task_running?(task_id)
        @mutex.synchronize { @running_tasks[task_id] }
      end

      # Get the process ID for a task's Claude process
      def get_claude_pid(task_id)
        # This would need to track PIDs from running processes
        # For now, return 0 (not running)
        0
      end

      # Kill the Claude process for a task
      def kill_claude_process(task_id)
        task = DB::Task.find(db, task_id)
        return false unless task

        # Kill via tmux if we have a window ID
        unless task.tmux_window_id.empty?
          system("tmux kill-window -t '#{task.tmux_window_id}' 2>/dev/null")
        end

        # Also try killing by daemon session
        unless task.daemon_session.empty?
          system("tmux kill-session -t '#{task.daemon_session}' 2>/dev/null")
        end

        true
      end

      # Suspend a task's process
      def suspend_task(task_id)
        @mutex.synchronize do
          @suspended_tasks[task_id] = Time.now
        end
        # Send SIGSTOP to the process if we have a PID
        # This is a simplified implementation
        true
      end

      # Resume a suspended task's process
      def resume_task(task_id)
        @mutex.synchronize do
          @suspended_tasks.delete(task_id)
        end
        # Send SIGCONT to the process if we have a PID
        true
      end

      # Check if a task is suspended
      def suspended?(task_id)
        @mutex.synchronize { @suspended_tasks.key?(task_id) }
      end

      # Run Claude CLI for a task
      def run_claude(task, work_dir, prompt)
        log_output(task.id, "system", "Starting Claude execution...")

        # Build Claude command
        cmd = build_claude_command(task, prompt)

        # Execute in worktree directory
        result = execute_command(task, cmd, work_dir)

        log_output(task.id, "system", "Claude execution completed")
        result
      end

      # Resume Claude with feedback
      def run_claude_resume(task, work_dir, prompt, feedback)
        log_output(task.id, "system", "Resuming Claude with feedback...")

        cmd = build_claude_resume_command(task, prompt, feedback)
        result = execute_command(task, cmd, work_dir)

        log_output(task.id, "system", "Claude resume completed")
        result
      end

      # Run Codex CLI for a task
      def run_codex(task, work_dir, prompt)
        log_output(task.id, "system", "Starting Codex execution...")

        cmd = ["codex", "--prompt", prompt]
        result = execute_command(task, cmd, work_dir)

        log_output(task.id, "system", "Codex execution completed")
        result
      end

      private

      def detect_executor_identity
        EXECUTOR_ENV_KEYS.each do |key|
          value = ENV[key]&.strip
          next if value.nil? || value.empty?

          slug = value.downcase
          display = format_executor_display_name(slug, value)
          return [slug, display]
        end

        [DEFAULT_EXECUTOR_SLUG, DEFAULT_EXECUTOR_NAME]
      end

      def format_executor_display_name(slug, raw)
        case slug
        when "codex"
          "Codex"
        when "claude"
          DEFAULT_EXECUTOR_NAME
        else
          trimmed = raw.strip
          return DEFAULT_EXECUTOR_NAME if trimmed.empty?

          # Capitalize first letter if all lowercase
          if trimmed == trimmed.downcase
            trimmed.capitalize
          else
            trimmed
          end
        end
      end

      def run_loop
        logger.info("Executor loop started")

        loop do
          # Check for stop signal
          begin
            @stop_channel.pop(true)
            break
          rescue ThreadError
            # Queue empty, continue
          end

          # Process next queued task
          process_next_task

          # Small sleep to avoid busy waiting
          sleep 1
        end

        logger.info("Executor loop stopped")
      end

      def process_next_task
        task = DB::Task.next_queued(db)
        return unless task

        execute_task(task)
      end

      def execute_task(task)
        @mutex.synchronize do
          return if @running_tasks[task.id]

          @running_tasks[task.id] = true
        end

        begin
          # Mark task as processing
          task.update_status(db, DB::Task::STATUS_PROCESSING)
          publish_task_event(TaskEvent.new(type: "status_changed", task: task, task_id: task.id))

          # Setup worktree if needed
          setup_worktree(task)

          # Get task type instructions
          task_type = DB::TaskType.find_by_name(db, task.type)
          prompt = build_prompt(task, task_type)

          # Get the appropriate executor
          executor = @executor_factory.get(task.executor)
          unless executor
            log_output(task.id, "error", "Unknown executor: #{task.executor}")
            task.update_status(db, DB::Task::STATUS_BLOCKED)
            return
          end

          # Execute the task
          work_dir = task.worktree_path.empty? ? config.get_project_dir(task.project) : task.worktree_path
          result = executor.execute(task, work_dir, prompt)

          # Handle result
          if result.success?
            task.update_status(db, DB::Task::STATUS_DONE)
          elsif result.needs_input?
            task.update_status(db, DB::Task::STATUS_BLOCKED)
          else
            task.update_status(db, DB::Task::STATUS_BLOCKED)
          end

          publish_task_event(TaskEvent.new(type: "status_changed", task: task, task_id: task.id))
        ensure
          @mutex.synchronize do
            @running_tasks.delete(task.id)
          end
        end
      end

      def setup_worktree(task)
        return if task.worktree_path && !task.worktree_path.empty?

        # Get project directory
        project_dir = config.get_project_dir(task.project)

        # Create worktree directory
        worktree_base = File.join(Dir.home, ".local", "share", "task", "worktrees", task.project)
        worktree_path = File.join(worktree_base, "task-#{task.id}")

        FileUtils.mkdir_p(worktree_base)

        # Create git worktree
        branch_name = "task/#{task.id}-#{sanitize_branch_name(task.title)}"

        Dir.chdir(project_dir) do
          system("git worktree add '#{worktree_path}' -b '#{branch_name}' 2>/dev/null")
        end

        # Allocate port
        port = DB::Task.allocate_port(db, task.id)

        # Update task
        task.worktree_path = worktree_path
        task.branch_name = branch_name
        task.port = port
        task.save(db)
      end

      def sanitize_branch_name(title)
        title.downcase
             .gsub(/[^a-z0-9\s-]/, "")
             .gsub(/\s+/, "-")
             .slice(0, 50)
      end

      def build_prompt(task, task_type)
        template = task_type&.instructions || default_prompt_template

        # Get project
        project = DB::Project.find_by_name(db, task.project)

        # Get memories
        memories = DB::Memory.for_project(db, task.project)
        memories_text = format_memories(memories)

        # Replace placeholders
        template
          .gsub("{{project}}", task.project)
          .gsub("{{title}}", task.title)
          .gsub("{{body}}", task.body)
          .gsub("{{project_instructions}}", project&.instructions || "")
          .gsub("{{memories}}", memories_text)
          .gsub("{{attachments}}", "") # TODO: implement attachments
          .gsub("{{history}}", "") # TODO: implement history
      end

      def format_memories(memories)
        return "" if memories.empty?

        grouped = memories.group_by(&:category)
        parts = []

        grouped.each do |category, mems|
          parts << "## #{category.capitalize}"
          mems.each { |m| parts << "- #{m.content}" }
        end

        parts.join("\n")
      end

      def default_prompt_template
        <<~TEMPLATE
          Task: {{title}}

          {{body}}

          Complete this task and provide a summary when done.
        TEMPLATE
      end

      def build_claude_command(task, prompt)
        cmd = ["claude", "--print", "--dangerously-skip-permissions"]

        # Add session resume if available
        unless task.claude_session_id.empty?
          cmd += ["--resume", task.claude_session_id]
        end

        cmd + [prompt]
      end

      def build_claude_resume_command(task, prompt, feedback)
        cmd = ["claude", "--print", "--dangerously-skip-permissions"]

        unless task.claude_session_id.empty?
          cmd += ["--resume", task.claude_session_id]
        end

        combined = "#{prompt}\n\nFeedback:\n#{feedback}"
        cmd + [combined]
      end

      def execute_command(task, cmd, work_dir)
        FileUtils.mkdir_p(work_dir) unless Dir.exist?(work_dir)

        stdout, stderr, status = Open3.capture3(*cmd, chdir: work_dir)

        # Log output
        stdout.each_line { |line| log_output(task.id, "output", line.chomp) }
        stderr.each_line { |line| log_output(task.id, "error", line.chomp) }

        # Parse result
        if status.success?
          # Check for session ID in output
          if stdout =~ /Session ID: (\S+)/
            task.update_claude_session_id(db, ::Regexp.last_match(1))
          end

          ExecResult.new(success: true, message: "Completed successfully")
        elsif stdout.include?("needs input") || stdout.include?("blocked")
          ExecResult.new(needs_input: true, message: "Waiting for input")
        else
          ExecResult.new(success: false, message: stderr)
        end
      end

      def log_output(task_id, line_type, content)
        DB::TaskLog.append(db, task_id, line_type, content)

        log = DB::TaskLog.new(
          task_id: task_id,
          line_type: line_type,
          content: content,
          created_at: Time.now
        )

        publish_log(task_id, log)
      end
    end
  end
end
