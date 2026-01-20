# frozen_string_literal: true

module Taskyou
  module DB
    class TaskType
      attr_accessor :id, :name, :label, :instructions, :sort_order, :is_builtin, :created_at

      def initialize(attrs = {})
        @id = attrs[:id]
        @name = attrs[:name] || ""
        @label = attrs[:label] || ""
        @instructions = attrs[:instructions] || ""
        @sort_order = attrs[:sort_order] || 0
        @is_builtin = attrs[:is_builtin] || false
        @created_at = parse_time(attrs[:created_at])
      end

      def self.all(db)
        rows = db.execute(<<~SQL)
          SELECT id, name, label, instructions, sort_order, is_builtin, created_at
          FROM task_types
          ORDER BY sort_order ASC, name ASC
        SQL

        rows.map { |row| from_row(row) }
      end

      def self.find(db, id)
        row = db.get_first_row(<<~SQL, id)
          SELECT id, name, label, instructions, sort_order, is_builtin, created_at
          FROM task_types WHERE id = ?
        SQL

        return nil if row.nil?

        from_row(row)
      end

      def self.find_by_name(db, name)
        row = db.get_first_row(<<~SQL, name)
          SELECT id, name, label, instructions, sort_order, is_builtin, created_at
          FROM task_types WHERE name = ?
        SQL

        return nil if row.nil?

        from_row(row)
      end

      def self.create(db, attrs)
        task_type = new(attrs)

        db.execute(<<~SQL, task_type.name, task_type.label, task_type.instructions, task_type.sort_order, task_type.is_builtin ? 1 : 0)
          INSERT INTO task_types (name, label, instructions, sort_order, is_builtin)
          VALUES (?, ?, ?, ?, ?)
        SQL

        task_type.id = db.last_insert_row_id
        task_type
      end

      def save(db)
        db.execute(<<~SQL, name, label, instructions, sort_order, id)
          UPDATE task_types SET
            name = ?, label = ?, instructions = ?, sort_order = ?
          WHERE id = ?
        SQL
      end

      def delete(db)
        return false if is_builtin

        db.execute("DELETE FROM task_types WHERE id = ? AND is_builtin = 0", id)
        true
      end

      def builtin?
        is_builtin
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
          name: row["name"],
          label: row["label"],
          instructions: row["instructions"],
          sort_order: row["sort_order"],
          is_builtin: row["is_builtin"] == 1,
          created_at: row["created_at"]
        )
      end
    end
  end
end
