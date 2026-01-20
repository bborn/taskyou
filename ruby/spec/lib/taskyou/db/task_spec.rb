# frozen_string_literal: true

require "spec_helper"

RSpec.describe Taskyou::DB::Task, :db do
  describe ".create" do
    it "creates a task with default project" do
      task = described_class.create(@db, title: "Test task")

      expect(task.id).not_to be_nil
      expect(task.title).to eq("Test task")
      expect(task.project).to eq("personal")
      expect(task.status).to eq(described_class::STATUS_BACKLOG)
    end

    it "creates a task with specified project" do
      task = described_class.create(@db, title: "Test", project: "personal")

      expect(task.project).to eq("personal")
    end

    it "defaults executor to claude" do
      task = described_class.create(@db, title: "Test")

      expect(task.executor).to eq(described_class::EXECUTOR_CLAUDE)
    end

    it "raises error for non-existent project" do
      expect do
        described_class.create(@db, title: "Test", project: "nonexistent")
      end.to raise_error(Taskyou::ProjectNotFoundError)
    end
  end

  describe ".find" do
    it "finds a task by id" do
      created = described_class.create(@db, title: "Find me", body: "Some body")
      found = described_class.find(@db, created.id)

      expect(found).not_to be_nil
      expect(found.title).to eq("Find me")
      expect(found.body).to eq("Some body")
    end

    it "returns nil for non-existent task" do
      found = described_class.find(@db, 99_999)

      expect(found).to be_nil
    end
  end

  describe ".list" do
    before do
      described_class.create(@db, title: "Task 1", status: described_class::STATUS_BACKLOG)
      described_class.create(@db, title: "Task 2", status: described_class::STATUS_QUEUED)
      described_class.create(@db, title: "Task 3", status: described_class::STATUS_DONE)
    end

    it "lists all non-closed tasks by default" do
      tasks = described_class.list(@db)

      expect(tasks.length).to eq(2)
      expect(tasks.map(&:title)).to contain_exactly("Task 1", "Task 2")
    end

    it "includes closed tasks when requested" do
      tasks = described_class.list(@db, include_closed: true)

      expect(tasks.length).to eq(3)
    end

    it "filters by status" do
      tasks = described_class.list(@db, status: described_class::STATUS_BACKLOG)

      expect(tasks.length).to eq(1)
      expect(tasks.first.title).to eq("Task 1")
    end

    it "respects limit" do
      tasks = described_class.list(@db, limit: 1, include_closed: true)

      expect(tasks.length).to eq(1)
    end
  end

  describe ".search" do
    before do
      described_class.create(@db, title: "Fix authentication bug")
      described_class.create(@db, title: "Add new feature")
      described_class.create(@db, title: "Update docs")
    end

    it "searches by title" do
      results = described_class.search(@db, "auth")

      expect(results.length).to eq(1)
      expect(results.first.title).to eq("Fix authentication bug")
    end

    it "is case insensitive" do
      results = described_class.search(@db, "AUTH")

      expect(results.length).to eq(1)
    end

    it "returns empty array when no matches" do
      results = described_class.search(@db, "nonexistent")

      expect(results).to be_empty
    end
  end

  describe "#update_status" do
    it "updates status to queued" do
      task = described_class.create(@db, title: "Test")
      task.update_status(@db, described_class::STATUS_QUEUED)

      reloaded = described_class.find(@db, task.id)
      expect(reloaded.status).to eq(described_class::STATUS_QUEUED)
    end

    it "sets started_at when moving to processing" do
      task = described_class.create(@db, title: "Test")
      task.update_status(@db, described_class::STATUS_PROCESSING)

      reloaded = described_class.find(@db, task.id)
      expect(reloaded.started_at).not_to be_nil
    end

    it "sets completed_at when moving to done" do
      task = described_class.create(@db, title: "Test")
      task.update_status(@db, described_class::STATUS_DONE)

      reloaded = described_class.find(@db, task.id)
      expect(reloaded.completed_at).not_to be_nil
    end
  end

  describe "#in_progress?" do
    it "returns true for queued tasks" do
      task = described_class.new(status: described_class::STATUS_QUEUED)

      expect(task.in_progress?).to be true
    end

    it "returns true for processing tasks" do
      task = described_class.new(status: described_class::STATUS_PROCESSING)

      expect(task.in_progress?).to be true
    end

    it "returns false for backlog tasks" do
      task = described_class.new(status: described_class::STATUS_BACKLOG)

      expect(task.in_progress?).to be false
    end

    it "returns false for done tasks" do
      task = described_class.new(status: described_class::STATUS_DONE)

      expect(task.in_progress?).to be false
    end
  end

  describe ".allocate_port" do
    it "allocates a port in the valid range" do
      task = described_class.create(@db, title: "Test")
      port = described_class.allocate_port(@db, task.id)

      expect(port).to be >= described_class::PORT_RANGE_START
      expect(port).to be <= described_class::PORT_RANGE_END
    end

    it "allocates different ports for different tasks" do
      task1 = described_class.create(@db, title: "Test 1")
      task2 = described_class.create(@db, title: "Test 2")

      port1 = described_class.allocate_port(@db, task1.id)
      port2 = described_class.allocate_port(@db, task2.id)

      expect(port1).not_to eq(port2)
    end
  end

  describe "#delete" do
    it "deletes the task" do
      task = described_class.create(@db, title: "Test")
      task.delete(@db)

      expect(described_class.find(@db, task.id)).to be_nil
    end
  end
end
