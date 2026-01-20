# frozen_string_literal: true

require "sqlite3"
require "fileutils"

module Taskyou
  module DB
    class Database
      DEFAULT_PROJECT_COLORS = %w[
        #C678DD
        #61AFEF
        #56B6C2
        #98C379
        #E5C07B
        #E06C75
        #D19A66
        #ABB2BF
      ].freeze

      attr_reader :path

      def initialize(path = nil)
        @path = path || self.class.default_path
        setup_database
      end

      def self.default_path
        ENV["WORKTREE_DB_PATH"] || File.join(Dir.home, ".local", "share", "task", "tasks.db")
      end

      def close
        @db.close if @db
      end

      def execute(sql, *args)
        @db.execute(sql, args.flatten)
      end

      def get_first_row(sql, *args)
        @db.get_first_row(sql, args.flatten)
      end

      def get_first_value(sql, *args)
        @db.get_first_value(sql, args.flatten)
      end

      def last_insert_row_id
        @db.last_insert_row_id
      end

      private

      def setup_database
        FileUtils.mkdir_p(File.dirname(@path))

        @db = SQLite3::Database.new(@path)
        @db.busy_timeout = 5000
        @db.results_as_hash = true

        # Enable WAL mode for better concurrent access
        @db.execute("PRAGMA journal_mode=WAL")
        @db.execute("PRAGMA foreign_keys=ON")

        run_migrations
      end

      def run_migrations
        create_tables
        run_alter_migrations
        run_status_migrations
        ensure_personal_project
        ensure_default_task_types
        ensure_project_colors
      end

      def create_tables
        migrations = [
          <<~SQL,
            CREATE TABLE IF NOT EXISTS tasks (
              id INTEGER PRIMARY KEY AUTOINCREMENT,
              title TEXT NOT NULL,
              body TEXT DEFAULT '',
              status TEXT DEFAULT 'backlog',
              type TEXT DEFAULT '',
              project TEXT DEFAULT '',
              created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
              updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
              started_at DATETIME,
              completed_at DATETIME
            )
          SQL

          <<~SQL,
            CREATE TABLE IF NOT EXISTS task_logs (
              id INTEGER PRIMARY KEY AUTOINCREMENT,
              task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
              line_type TEXT DEFAULT 'output',
              content TEXT NOT NULL,
              created_at DATETIME DEFAULT CURRENT_TIMESTAMP
            )
          SQL

          <<~SQL,
            CREATE TABLE IF NOT EXISTS projects (
              id INTEGER PRIMARY KEY AUTOINCREMENT,
              name TEXT NOT NULL UNIQUE,
              path TEXT NOT NULL,
              aliases TEXT DEFAULT '',
              created_at DATETIME DEFAULT CURRENT_TIMESTAMP
            )
          SQL

          <<~SQL,
            CREATE TABLE IF NOT EXISTS settings (
              key TEXT PRIMARY KEY,
              value TEXT NOT NULL
            )
          SQL

          "CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status)",
          "CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project)",
          "CREATE INDEX IF NOT EXISTS idx_task_logs_task_id ON task_logs(task_id)",

          <<~SQL,
            CREATE TABLE IF NOT EXISTS project_memories (
              id INTEGER PRIMARY KEY AUTOINCREMENT,
              project TEXT NOT NULL,
              category TEXT NOT NULL DEFAULT 'general',
              content TEXT NOT NULL,
              source_task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
              created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
              updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
            )
          SQL

          "CREATE INDEX IF NOT EXISTS idx_project_memories_project ON project_memories(project)",
          "CREATE INDEX IF NOT EXISTS idx_project_memories_category ON project_memories(category)",

          <<~SQL,
            CREATE TABLE IF NOT EXISTS task_attachments (
              id INTEGER PRIMARY KEY AUTOINCREMENT,
              task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
              filename TEXT NOT NULL,
              mime_type TEXT DEFAULT '',
              size INTEGER DEFAULT 0,
              data BLOB NOT NULL,
              created_at DATETIME DEFAULT CURRENT_TIMESTAMP
            )
          SQL

          "CREATE INDEX IF NOT EXISTS idx_task_attachments_task_id ON task_attachments(task_id)",

          <<~SQL,
            CREATE TABLE IF NOT EXISTS task_types (
              id INTEGER PRIMARY KEY AUTOINCREMENT,
              name TEXT NOT NULL UNIQUE,
              label TEXT NOT NULL,
              instructions TEXT DEFAULT '',
              sort_order INTEGER DEFAULT 0,
              is_builtin INTEGER DEFAULT 0,
              created_at DATETIME DEFAULT CURRENT_TIMESTAMP
            )
          SQL

          <<~SQL,
            CREATE TABLE IF NOT EXISTS task_compaction_summaries (
              id INTEGER PRIMARY KEY AUTOINCREMENT,
              task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
              session_id TEXT NOT NULL,
              trigger TEXT NOT NULL,
              pre_tokens INTEGER DEFAULT 0,
              summary TEXT NOT NULL,
              custom_instructions TEXT DEFAULT '',
              created_at DATETIME DEFAULT CURRENT_TIMESTAMP
            )
          SQL

          "CREATE INDEX IF NOT EXISTS idx_task_compaction_summaries_task_id ON task_compaction_summaries(task_id)",

          <<~SQL
            CREATE VIRTUAL TABLE IF NOT EXISTS task_search USING fts5(
              task_id UNINDEXED,
              project,
              title,
              body,
              tags,
              transcript_excerpt,
              tokenize='porter unicode61'
            )
          SQL
        ]

        migrations.each { |sql| @db.execute(sql) }
      end

      def run_alter_migrations
        alter_migrations = [
          "ALTER TABLE projects ADD COLUMN instructions TEXT DEFAULT ''",
          "ALTER TABLE projects ADD COLUMN actions TEXT DEFAULT '[]'",
          "ALTER TABLE tasks ADD COLUMN worktree_path TEXT DEFAULT ''",
          "ALTER TABLE tasks ADD COLUMN branch_name TEXT DEFAULT ''",
          "ALTER TABLE tasks ADD COLUMN port INTEGER DEFAULT 0",
          "ALTER TABLE tasks ADD COLUMN scheduled_at DATETIME",
          "ALTER TABLE tasks ADD COLUMN recurrence TEXT DEFAULT ''",
          "ALTER TABLE tasks ADD COLUMN last_run_at DATETIME",
          "ALTER TABLE tasks ADD COLUMN claude_session_id TEXT DEFAULT ''",
          "ALTER TABLE projects ADD COLUMN color TEXT DEFAULT ''",
          "ALTER TABLE tasks ADD COLUMN pr_url TEXT DEFAULT ''",
          "ALTER TABLE tasks ADD COLUMN pr_number INTEGER DEFAULT 0",
          "ALTER TABLE tasks ADD COLUMN dangerous_mode INTEGER DEFAULT 0",
          "ALTER TABLE tasks ADD COLUMN daemon_session TEXT DEFAULT ''",
          "ALTER TABLE tasks ADD COLUMN tags TEXT DEFAULT ''",
          "ALTER TABLE tasks ADD COLUMN executor TEXT DEFAULT 'claude'",
          "ALTER TABLE tasks ADD COLUMN tmux_window_id TEXT DEFAULT ''"
        ]

        alter_migrations.each do |sql|
          @db.execute(sql)
        rescue SQLite3::SQLException
          # Ignore "duplicate column" errors for idempotent migrations
        end
      end

      def run_status_migrations
        status_migrations = [
          "UPDATE tasks SET status = 'backlog' WHERE status IN ('pending', 'interrupted')",
          "UPDATE tasks SET status = 'queued' WHERE status = 'in_progress'",
          "UPDATE tasks SET status = 'done' WHERE status IN ('ready', 'closed')"
        ]

        status_migrations.each { |sql| @db.execute(sql) }

        # Migrate tasks with empty project to 'personal'
        @db.execute("UPDATE tasks SET project = 'personal' WHERE project = ''")

        # Drop priority column if it exists (SQLite 3.35.0+ supports DROP COLUMN)
        begin
          @db.execute("ALTER TABLE tasks DROP COLUMN priority")
        rescue SQLite3::SQLException
          # Ignore if column doesn't exist
        end
      end

      def ensure_personal_project
        count = @db.get_first_value("SELECT COUNT(*) FROM projects WHERE name = 'personal'")
        return if count.positive?

        personal_dir = File.join(Dir.home, ".local", "share", "task", "personal")
        FileUtils.mkdir_p(personal_dir)

        git_dir = File.join(personal_dir, ".git")
        init_git_repo(personal_dir) unless File.exist?(git_dir)

        @db.execute(<<~SQL, [personal_dir])
          INSERT INTO projects (name, path, aliases, instructions)
          VALUES ('personal', ?, '', 'Default project for personal tasks')
        SQL
      end

      def init_git_repo(path)
        git_dir = File.join(path, ".git")
        FileUtils.mkdir_p(git_dir)

        config = <<~CONFIG
          [core]
          \trepositoryformatversion = 0
          \tfilemode = true
          \tbare = false
          \tlogallrefupdates = true
          [init]
          \tdefaultBranch = main
        CONFIG

        File.write(File.join(git_dir, "config"), config)
        File.write(File.join(git_dir, "HEAD"), "ref: refs/heads/main\n")

        FileUtils.mkdir_p(File.join(git_dir, "objects"))
        FileUtils.mkdir_p(File.join(git_dir, "refs", "heads"))

        readme = <<~README
          # Personal Tasks

          This is the default workspace for personal tasks.
        README

        File.write(File.join(path, "README.md"), readme)
      end

      def ensure_default_task_types
        count = @db.get_first_value("SELECT COUNT(*) FROM task_types")
        return if count.positive?

        defaults = [
          {
            name: "code",
            label: "Code",
            instructions: <<~INSTRUCTIONS,
              You are working on: {{project}}

              {{project_instructions}}

              {{memories}}

              Task: {{title}}

              {{body}}

              {{attachments}}

              {{history}}

              Instructions:
              - Explore the codebase to understand the context
              - Implement the solution
              - Write tests if applicable
              - Commit your changes with clear messages
              - Submit a pull request when your work is complete

              IMPORTANT: Your objective is to submit a PR to complete this task. Always remember to create and submit a pull request as the final step of your work. This is how you signal that the implementation is ready for review and merging.

              When finished, provide a summary of what you did:
              - List files changed/created
              - Describe the key changes made
              - Include any relevant links (PRs, commits, etc.)
              - Note any follow-up items or concerns
            INSTRUCTIONS
            sort_order: 1
          },
          {
            name: "writing",
            label: "Writing",
            instructions: <<~INSTRUCTIONS,
              You are a skilled writer. Please complete this task:

              {{project_instructions}}

              {{memories}}

              Task: {{title}}

              Details: {{body}}

              {{attachments}}

              {{history}}

              Write the requested content. Be professional, clear, and match the appropriate tone.
              Output the final content, then summarize what you created.
            INSTRUCTIONS
            sort_order: 2
          },
          {
            name: "thinking",
            label: "Thinking",
            instructions: <<~INSTRUCTIONS,
              You are a strategic advisor. Analyze this thoroughly:

              {{project_instructions}}

              {{memories}}

              Question: {{title}}

              Context: {{body}}

              {{attachments}}

              {{history}}

              Provide:
              1. Clear analysis of the question/problem
              2. Key considerations and tradeoffs
              3. Recommended approach
              4. Concrete next steps

              Think deeply but be actionable. Summarize your conclusions clearly.
            INSTRUCTIONS
            sort_order: 3
          }
        ]

        defaults.each do |d|
          @db.execute(<<~SQL, [d[:name], d[:label], d[:instructions], d[:sort_order]])
            INSERT INTO task_types (name, label, instructions, sort_order, is_builtin)
            VALUES (?, ?, ?, ?, 1)
          SQL
        end
      end

      def ensure_project_colors
        rows = @db.execute("SELECT id, name FROM projects WHERE color = '' OR color IS NULL ORDER BY id")

        rows.each_with_index do |row, i|
          color = DEFAULT_PROJECT_COLORS[i % DEFAULT_PROJECT_COLORS.length]
          @db.execute("UPDATE projects SET color = ? WHERE id = ?", [color, row["id"]])
        end
      end
    end
  end
end
