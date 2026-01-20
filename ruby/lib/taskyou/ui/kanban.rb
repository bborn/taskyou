# frozen_string_literal: true

# NOTE: This is a stub implementation. Full implementation requires bubbletea-ruby gem.

module Taskyou
  module UI
    class Kanban
      COLUMNS = [
        { status: DB::Task::STATUS_BACKLOG, title: "Backlog" },
        { status: DB::Task::STATUS_QUEUED, title: "Queued" },
        { status: DB::Task::STATUS_PROCESSING, title: "In Progress" },
        { status: DB::Task::STATUS_BLOCKED, title: "Blocked" },
        { status: DB::Task::STATUS_DONE, title: "Done" }
      ].freeze

      attr_reader :db, :selected_column, :selected_row

      def initialize(db)
        @db = db
        @selected_column = 0
        @selected_row = 0
        @tasks_by_column = {}
        refresh_tasks
      end

      def refresh_tasks
        @tasks_by_column = {}
        COLUMNS.each do |col|
          @tasks_by_column[col[:status]] = DB::Task.list(@db, status: col[:status])
        end
      end

      def tasks_for_column(index)
        col = COLUMNS[index]
        @tasks_by_column[col[:status]] || []
      end

      def selected_task
        tasks = tasks_for_column(@selected_column)
        return nil if tasks.empty? || @selected_row >= tasks.length

        tasks[@selected_row]
      end

      def move_left
        @selected_column = [@selected_column - 1, 0].max
        clamp_row
      end

      def move_right
        @selected_column = [@selected_column + 1, COLUMNS.length - 1].min
        clamp_row
      end

      def move_up
        @selected_row = [@selected_row - 1, 0].max
      end

      def move_down
        tasks = tasks_for_column(@selected_column)
        @selected_row = [@selected_row + 1, tasks.length - 1].max
      end

      def view
        # Stub view - actual implementation would use lipgloss for styling
        lines = []
        lines << "=" * 80
        lines << COLUMNS.map { |c| c[:title].center(15) }.join(" | ")
        lines << "=" * 80

        max_rows = COLUMNS.map { |c| @tasks_by_column[c[:status]]&.length || 0 }.max

        (0...max_rows).each do |row|
          row_parts = COLUMNS.map.with_index do |col, col_idx|
            tasks = @tasks_by_column[col[:status]] || []
            if row < tasks.length
              task = tasks[row]
              selected = col_idx == @selected_column && row == @selected_row
              prefix = selected ? ">" : " "
              "#{prefix}##{task.id} #{task.title.slice(0, 12)}"
            else
              " " * 15
            end
          end
          lines << row_parts.join(" | ")
        end

        lines.join("\n")
      end

      private

      def clamp_row
        tasks = tasks_for_column(@selected_column)
        @selected_row = [[@selected_row, 0].max, [tasks.length - 1, 0].max].min
      end
    end
  end
end
