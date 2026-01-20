# frozen_string_literal: true

module Taskyou
  module DB
    class Project
      attr_accessor :id, :name, :path, :aliases, :instructions, :actions, :color, :created_at

      def initialize(attrs = {})
        @id = attrs[:id]
        @name = attrs[:name] || ""
        @path = attrs[:path] || ""
        @aliases = attrs[:aliases] || ""
        @instructions = attrs[:instructions] || ""
        @actions = attrs[:actions] || "[]"
        @color = attrs[:color] || ""
        @created_at = parse_time(attrs[:created_at])
      end

      def self.create(db, attrs)
        project = new(attrs)

        db.execute(<<~SQL, project.name, project.path, project.aliases, project.instructions, project.actions, project.color)
          INSERT INTO projects (name, path, aliases, instructions, actions, color)
          VALUES (?, ?, ?, ?, ?, ?)
        SQL

        project.id = db.last_insert_row_id
        project
      end

      def self.find(db, id)
        row = db.get_first_row(<<~SQL, id)
          SELECT id, name, path, COALESCE(aliases, '') as aliases,
                 COALESCE(instructions, '') as instructions,
                 COALESCE(actions, '[]') as actions,
                 COALESCE(color, '') as color,
                 created_at
          FROM projects WHERE id = ?
        SQL

        return nil if row.nil?

        from_row(row)
      end

      def self.find_by_name(db, name)
        row = db.get_first_row(<<~SQL, name)
          SELECT id, name, path, COALESCE(aliases, '') as aliases,
                 COALESCE(instructions, '') as instructions,
                 COALESCE(actions, '[]') as actions,
                 COALESCE(color, '') as color,
                 created_at
          FROM projects WHERE name = ?
        SQL

        return nil if row.nil?

        from_row(row)
      end

      def self.all(db)
        rows = db.execute(<<~SQL)
          SELECT id, name, path, COALESCE(aliases, '') as aliases,
                 COALESCE(instructions, '') as instructions,
                 COALESCE(actions, '[]') as actions,
                 COALESCE(color, '') as color,
                 created_at
          FROM projects
          ORDER BY name ASC
        SQL

        rows.map { |row| from_row(row) }
      end

      def self.find_by_path(db, path)
        row = db.get_first_row(<<~SQL, path)
          SELECT id, name, path, COALESCE(aliases, '') as aliases,
                 COALESCE(instructions, '') as instructions,
                 COALESCE(actions, '[]') as actions,
                 COALESCE(color, '') as color,
                 created_at
          FROM projects WHERE path = ?
        SQL

        return nil if row.nil?

        from_row(row)
      end

      def save(db)
        db.execute(<<~SQL, name, path, aliases, instructions, actions, color, id)
          UPDATE projects SET
            name = ?, path = ?, aliases = ?, instructions = ?, actions = ?, color = ?
          WHERE id = ?
        SQL
      end

      def delete(db)
        db.execute("DELETE FROM projects WHERE id = ?", id)
      end

      def aliases_list
        return [] if aliases.nil? || aliases.empty?

        aliases.split(",").map(&:strip)
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
          path: row["path"],
          aliases: row["aliases"],
          instructions: row["instructions"],
          actions: row["actions"],
          color: row["color"],
          created_at: row["created_at"]
        )
      end
    end
  end
end
