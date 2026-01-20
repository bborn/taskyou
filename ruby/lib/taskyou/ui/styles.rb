# frozen_string_literal: true

# NOTE: This is a stub implementation. Full implementation requires bubbletea-ruby gem.
# The actual styling will use lipgloss-ruby when available.

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

      def self.status_color(status)
        STATUS_COLORS[status] || COLORS[:foreground]
      end

      # ANSI escape codes for terminal colors
      def self.colorize(text, color)
        # Simple ANSI coloring - would be replaced by lipgloss in full implementation
        "\e[38;5;#{hex_to_ansi(color)}m#{text}\e[0m"
      end

      def self.hex_to_ansi(hex)
        # Simplified conversion - full implementation would use proper color matching
        case hex
        when COLORS[:red] then 196
        when COLORS[:green] then 114
        when COLORS[:yellow] then 220
        when COLORS[:blue] then 75
        when COLORS[:magenta] then 176
        when COLORS[:cyan] then 80
        else 252
        end
      end
    end
  end
end
