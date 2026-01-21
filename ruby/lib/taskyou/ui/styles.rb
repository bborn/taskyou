# frozen_string_literal: true

require "lipgloss"

module Taskyou
  module UI
    module Styles
      # Color palette (One Dark theme)
      COLORS = {
        background: "#282c34",
        foreground: "#abb2bf",
        cursor: "#528bff",
        selection: "#3e4451",
        black: "#282c34",
        red: "#e06c75",
        green: "#98c379",
        yellow: "#e5c07b",
        blue: "#61afef",
        magenta: "#c678dd",
        cyan: "#56b6c2",
        white: "#abb2bf",
        bright_black: "#5c6370",
        bright_red: "#e06c75",
        bright_green: "#98c379",
        bright_yellow: "#e5c07b",
        bright_blue: "#61afef",
        bright_magenta: "#c678dd",
        bright_cyan: "#56b6c2",
        bright_white: "#ffffff"
      }.freeze

      # Status colors
      STATUS_COLORS = {
        "backlog" => COLORS[:bright_black],
        "queued" => COLORS[:yellow],
        "processing" => COLORS[:blue],
        "blocked" => COLORS[:red],
        "done" => COLORS[:green],
        "archived" => COLORS[:bright_black]
      }.freeze

      class << self
        def status_color(status)
          STATUS_COLORS[status] || COLORS[:foreground]
        end

        # Base style for the app
        def app_style
          Lipgloss::Style.new
            .padding(1, 2)
        end

        # Title bar style
        def title_style
          Lipgloss::Style.new
            .foreground(COLORS[:bright_white])
            .background(COLORS[:blue])
            .bold(true)
            .padding(0, 1)
        end

        # Column header style
        def column_header_style(selected: false)
          style = Lipgloss::Style.new
            .bold(true)
            .padding(0, 1)
            .margin_bottom(1)

          if selected
            style.foreground(COLORS[:bright_white])
                 .background(COLORS[:blue])
          else
            style.foreground(COLORS[:foreground])
          end
        end

        # Task card style
        def task_card_style(status:, selected: false)
          color = status_color(status)
          style = Lipgloss::Style.new
            .border(Lipgloss::ROUNDED_BORDER)
            .border_foreground(color)
            .padding(0, 1)
            .margin_bottom(1)
            .width(20)

          if selected
            style.border_foreground(COLORS[:bright_white])
                 .bold(true)
          else
            style
          end
        end

        # Status badge style
        def status_badge_style(status)
          color = status_color(status)
          Lipgloss::Style.new
            .foreground(COLORS[:black])
            .background(color)
            .padding(0, 1)
        end

        # Help style
        def help_style
          Lipgloss::Style.new
            .foreground(COLORS[:bright_black])
        end

        # Error style
        def error_style
          Lipgloss::Style.new
            .foreground(COLORS[:red])
            .bold(true)
        end

        # Success style
        def success_style
          Lipgloss::Style.new
            .foreground(COLORS[:green])
            .bold(true)
        end

        # Muted text style
        def muted_style
          Lipgloss::Style.new
            .foreground(COLORS[:bright_black])
        end

        # Project label style
        def project_label_style(color = nil)
          Lipgloss::Style.new
            .foreground(color || COLORS[:magenta])
            .bold(true)
        end

        # Key binding style (for help)
        def key_style
          Lipgloss::Style.new
            .foreground(COLORS[:cyan])
            .bold(true)
        end

        # Description style (for help)
        def desc_style
          Lipgloss::Style.new
            .foreground(COLORS[:foreground])
        end

        # Divider
        def divider(width)
          Lipgloss::Style.new
            .foreground(COLORS[:bright_black])
            .render("─" * width)
        end
      end
    end
  end
end
