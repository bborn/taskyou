# frozen_string_literal: true

require_relative "taskyou/version"

module Taskyou
  class Error < StandardError; end
  class ProjectNotFoundError < Error; end
  class TaskNotFoundError < Error; end
  class DatabaseError < Error; end

  autoload :Config, "taskyou/config"
  autoload :CLI, "taskyou/cli"

  module DB
    autoload :Database, "taskyou/db/database"
    autoload :Task, "taskyou/db/task"
    autoload :Project, "taskyou/db/project"
    autoload :Memory, "taskyou/db/memory"
    autoload :TaskType, "taskyou/db/task_type"
    autoload :TaskLog, "taskyou/db/task_log"
  end

  module Executor
    autoload :ExecResult, "taskyou/executor/task_executor"
    autoload :TaskExecutorInterface, "taskyou/executor/task_executor"
    autoload :ExecutorFactory, "taskyou/executor/task_executor"
    autoload :Executor, "taskyou/executor/executor"
    autoload :ClaudeExecutor, "taskyou/executor/claude_executor"
    autoload :CodexExecutor, "taskyou/executor/codex_executor"
  end

  module UI
    autoload :App, "taskyou/ui/app"
    autoload :Kanban, "taskyou/ui/kanban"
    autoload :Styles, "taskyou/ui/styles"
    autoload :Theme, "taskyou/ui/theme"
  end
end
