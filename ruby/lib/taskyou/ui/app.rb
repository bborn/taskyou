# frozen_string_literal: true

# NOTE: This is a stub implementation. Full implementation requires bubbletea-ruby gem.
# When bubbletea-ruby is properly installed, this will be replaced with actual TUI code.

module Taskyou
  module UI
    class App
      attr_reader :db, :executor, :working_dir, :theme

      def initialize(db, executor, working_dir = nil)
        @db = db
        @executor = executor
        @working_dir = working_dir
        @theme = Theme.new
        @current_view = :kanban
        @selected_task = nil
      end

      # Bubble Tea Model interface methods (stubs)

      def init
        # Return initial command (nil for no command)
        nil
      end

      def update(message)
        # Process message and return [model, command]
        # This is a stub - actual implementation would handle KeyMessage, MouseMessage, etc.
        [self, nil]
      end

      def view
        # Return string representation of current view
        # This is a stub - actual implementation would render full TUI
        <<~VIEW
          TaskYou Ruby (TUI stub)
          ========================

          This is a placeholder for the Bubble Tea TUI.
          Full implementation requires bubbletea-ruby gem.

          Current view: #{@current_view}
          Tasks in database: #{task_count}

          Press 'q' to quit.
        VIEW
      end

      private

      def task_count
        DB::Task.list(@db, include_closed: true).count
      end
    end
  end
end
