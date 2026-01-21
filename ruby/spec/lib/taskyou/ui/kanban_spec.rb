# frozen_string_literal: true

require "spec_helper"

RSpec.describe Taskyou::UI::Kanban do
  let(:db) { Taskyou::DB::Database.new(":memory:") }
  let(:kanban) { described_class.new(db, width: 80, height: 24) }

  after do
    db.close
  end

  describe "#initialize" do
    it "sets up column selection" do
      expect(kanban.selected_column).to eq(0)
      expect(kanban.selected_row).to eq(0)
    end

    it "loads tasks by column" do
      expect(kanban.tasks_by_column).to be_a(Hash)
    end
  end

  describe "::COLUMNS" do
    it "has 4 columns" do
      expect(described_class::COLUMNS.length).to eq(4)
    end

    it "includes all expected columns" do
      statuses = described_class::COLUMNS.map { |c| c[:status] }
      expect(statuses).to include("backlog", "queued", "processing", "blocked")
    end
  end

  describe "#tasks_for_column" do
    it "returns an array" do
      expect(kanban.tasks_for_column(0)).to be_an(Array)
    end
  end

  describe "#selected_task" do
    context "with no tasks" do
      it "returns nil" do
        expect(kanban.selected_task).to be_nil
      end
    end

    context "with tasks" do
      before do
        Taskyou::DB::Task.create(db,
          title: "Test task",
          project: "personal",
          status: "backlog")
        kanban.refresh_tasks
      end

      it "returns the selected task" do
        expect(kanban.selected_task).to be_a(Taskyou::DB::Task)
        expect(kanban.selected_task.title).to eq("Test task")
      end
    end
  end

  describe "#refresh_tasks" do
    it "reloads tasks from database" do
      expect(kanban.tasks_by_column).to be_a(Hash)

      Taskyou::DB::Task.create(db,
        title: "New task",
        project: "personal",
        status: "backlog")

      kanban.refresh_tasks

      backlog_tasks = kanban.tasks_for_column(0)
      expect(backlog_tasks.length).to eq(1)
    end
  end

  # Note: View rendering tests are limited due to memory management issues
  # in the lipgloss-ruby gem when creating many styles rapidly.
  # View rendering works correctly in normal usage as verified by integration tests.
end
