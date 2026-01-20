# frozen_string_literal: true

require "time"

module Taskyou
  module DB
    class Task
      # Task statuses
      STATUS_BACKLOG = "backlog"
      STATUS_QUEUED = "queued"
      STATUS_PROCESSING = "processing"
      STATUS_BLOCKED = "blocked"
      STATUS_DONE = "done"
      STATUS_ARCHIVED = "archived"

      # Task types (default values, actual types are stored in task_types table)
      TYPE_CODE = "code"
      TYPE_WRITING = "writing"
      TYPE_THINKING = "thinking"

      # Task executors
      EXECUTOR_CLAUDE = "claude"
      EXECUTOR_CODEX = "codex"

      # Recurrence patterns
      RECURRENCE_NONE = ""
      RECURRENCE_HOURLY = "hourly"
      RECURRENCE_DAILY = "daily"
      RECURRENCE_WEEKLY = "weekly"
      RECURRENCE_MONTHLY = "monthly"

      # Port allocation constants
      PORT_RANGE_START = 3100
      PORT_RANGE_END = 4099

      attr_accessor :id, :title, :body, :status, :type, :project, :executor,
                    :worktree_path, :branch_name, :port, :claude_session_id,
                    :daemon_session, :tmux_window_id, :pr_url, :pr_number,
                    :dangerous_mode, :tags, :created_at, :updated_at,
                    :started_at, :completed_at, :scheduled_at, :recurrence, :last_run_at

      def initialize(attrs = {})
        @id = attrs[:id]
        @title = attrs[:title] || ""
        @body = attrs[:body] || ""
        @status = attrs[:status] || STATUS_BACKLOG
        @type = attrs[:type] || ""
        @project = attrs[:project] || ""
        @executor = attrs[:executor] || EXECUTOR_CLAUDE
        @worktree_path = attrs[:worktree_path] || ""
        @branch_name = attrs[:branch_name] || ""
        @port = attrs[:port] || 0
        @claude_session_id = attrs[:claude_session_id] || ""
        @daemon_session = attrs[:daemon_session] || ""
        @tmux_window_id = attrs[:tmux_window_id] || ""
        @pr_url = attrs[:pr_url] || ""
        @pr_number = attrs[:pr_number] || 0
        @dangerous_mode = attrs[:dangerous_mode] || false
        @tags = attrs[:tags] || ""
        @created_at = parse_time(attrs[:created_at])
        @updated_at = parse_time(attrs[:updated_at])
        @started_at = parse_time(attrs[:started_at])
        @completed_at = parse_time(attrs[:completed_at])
        @scheduled_at = parse_time(attrs[:scheduled_at])
        @recurrence = attrs[:recurrence] || ""
        @last_run_at = parse_time(attrs[:last_run_at])
      end

      def self.default_executor
        EXECUTOR_CLAUDE
      end

      def in_progress?
        status == STATUS_QUEUED || status == STATUS_PROCESSING
      end

      def scheduled?
        !scheduled_at.nil?
      end

      def recurring?
        !recurrence.nil? && recurrence != ""
      end

      # Database operations - class methods that take a db instance

      def self.create(db, attrs)
        task = new(attrs)
        task.project = "personal" if task.project.empty?
        task.executor = default_executor if task.executor.empty?

        # Validate project exists
        project = Project.find_by_name(db, task.project)
        raise ProjectNotFoundError, "Project not found: #{task.project}" if project.nil?

        db.execute(<<~SQL, task.title, task.body, task.status, task.type, task.project, task.executor, task.scheduled_at, task.recurrence, task.last_run_at)
          INSERT INTO tasks (title, body, status, type, project, executor, scheduled_at, recurrence, last_run_at)
          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        SQL

        task.id = db.last_insert_row_id
        task
      end

      def self.find(db, id)
        row = db.get_first_row(<<~SQL, id)
          SELECT id, title, body, status, type, project, COALESCE(executor, 'claude') as executor,
                 worktree_path, branch_name, port, claude_session_id,
                 COALESCE(daemon_session, '') as daemon_session,
                 COALESCE(tmux_window_id, '') as tmux_window_id,
                 COALESCE(pr_url, '') as pr_url,
                 COALESCE(pr_number, 0) as pr_number,
                 COALESCE(dangerous_mode, 0) as dangerous_mode,
                 COALESCE(tags, '') as tags,
                 created_at, updated_at, started_at, completed_at,
                 scheduled_at, recurrence, last_run_at
          FROM tasks WHERE id = ?
        SQL

        return nil if row.nil?

        from_row(row)
      end

      def self.list(db, opts = {})
        query = <<~SQL
          SELECT id, title, body, status, type, project, COALESCE(executor, 'claude') as executor,
                 worktree_path, branch_name, port, claude_session_id,
                 COALESCE(daemon_session, '') as daemon_session,
                 COALESCE(tmux_window_id, '') as tmux_window_id,
                 COALESCE(pr_url, '') as pr_url,
                 COALESCE(pr_number, 0) as pr_number,
                 COALESCE(dangerous_mode, 0) as dangerous_mode,
                 COALESCE(tags, '') as tags,
                 created_at, updated_at, started_at, completed_at,
                 scheduled_at, recurrence, last_run_at
          FROM tasks WHERE 1=1
        SQL

        args = []

        if opts[:status]
          query += " AND status = ?"
          args << opts[:status]
        end

        if opts[:type]
          query += " AND type = ?"
          args << opts[:type]
        end

        if opts[:project]
          query += " AND project = ?"
          args << opts[:project]
        end

        # Exclude done and archived by default
        if opts[:status].nil? && !opts[:include_closed]
          query += " AND status NOT IN ('done', 'archived')"
        end

        query += " ORDER BY CASE WHEN status IN ('done', 'blocked') THEN completed_at ELSE created_at END DESC, id DESC"

        limit = opts[:limit] || 100
        query += " LIMIT #{limit}"

        if opts[:offset]&.positive?
          query += " OFFSET #{opts[:offset]}"
        end

        rows = db.execute(query, *args)
        rows.map { |row| from_row(row) }
      end

      def self.search(db, query_str, limit = 50)
        sql = <<~SQL
          SELECT id, title, body, status, type, project, COALESCE(executor, 'claude') as executor,
                 worktree_path, branch_name, port, claude_session_id,
                 COALESCE(daemon_session, '') as daemon_session,
                 COALESCE(tmux_window_id, '') as tmux_window_id,
                 COALESCE(pr_url, '') as pr_url,
                 COALESCE(pr_number, 0) as pr_number,
                 COALESCE(dangerous_mode, 0) as dangerous_mode,
                 COALESCE(tags, '') as tags,
                 created_at, updated_at, started_at, completed_at,
                 scheduled_at, recurrence, last_run_at
          FROM tasks
          WHERE (
            title LIKE ? COLLATE NOCASE
            OR project LIKE ? COLLATE NOCASE
            OR CAST(id AS TEXT) LIKE ?
            OR CAST(pr_number AS TEXT) LIKE ?
            OR pr_url LIKE ? COLLATE NOCASE
          )
          ORDER BY CASE WHEN status IN ('done', 'blocked') THEN completed_at ELSE created_at END DESC, id DESC
          LIMIT ?
        SQL

        pattern = "%#{query_str}%"
        rows = db.execute(sql, pattern, pattern, pattern, pattern, pattern, limit)
        rows.map { |row| from_row(row) }
      end

      def self.count_by_status(db, status)
        db.get_first_value("SELECT COUNT(*) FROM tasks WHERE status = ?", status)
      end

      def self.next_queued(db)
        row = db.get_first_row(<<~SQL, STATUS_QUEUED)
          SELECT id, title, body, status, type, project, COALESCE(executor, 'claude') as executor,
                 worktree_path, branch_name, port, claude_session_id,
                 COALESCE(daemon_session, '') as daemon_session,
                 COALESCE(tmux_window_id, '') as tmux_window_id,
                 COALESCE(pr_url, '') as pr_url,
                 COALESCE(pr_number, 0) as pr_number,
                 COALESCE(dangerous_mode, 0) as dangerous_mode,
                 COALESCE(tags, '') as tags,
                 created_at, updated_at, started_at, completed_at,
                 scheduled_at, recurrence, last_run_at
          FROM tasks
          WHERE status = ?
          ORDER BY created_at ASC
          LIMIT 1
        SQL

        return nil if row.nil?

        from_row(row)
      end

      def self.queued(db)
        rows = db.execute(<<~SQL, STATUS_QUEUED)
          SELECT id, title, body, status, type, project, COALESCE(executor, 'claude') as executor,
                 worktree_path, branch_name, port, claude_session_id,
                 COALESCE(daemon_session, '') as daemon_session,
                 COALESCE(tmux_window_id, '') as tmux_window_id,
                 COALESCE(pr_url, '') as pr_url,
                 COALESCE(pr_number, 0) as pr_number,
                 COALESCE(dangerous_mode, 0) as dangerous_mode,
                 COALESCE(tags, '') as tags,
                 created_at, updated_at, started_at, completed_at,
                 scheduled_at, recurrence, last_run_at
          FROM tasks
          WHERE status = ?
          ORDER BY created_at ASC
        SQL

        rows.map { |row| from_row(row) }
      end

      def self.active_ports(db)
        rows = db.execute(<<~SQL, STATUS_DONE, STATUS_ARCHIVED)
          SELECT port FROM tasks
          WHERE port > 0 AND status NOT IN (?, ?)
        SQL

        rows.each_with_object({}) { |row, hash| hash[row["port"]] = true }
      end

      def self.allocate_port(db, task_id)
        used_ports = active_ports(db)

        (PORT_RANGE_START..PORT_RANGE_END).each do |port|
          next if used_ports[port]

          db.execute("UPDATE tasks SET port = ? WHERE id = ?", port, task_id)
          return port
        end

        raise DatabaseError, "No available ports in range #{PORT_RANGE_START}-#{PORT_RANGE_END}"
      end

      # Instance methods for updates

      def save(db)
        db.execute(<<~SQL, title, body, status, type, project, executor, worktree_path, branch_name, port, claude_session_id, daemon_session, pr_url, pr_number, dangerous_mode ? 1 : 0, tags, scheduled_at, recurrence, last_run_at, id)
          UPDATE tasks SET
            title = ?, body = ?, status = ?, type = ?, project = ?, executor = ?,
            worktree_path = ?, branch_name = ?, port = ?, claude_session_id = ?,
            daemon_session = ?, pr_url = ?, pr_number = ?, dangerous_mode = ?,
            tags = ?, scheduled_at = ?, recurrence = ?, last_run_at = ?,
            updated_at = CURRENT_TIMESTAMP
          WHERE id = ?
        SQL
      end

      def update_status(db, new_status)
        query = "UPDATE tasks SET status = ?, updated_at = CURRENT_TIMESTAMP"

        case new_status
        when STATUS_PROCESSING
          query += ", started_at = CURRENT_TIMESTAMP"
        when STATUS_DONE, STATUS_BLOCKED, STATUS_ARCHIVED
          query += ", completed_at = CURRENT_TIMESTAMP"
        end

        query += " WHERE id = ?"
        db.execute(query, new_status, id)
        @status = new_status
      end

      def mark_started(db)
        db.execute(<<~SQL, id)
          UPDATE tasks SET started_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
          WHERE id = ? AND started_at IS NULL
        SQL
      end

      def update_claude_session_id(db, session_id)
        db.execute(<<~SQL, session_id, id)
          UPDATE tasks SET claude_session_id = ?, updated_at = CURRENT_TIMESTAMP
          WHERE id = ?
        SQL
        @claude_session_id = session_id
      end

      def update_dangerous_mode(db, mode)
        db.execute(<<~SQL, mode ? 1 : 0, id)
          UPDATE tasks SET dangerous_mode = ?, updated_at = CURRENT_TIMESTAMP
          WHERE id = ?
        SQL
        @dangerous_mode = mode
      end

      def update_daemon_session(db, session)
        db.execute(<<~SQL, session, id)
          UPDATE tasks SET daemon_session = ?, updated_at = CURRENT_TIMESTAMP
          WHERE id = ?
        SQL
        @daemon_session = session
      end

      def update_window_id(db, window_id)
        db.execute(<<~SQL, window_id, id)
          UPDATE tasks SET tmux_window_id = ?, updated_at = CURRENT_TIMESTAMP
          WHERE id = ?
        SQL
        @tmux_window_id = window_id
      end

      def delete(db)
        db.execute("DELETE FROM tasks WHERE id = ?", id)
      end

      def retry(db, feedback = nil)
        TaskLog.append(db, id, "system", "--- Continuation ---")
        TaskLog.append(db, id, "text", "Feedback: #{feedback}") if feedback && !feedback.empty?
        update_status(db, STATUS_QUEUED)
      end

      private

      def parse_time(value)
        return nil if value.nil?
        return value if value.is_a?(Time)

        Time.parse(value).localtime
      rescue ArgumentError
        nil
      end

      def self.from_row(row)
        new(
          id: row["id"],
          title: row["title"],
          body: row["body"],
          status: row["status"],
          type: row["type"],
          project: row["project"],
          executor: row["executor"],
          worktree_path: row["worktree_path"],
          branch_name: row["branch_name"],
          port: row["port"],
          claude_session_id: row["claude_session_id"],
          daemon_session: row["daemon_session"],
          tmux_window_id: row["tmux_window_id"],
          pr_url: row["pr_url"],
          pr_number: row["pr_number"],
          dangerous_mode: row["dangerous_mode"] == 1,
          tags: row["tags"],
          created_at: row["created_at"],
          updated_at: row["updated_at"],
          started_at: row["started_at"],
          completed_at: row["completed_at"],
          scheduled_at: row["scheduled_at"],
          recurrence: row["recurrence"],
          last_run_at: row["last_run_at"]
        )
      end
    end
  end
end
