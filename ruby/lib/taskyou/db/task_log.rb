# frozen_string_literal: true

module Taskyou
  module DB
    class TaskLog
      attr_accessor :id, :task_id, :line_type, :content, :created_at

      def initialize(attrs = {})
        @id = attrs[:id]
        @task_id = attrs[:task_id]
        @line_type = attrs[:line_type] || "output"
        @content = attrs[:content] || ""
        @created_at = parse_time(attrs[:created_at])
      end

      def self.append(db, task_id, line_type, content)
        db.execute(<<~SQL, task_id, line_type, content)
          INSERT INTO task_logs (task_id, line_type, content)
          VALUES (?, ?, ?)
        SQL
      end

      def self.for_task(db, task_id, limit = nil)
        sql = <<~SQL
          SELECT id, task_id, line_type, content, created_at
          FROM task_logs
          WHERE task_id = ?
          ORDER BY id ASC
        SQL

        sql += " LIMIT #{limit}" if limit

        rows = db.execute(sql, task_id)
        rows.map { |row| from_row(row) }
      end

      def self.recent_for_task(db, task_id, limit = 100)
        rows = db.execute(<<~SQL, task_id, limit)
          SELECT id, task_id, line_type, content, created_at
          FROM task_logs
          WHERE task_id = ?
          ORDER BY id DESC
          LIMIT ?
        SQL

        rows.reverse.map { |row| from_row(row) }
      end

      def self.clear_for_task(db, task_id)
        db.execute("DELETE FROM task_logs WHERE task_id = ?", task_id)
      end

      def self.count_for_task(db, task_id)
        db.get_first_value("SELECT COUNT(*) FROM task_logs WHERE task_id = ?", task_id)
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
          task_id: row["task_id"],
          line_type: row["line_type"],
          content: row["content"],
          created_at: row["created_at"]
        )
      end
    end
  end
end
