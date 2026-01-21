# frozen_string_literal: true

require "spec_helper"

RSpec.describe Taskyou::UI::Styles do
  describe "::COLORS" do
    it "defines a color palette" do
      expect(described_class::COLORS).to be_a(Hash)
      expect(described_class::COLORS[:background]).to eq("#282c34")
    end
  end

  describe "::STATUS_COLORS" do
    it "maps statuses to colors" do
      expect(described_class::STATUS_COLORS["backlog"]).not_to be_nil
      expect(described_class::STATUS_COLORS["queued"]).not_to be_nil
      expect(described_class::STATUS_COLORS["processing"]).not_to be_nil
      expect(described_class::STATUS_COLORS["blocked"]).not_to be_nil
      expect(described_class::STATUS_COLORS["done"]).not_to be_nil
    end
  end

  describe ".status_color" do
    it "returns color for valid status" do
      expect(described_class.status_color("backlog")).to eq(described_class::COLORS[:bright_black])
      expect(described_class.status_color("processing")).to eq(described_class::COLORS[:blue])
    end
  end

  describe ".title_style" do
    it "returns a SimpleStyle" do
      style = described_class.title_style
      expect(style).to be_a(described_class::SimpleStyle)
    end

    it "renders text with ANSI codes" do
      style = described_class.title_style
      result = style.render("Test")
      expect(result).to include("Test")
      expect(result).to include("\e[")  # Contains ANSI codes
    end
  end

  describe ".divider" do
    it "renders a divider line" do
      result = described_class.divider(10)
      expect(result).to be_a(String)
      expect(result).to include("─")
    end
  end

  describe Taskyou::UI::Styles::SimpleStyle do
    describe "#render" do
      it "applies ANSI codes and resets" do
        style = described_class.new.bold
        result = style.render("Hello")
        expect(result).to include("\e[1m")  # Bold
        expect(result).to include("\e[0m")  # Reset
        expect(result).to include("Hello")
      end
    end

    describe "#foreground" do
      it "is chainable" do
        style = described_class.new
        expect(style.foreground("blue")).to eq(style)
      end
    end

    describe "#bold" do
      it "is chainable" do
        style = described_class.new
        expect(style.bold).to eq(style)
      end
    end
  end
end
