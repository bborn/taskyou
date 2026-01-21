# frozen_string_literal: true

module Taskyou
  module UI
    # Plain-text styles module (lipgloss-ruby has memory corruption bugs)
    # Uses ANSI escape codes directly for terminal styling
    module Styles
      # ANSI color codes
      ANSI = {
        reset: "\e[0m",
        bold: "\e[1m",
        dim: "\e[2m",
        # Foreground colors
        black: "\e[30m",
        red: "\e[31m",
        green: "\e[32m",
        yellow: "\e[33m",
        blue: "\e[34m",
        magenta: "\e[35m",
        cyan: "\e[36m",
        white: "\e[37m",
        bright_black: "\e[90m",
        bright_white: "\e[97m",
        # Background colors
        bg_blue: "\e[44m",
        bg_red: "\e[41m",
        bg_green: "\e[42m",
        bg_yellow: "\e[43m"
      }.freeze

      # Color palette (One Dark theme) - kept for reference
      COLORS = {
        background: "#282c34",
        foreground: "#abb2bf",
        bright_black: "#5c6370",
        red: "#e06c75",
        green: "#98c379",
        yellow: "#e5c07b",
        blue: "#61afef",
        magenta: "#c678dd",
        cyan: "#56b6c2",
        bright_white: "#ffffff"
      }.freeze

      # Status colors (ANSI codes)
      STATUS_ANSI = {
        "backlog" => :bright_black,
        "queued" => :yellow,
        "processing" => :blue,
        "blocked" => :red,
        "done" => :green,
        "archived" => :bright_black
      }.freeze

      # Status colors (hex for reference)
      STATUS_COLORS = {
        "backlog" => COLORS[:bright_black],
        "queued" => COLORS[:yellow],
        "processing" => COLORS[:blue],
        "blocked" => COLORS[:red],
        "done" => COLORS[:green],
        "archived" => COLORS[:bright_black]
      }.freeze

      # Simple style class that mimics lipgloss interface
      class SimpleStyle
        def initialize
          @codes = []
        end

        def foreground(color)
          @codes << color_to_ansi(color)
          self
        end

        def background(color)
          @codes << color_to_bg_ansi(color)
          self
        end

        def bold(val = true)
          @codes << ANSI[:bold] if val
          self
        end

        def padding(*_args)
          self
        end

        def margin_bottom(_n)
          self
        end

        def border(_type)
          @border = true
          self
        end

        def border_foreground(_color)
          self
        end

        def width(w)
          @width = w
          self
        end

        def render(text)
          result = @codes.join + text.to_s + ANSI[:reset]
          if @width && text.length < @width
            result = result + " " * (@width - text.length)
          end
          result
        end

        private

        def color_to_ansi(color)
          case color
          when /blue/i then ANSI[:blue]
          when /red/i then ANSI[:red]
          when /green/i then ANSI[:green]
          when /yellow/i then ANSI[:yellow]
          when /magenta/i then ANSI[:magenta]
          when /cyan/i then ANSI[:cyan]
          when /white/i then ANSI[:bright_white]
          when /black/i, /5c6370/i then ANSI[:bright_black]
          else ANSI[:white]
          end
        end

        def color_to_bg_ansi(color)
          case color
          when /blue/i then ANSI[:bg_blue]
          when /red/i then ANSI[:bg_red]
          when /green/i then ANSI[:bg_green]
          when /yellow/i then ANSI[:bg_yellow]
          else ""
          end
        end
      end

      class << self
        def status_color(status)
          STATUS_COLORS[status] || COLORS[:foreground]
        end

        def title_style
          SimpleStyle.new.foreground("white").background("blue").bold
        end

        def column_header_style(selected: false)
          style = SimpleStyle.new.bold
          if selected
            style.foreground("white").background("blue")
          else
            style
          end
        end

        def task_card_style(status:, selected: false)
          style = SimpleStyle.new
          if selected
            style.bold.foreground("white")
          else
            ansi_color = STATUS_ANSI[status] || :white
            style.foreground(ansi_color.to_s)
          end
        end

        def status_badge_style(status)
          SimpleStyle.new.bold.foreground(STATUS_ANSI[status]&.to_s || "white")
        end

        def help_style
          SimpleStyle.new.foreground("bright_black")
        end

        def error_style
          SimpleStyle.new.foreground("red").bold
        end

        def success_style
          SimpleStyle.new.foreground("green").bold
        end

        def muted_style
          SimpleStyle.new.foreground("bright_black")
        end

        def project_label_style(_color = nil)
          SimpleStyle.new.foreground("magenta").bold
        end

        def key_style
          SimpleStyle.new.foreground("cyan").bold
        end

        def desc_style
          SimpleStyle.new.foreground("white")
        end

        def divider(width)
          "#{ANSI[:bright_black]}#{"─" * width}#{ANSI[:reset]}"
        end
      end
    end
  end
end
