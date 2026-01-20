# frozen_string_literal: true

module Taskyou
  module DB
    class Memory
      # Memory categories
      CATEGORY_GENERAL = "general"
      CATEGORY_CONTEXT = "context"
      CATEGORY_PATTERNS = "patterns"
      CATEGORY_DECISIONS = "decisions"
      CATEGORY_GOTCHAS = "gotchas"

      CATEGORIES = [
        CATEGORY_GENERAL,
        CATEGORY_CONTEXT,
        CATEGORY_PATTERNS,
        CATEGORY_DECISIONS,
        CATEGORY_GOTCHAS
      ].freeze

      attr_accessor :id, :project, :category, :content, :source_task_id, :created_at, :updated_at

      def initialize(attrs = {})
        @id = attrs[:id]
        @project = attrs[:project] || ""
        @category = attrs[:category] || CATEGORY_GENERAL
        @content = attrs[:content] || ""
        @source_task_id = attrs[:source_task_id]
        @created_at = parse_time(attrs[:created_at])
        @updated_at = parse_time(attrs[:updated_at])
      end

      def self.create(db, attrs)
        memory = new(attrs)

        db.execute(<<~SQL, memory.project, memory.category, memory.content, memory.source_task_id)
          INSERT INTO project_memories (project, category, content, source_task_id)
          VALUES (?, ?, ?, ?)
        SQL

        memory.id = db.last_insert_row_id
        memory
      end

      def self.find(db, id)
        row = db.get_first_row(<<~SQL, id)
          SELECT id, project, category, content, source_task_id, created_at, updated_at
          FROM project_memories WHERE id = ?
        SQL

        return nil if row.nil?

        from_row(row)
      end

      def self.for_project(db, project, category = nil)
        if category
          rows = db.execute(<<~SQL, project, category)
            SELECT id, project, category, content, source_task_id, created_at, updated_at
            FROM project_memories
            WHERE project = ? AND category = ?
            ORDER BY created_at DESC
          SQL
        else
          rows = db.execute(<<~SQL, project)
            SELECT id, project, category, content, source_task_id, created_at, updated_at
            FROM project_memories
            WHERE project = ?
            ORDER BY category ASC, created_at DESC
          SQL
        end

        rows.map { |row| from_row(row) }
      end

      def self.for_project_grouped(db, project)
        memories = for_project(db, project)
        memories.group_by(&:category)
      end

      def save(db)
        db.execute(<<~SQL, project, category, content, source_task_id, id)
          UPDATE project_memories SET
            project = ?, category = ?, content = ?, source_task_id = ?,
            updated_at = CURRENT_TIMESTAMP
          WHERE id = ?
        SQL
      end

      def delete(db)
        db.execute("DELETE FROM project_memories WHERE id = ?", id)
      end

      def self.delete_for_project(db, project)
        db.execute("DELETE FROM project_memories WHERE project = ?", project)
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
          project: row["project"],
          category: row["category"],
          content: row["content"],
          source_task_id: row["source_task_id"],
          created_at: row["created_at"],
          updated_at: row["updated_at"]
        )
      end
    end
  end
end
