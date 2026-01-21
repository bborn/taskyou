# frozen_string_literal: true

require "bubbletea"

module Taskyou
  module UI
    # Kanban board view for task management
    class Kanban
      include Bubbletea::Model

      COLUMNS = [
        { status: DB::Task::STATUS_BACKLOG, title: "Backlog", key: "1" },
        { status: DB::Task::STATUS_QUEUED, title: "Queued", key: "2" },
        { status: DB::Task::STATUS_PROCESSING, title: "In Progress", key: "3" },
        { status: DB::Task::STATUS_BLOCKED, title: "Blocked", key: "4" }
      ].freeze

      attr_reader :db, :selected_column, :selected_row, :width, :height
      attr_accessor :tasks_by_column

      def initialize(db, width: 80, height: 24)
        @db = db
        @selected_column = 0
        @selected_row = 0
        @width = width
        @height = height
        @tasks_by_column = {}
        @column_scroll = Array.new(COLUMNS.length, 0)
        refresh_tasks
      end

      def init
        [self, nil]
      end

      def update(message)
        case message
        when Bubbletea::KeyMessage
          handle_key(message)
        when Bubbletea::WindowSizeMessage
          @width = message.width
          @height = message.height
          [self, nil]
        else
          [self, nil]
        end
      end

      def view
        return "Loading..." if @tasks_by_column.empty?

        lines = []

        # Title
        title = Styles.title_style.render(" TaskYou ")
        lines << title
        lines << ""

        # Calculate column width
        col_width = [(@width - 4) / COLUMNS.length, 20].max

        # Headers
        headers = COLUMNS.map.with_index do |col, idx|
          selected = idx == @selected_column
          style = Styles.column_header_style(selected: selected)
          text = "#{col[:title]} (#{tasks_for_column(idx).length})"
          style.width(col_width).render(text)
        end
        lines << headers.join(" ")
        lines << Styles.divider(@width - 4)

        # Task cards - render rows
        max_visible_rows = [@height - 10, 5].max
        (0...max_visible_rows).each do |row|
          row_parts = COLUMNS.map.with_index do |_, col_idx|
            tasks = tasks_for_column(col_idx)
            scroll = @column_scroll[col_idx]
            actual_row = row + scroll

            if actual_row < tasks.length
              task = tasks[actual_row]
              selected = col_idx == @selected_column && actual_row == @selected_row
              render_task_card(task, col_width, selected)
            else
              " " * col_width
            end
          end
          lines << row_parts.join(" ")
        end

        # Help footer
        lines << ""
        lines << Styles.divider(@width - 4)
        lines << render_help

        lines.join("\n")
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

      private

      def handle_key(key)
        case key.to_s
        when "left", "h"
          move_left
        when "right", "l"
          move_right
        when "up", "k"
          move_up
        when "down", "j"
          move_down
        when "1"
          @selected_column = 0
          clamp_row
        when "2"
          @selected_column = 1
          clamp_row
        when "3"
          @selected_column = 2
          clamp_row
        when "4"
          @selected_column = 3
          clamp_row
        when "g"
          # Go to top
          @selected_row = 0
        when "G"
          # Go to bottom
          tasks = tasks_for_column(@selected_column)
          @selected_row = [tasks.length - 1, 0].max
        end
        [self, nil]
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
        adjust_scroll
      end

      def move_down
        tasks = tasks_for_column(@selected_column)
        @selected_row = [@selected_row + 1, [tasks.length - 1, 0].max].min
        adjust_scroll
      end

      def clamp_row
        tasks = tasks_for_column(@selected_column)
        @selected_row = [[@selected_row, 0].max, [tasks.length - 1, 0].max].min
        adjust_scroll
      end

      def adjust_scroll
        max_visible = [@height - 10, 5].max
        scroll = @column_scroll[@selected_column]

        # Scroll down if needed
        if @selected_row >= scroll + max_visible
          @column_scroll[@selected_column] = @selected_row - max_visible + 1
        end

        # Scroll up if needed
        if @selected_row < scroll
          @column_scroll[@selected_column] = @selected_row
        end
      end

      def render_task_card(task, width, selected)
        style = Styles.task_card_style(status: task.status, selected: selected)

        # Truncate title
        title = task.title.length > width - 4 ? "#{task.title[0, width - 7]}..." : task.title

        # Build card content
        content = []
        content << "##{task.id} #{title}"

        # Add project label
        project_style = Styles.project_label_style
        content << project_style.render(task.project)

        style.width(width - 2).render(content.join("\n"))
      end

      def render_help
        bindings = [
          ["←/h", "prev"],
          ["→/l", "next"],
          ["↑/k", "up"],
          ["↓/j", "down"],
          ["enter", "view"],
          ["n", "new"],
          ["x", "exec"],
          ["q", "quit"]
        ]

        help_parts = bindings.map do |key, desc|
          key_text = Styles.key_style.render(key)
          desc_text = Styles.desc_style.render(desc)
          "#{key_text} #{desc_text}"
        end

        Styles.help_style.render(help_parts.join("  "))
      end
    end
  end
end
