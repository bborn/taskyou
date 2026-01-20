# frozen_string_literal: true

require "spec_helper"

RSpec.describe Taskyou::DB::Database, :db do
  describe "#initialize" do
    it "creates a new database file" do
      expect(File.exist?(@temp_db_path)).to be true
    end

    it "creates required tables" do
      tables = @db.execute("SELECT name FROM sqlite_master WHERE type='table'").map { |r| r["name"] }

      expect(tables).to include("tasks")
      expect(tables).to include("projects")
      expect(tables).to include("task_logs")
      expect(tables).to include("settings")
      expect(tables).to include("project_memories")
      expect(tables).to include("task_attachments")
      expect(tables).to include("task_types")
    end

    it "creates personal project" do
      count = @db.get_first_value("SELECT COUNT(*) FROM projects WHERE name = 'personal'")

      expect(count).to eq(1)
    end

    it "creates default task types" do
      count = @db.get_first_value("SELECT COUNT(*) FROM task_types")

      expect(count).to eq(3)
    end

    it "enables WAL mode" do
      mode = @db.get_first_value("PRAGMA journal_mode")

      expect(mode).to eq("wal")
    end
  end

  describe ".default_path" do
    it "returns path in user home directory" do
      path = described_class.default_path

      expect(path).to include(".local/share/task/tasks.db")
    end

    it "respects WORKTREE_DB_PATH environment variable" do
      original = ENV.fetch("WORKTREE_DB_PATH", nil)
      ENV["WORKTREE_DB_PATH"] = "/custom/path.db"

      expect(described_class.default_path).to eq("/custom/path.db")
    ensure
      if original
        ENV["WORKTREE_DB_PATH"] = original
      else
        ENV.delete("WORKTREE_DB_PATH")
      end
    end
  end
end
