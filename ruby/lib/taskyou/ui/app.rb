# frozen_string_literal: true

require "bubbletea"
require "lipgloss"

module Taskyou
  module UI
    # View represents the current view state
    module View
      DASHBOARD = :dashboard
      DETAIL = :detail
      NEW_TASK = :new_task
      EDIT_TASK = :edit_task
      DELETE_CONFIRM = :delete_confirm
      CLOSE_CONFIRM = :close_confirm
      ARCHIVE_CONFIRM = :archive_confirm
      QUIT_CONFIRM = :quit_confirm
      SETTINGS = :settings
      MEMORIES = :memories
    end

    # KeyMap defines the key bindings for the application
    class KeyMap
      BINDINGS = {
        left: { keys: %w[left h], help: "←/h", desc: "prev col" },
        right: { keys: %w[right l], help: "→/l", desc: "next col" },
        up: { keys: %w[up k], help: "↑/k", desc: "up" },
        down: { keys: %w[down j], help: "↓/j", desc: "down" },
        enter: { keys: %w[enter], help: "enter", desc: "view" },
        back: { keys: %w[esc], help: "esc", desc: "back" },
        new: { keys: %w[n], help: "n", desc: "new" },
        edit: { keys: %w[e], help: "e", desc: "edit" },
        queue: { keys: %w[x], help: "x", desc: "execute" },
        retry: { keys: %w[r], help: "r", desc: "retry" },
        close: { keys: %w[c], help: "c", desc: "close" },
        archive: { keys: %w[a], help: "a", desc: "archive" },
        delete: { keys: %w[d], help: "d", desc: "delete" },
        refresh: { keys: %w[R], help: "R", desc: "refresh" },
        settings: { keys: %w[s], help: "s", desc: "settings" },
        memories: { keys: %w[m], help: "m", desc: "memories" },
        help: { keys: %w[?], help: "?", desc: "help" },
        quit: { keys: %w[ctrl+c q], help: "q/ctrl+c", desc: "quit" },
        filter: { keys: %w[/], help: "/", desc: "filter" },
        focus_backlog: { keys: %w[1], help: "1", desc: "backlog" },
        focus_queued: { keys: %w[2], help: "2", desc: "queued" },
        focus_processing: { keys: %w[3], help: "3", desc: "in progress" },
        focus_blocked: { keys: %w[4], help: "4", desc: "blocked" },
        go_top: { keys: %w[g], help: "g", desc: "top" },
        go_bottom: { keys: %w[G], help: "G", desc: "bottom" }
      }.freeze

      def self.match?(key_string, action)
        binding = BINDINGS[action]
        return false unless binding

        binding[:keys].include?(key_string)
      end

      def self.short_help
        %i[left right up down enter new queue filter quit].map do |action|
          binding = BINDINGS[action]
          "#{binding[:help]} #{binding[:desc]}"
        end
      end
    end

    # Main application model
    class App
      include Bubbletea::Model

      attr_reader :db, :executor, :working_dir
      attr_reader :current_view, :kanban, :selected_task
      attr_reader :width, :height, :notification

      def initialize(db, executor, working_dir = nil)
        @db = db
        @executor = executor
        @working_dir = working_dir

        @current_view = View::DASHBOARD
        @previous_view = nil
        @selected_task = nil

        @width = 80
        @height = 24
        @loading = true
        @notification = nil
        @notification_until = nil

        @filter_text = ""
        @filter_active = false
        @show_help = false

        # Initialize kanban board
        @kanban = Kanban.new(@db, width: @width, height: @height)
      end

      def init
        # Load tasks on initialization
        @kanban.refresh_tasks
        @loading = false
        nil
      end

      def update(message)
        # Clear expired notifications
        clear_expired_notification

        case message
        when Bubbletea::KeyMessage
          handle_key(message)
        when Bubbletea::WindowSizeMessage
          handle_resize(message)
        else
          [self, nil]
        end
      end

      def view
        return render_loading if @loading

        case @current_view
        when View::DASHBOARD
          render_dashboard
        when View::DETAIL
          render_detail
        when View::QUIT_CONFIRM
          render_quit_confirm
        when View::DELETE_CONFIRM
          render_delete_confirm
        when View::CLOSE_CONFIRM
          render_close_confirm
        else
          render_dashboard
        end
      end

      def set_notification(message, duration_seconds = 3)
        @notification = message
        @notification_until = Time.now + duration_seconds
      end

      def refresh_tasks
        @kanban.refresh_tasks
      end

      private

      def clear_expired_notification
        return unless @notification_until && Time.now > @notification_until

        @notification = nil
        @notification_until = nil
      end

      def handle_key(key)
        key_str = key.to_s

        case @current_view
        when View::DASHBOARD
          handle_dashboard_key(key_str)
        when View::DETAIL
          handle_detail_key(key_str)
        when View::QUIT_CONFIRM
          handle_quit_confirm_key(key_str)
        when View::DELETE_CONFIRM
          handle_delete_confirm_key(key_str)
        when View::CLOSE_CONFIRM
          handle_close_confirm_key(key_str)
        else
          [self, nil]
        end
      end

      def handle_dashboard_key(key_str)
        # Check for quit
        if KeyMap.match?(key_str, :quit)
          @previous_view = @current_view
          @current_view = View::QUIT_CONFIRM
          return [self, nil]
        end

        # Navigation keys are handled by kanban
        case key_str
        when "left", "h", "right", "l", "up", "k", "down", "j",
             "1", "2", "3", "4", "g", "G"
          @kanban.update(Bubbletea::KeyMessage.new(key_str))

        when "enter"
          # View selected task
          task = @kanban.selected_task
          if task
            @selected_task = task
            @current_view = View::DETAIL
          end

        when "n"
          # New task (would open form - simplified for now)
          set_notification("New task form not yet implemented")

        when "x"
          # Execute/queue selected task
          task = @kanban.selected_task
          if task
            queue_task(task)
          end

        when "c"
          # Close task
          task = @kanban.selected_task
          if task
            @pending_close_task = task
            @current_view = View::CLOSE_CONFIRM
          end

        when "d"
          # Delete task
          task = @kanban.selected_task
          if task
            @pending_delete_task = task
            @current_view = View::DELETE_CONFIRM
          end

        when "R"
          # Refresh
          refresh_tasks
          set_notification("Tasks refreshed")

        when "?"
          # Toggle help
          @show_help = !@show_help
        end

        [self, nil]
      end

      def handle_detail_key(key_str)
        case key_str
        when "esc", "q"
          @current_view = View::DASHBOARD
          @selected_task = nil
        when "x"
          if @selected_task
            queue_task(@selected_task)
            @current_view = View::DASHBOARD
            @selected_task = nil
          end
        when "c"
          if @selected_task
            @pending_close_task = @selected_task
            @current_view = View::CLOSE_CONFIRM
          end
        when "d"
          if @selected_task
            @pending_delete_task = @selected_task
            @current_view = View::DELETE_CONFIRM
          end
        end
        [self, nil]
      end

      def handle_quit_confirm_key(key_str)
        case key_str
        when "y", "Y"
          return [self, Bubbletea::Quit.new]
        when "n", "N", "esc"
          @current_view = @previous_view || View::DASHBOARD
        end
        [self, nil]
      end

      def handle_delete_confirm_key(key_str)
        case key_str
        when "y", "Y"
          if @pending_delete_task
            DB::Task.delete(@db, @pending_delete_task.id)
            refresh_tasks
            set_notification("Task ##{@pending_delete_task.id} deleted")
            @pending_delete_task = nil
          end
          @current_view = View::DASHBOARD
        when "n", "N", "esc"
          @pending_delete_task = nil
          @current_view = View::DASHBOARD
        end
        [self, nil]
      end

      def handle_close_confirm_key(key_str)
        case key_str
        when "y", "Y"
          if @pending_close_task
            DB::Task.update_status(@db, @pending_close_task.id, DB::Task::STATUS_DONE)
            refresh_tasks
            set_notification("Task ##{@pending_close_task.id} closed")
            @pending_close_task = nil
          end
          @current_view = View::DASHBOARD
          @selected_task = nil
        when "n", "N", "esc"
          @pending_close_task = nil
          @current_view = View::DASHBOARD
        end
        [self, nil]
      end

      def handle_resize(message)
        @width = message.width
        @height = message.height
        @kanban.update(message)
        [self, nil]
      end

      def queue_task(task)
        # Queue the task for processing
        DB::Task.update_status(@db, task.id, DB::Task::STATUS_QUEUED)
        refresh_tasks
        set_notification("Task ##{task.id} queued for execution")
      end

      def render_loading
        Styles.muted_style.render("Loading...")
      end

      def render_dashboard
        lines = []

        # Notification bar
        if @notification
          lines << Styles.success_style.render(" #{@notification} ")
          lines << ""
        end

        # Kanban board
        lines << @kanban.view

        # Help footer
        if @show_help
          lines << ""
          lines << render_full_help
        end

        lines.join("\n")
      end

      def render_detail
        return "No task selected" unless @selected_task

        lines = []

        # Title bar
        title = Styles.title_style.render(" Task ##{@selected_task.id} ")
        lines << title
        lines << ""

        # Task info
        status_badge = Styles.status_badge_style(@selected_task.status)
                            .render(" #{@selected_task.status.upcase} ")

        lines << "#{Styles.muted_style.render("Status:")} #{status_badge}"
        lines << "#{Styles.muted_style.render("Project:")} #{Styles.project_label_style.render(@selected_task.project)}"
        lines << "#{Styles.muted_style.render("Type:")} #{@selected_task.type}"
        lines << ""

        # Title
        lines << Lipgloss::Style.new.bold(true).render(@selected_task.title)
        lines << ""

        # Body
        if @selected_task.body && !@selected_task.body.empty?
          lines << @selected_task.body
          lines << ""
        end

        # Timestamps
        lines << Styles.divider(@width - 4)
        lines << "#{Styles.muted_style.render("Created:")} #{@selected_task.created_at}"
        if @selected_task.started_at
          lines << "#{Styles.muted_style.render("Started:")} #{@selected_task.started_at}"
        end
        if @selected_task.completed_at
          lines << "#{Styles.muted_style.render("Completed:")} #{@selected_task.completed_at}"
        end

        lines << ""
        lines << render_detail_help

        lines.join("\n")
      end

      def render_quit_confirm
        lines = []
        lines << ""
        lines << Styles.title_style.render(" Quit TaskYou? ")
        lines << ""
        lines << "Are you sure you want to quit?"
        lines << ""
        lines << "#{Styles.key_style.render("y")} #{Styles.desc_style.render("yes")}  " \
                 "#{Styles.key_style.render("n")} #{Styles.desc_style.render("no")}"
        lines.join("\n")
      end

      def render_delete_confirm
        return "" unless @pending_delete_task

        lines = []
        lines << ""
        lines << Styles.error_style.render(" Delete Task ##{@pending_delete_task.id}? ")
        lines << ""
        lines << "This will permanently delete:"
        lines << Lipgloss::Style.new.bold(true).render(@pending_delete_task.title)
        lines << ""
        lines << "#{Styles.key_style.render("y")} #{Styles.desc_style.render("yes, delete")}  " \
                 "#{Styles.key_style.render("n")} #{Styles.desc_style.render("no, cancel")}"
        lines.join("\n")
      end

      def render_close_confirm
        return "" unless @pending_close_task

        lines = []
        lines << ""
        lines << Styles.title_style.render(" Close Task ##{@pending_close_task.id}? ")
        lines << ""
        lines << "Mark as done:"
        lines << Lipgloss::Style.new.bold(true).render(@pending_close_task.title)
        lines << ""
        lines << "#{Styles.key_style.render("y")} #{Styles.desc_style.render("yes, close")}  " \
                 "#{Styles.key_style.render("n")} #{Styles.desc_style.render("no, cancel")}"
        lines.join("\n")
      end

      def render_detail_help
        bindings = [
          %w[esc back],
          %w[x execute],
          %w[e edit],
          %w[c close],
          %w[d delete]
        ]

        help_parts = bindings.map do |key, desc|
          "#{Styles.key_style.render(key)} #{Styles.desc_style.render(desc)}"
        end

        Styles.help_style.render(help_parts.join("  "))
      end

      def render_full_help
        lines = []
        lines << Styles.divider(@width - 4)
        lines << ""
        lines << "Navigation:"
        lines << "  #{Styles.key_style.render("←/h →/l")} columns  #{Styles.key_style.render("↑/k ↓/j")} tasks  " \
                 "#{Styles.key_style.render("1-4")} focus column  #{Styles.key_style.render("g/G")} top/bottom"
        lines << ""
        lines << "Actions:"
        lines << "  #{Styles.key_style.render("enter")} view  #{Styles.key_style.render("n")} new  " \
                 "#{Styles.key_style.render("x")} execute  #{Styles.key_style.render("c")} close  " \
                 "#{Styles.key_style.render("d")} delete"
        lines << ""
        lines << "  #{Styles.key_style.render("R")} refresh  #{Styles.key_style.render("?")} toggle help  " \
                 "#{Styles.key_style.render("q")} quit"
        lines.join("\n")
      end
    end
  end
end
