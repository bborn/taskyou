# frozen_string_literal: true

require "lipgloss"

module Taskyou
  module UI
    # Theme provides color schemes for the UI
    class Theme
      DARK = "dark"
      LIGHT = "light"

      # One Dark theme colors
      DARK_COLORS = {
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
        bright_white: "#ffffff"
      }.freeze

      # One Light theme colors
      LIGHT_COLORS = {
        background: "#fafafa",
        foreground: "#383a42",
        cursor: "#526fff",
        selection: "#e5e5e6",
        black: "#fafafa",
        red: "#e45649",
        green: "#50a14f",
        yellow: "#c18401",
        blue: "#4078f2",
        magenta: "#a626a4",
        cyan: "#0184bc",
        white: "#383a42",
        bright_black: "#a0a1a7",
        bright_white: "#090a0b"
      }.freeze

      attr_reader :name

      def initialize(name = DARK)
        @name = name
        @colors = dark? ? DARK_COLORS : LIGHT_COLORS
      end

      def dark?
        @name == DARK
      end

      def light?
        @name == LIGHT
      end

      def toggle
        @name = dark? ? LIGHT : DARK
        @colors = dark? ? DARK_COLORS : LIGHT_COLORS
        self
      end

      def [](key)
        @colors[key] || @colors[:foreground]
      end

      def background
        @colors[:background]
      end

      def foreground
        @colors[:foreground]
      end

      def primary
        @colors[:blue]
      end

      def secondary
        @colors[:magenta]
      end

      def success
        @colors[:green]
      end

      def warning
        @colors[:yellow]
      end

      def error
        @colors[:red]
      end

      def muted
        @colors[:bright_black]
      end

      def accent
        @colors[:cyan]
      end

      # Create a base style with theme colors
      def base_style
        Lipgloss::Style.new
          .foreground(foreground)
      end

      # Create a highlighted/selected style
      def selected_style
        Lipgloss::Style.new
          .foreground(@colors[:bright_white])
          .background(primary)
          .bold(true)
      end

      # Create an error style
      def error_style
        Lipgloss::Style.new
          .foreground(error)
          .bold(true)
      end

      # Create a success style
      def success_style
        Lipgloss::Style.new
          .foreground(success)
          .bold(true)
      end

      # Create a muted style
      def muted_style
        Lipgloss::Style.new
          .foreground(muted)
      end
    end
  end
end
