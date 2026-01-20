# frozen_string_literal: true

# NOTE: This is a stub implementation. Full implementation requires bubbletea-ruby gem.

module Taskyou
  module UI
    class Theme
      DARK = "dark"
      LIGHT = "light"

      attr_reader :name

      def initialize(name = DARK)
        @name = name
      end

      def dark?
        @name == DARK
      end

      def light?
        @name == LIGHT
      end

      def background
        dark? ? "#282c34" : "#fafafa"
      end

      def foreground
        dark? ? "#abb2bf" : "#383a42"
      end

      def primary
        dark? ? "#61afef" : "#4078f2"
      end

      def secondary
        dark? ? "#c678dd" : "#a626a4"
      end

      def success
        dark? ? "#98c379" : "#50a14f"
      end

      def warning
        dark? ? "#e5c07b" : "#c18401"
      end

      def error
        dark? ? "#e06c75" : "#e45649"
      end

      def muted
        dark? ? "#5c6370" : "#a0a1a7"
      end
    end
  end
end
