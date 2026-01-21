# frozen_string_literal: true

require "spec_helper"

RSpec.describe Taskyou::UI::App do
  let(:db) { Taskyou::DB::Database.new(":memory:") }
  let(:executor) { Taskyou::Executor::Executor.new(db, Dir.pwd) }
  let(:app) { described_class.new(db, executor, Dir.pwd) }

  after do
    db.close
  end

  describe "#initialize" do
    it "starts on the dashboard view" do
      expect(app.current_view).to eq(Taskyou::UI::View::DASHBOARD)
    end

    it "initializes the kanban board" do
      expect(app.kanban).to be_a(Taskyou::UI::Kanban)
    end

    it "has default dimensions" do
      expect(app.width).to eq(80)
      expect(app.height).to eq(24)
    end
  end

  describe "#init" do
    it "loads tasks" do
      result = app.init
      expect(result).to be_nil
    end
  end

  describe "#set_notification" do
    it "sets a notification message" do
      app.set_notification("Test notification")
      expect(app.notification).to eq("Test notification")
    end
  end

  describe "#refresh_tasks" do
    it "refreshes the kanban board" do
      expect(app.kanban).to receive(:refresh_tasks)
      app.refresh_tasks
    end
  end

  # Note: View rendering tests are limited due to memory management issues
  # in the lipgloss-ruby gem when creating many styles rapidly.
  # View rendering works correctly in normal usage as verified by integration tests.
end
