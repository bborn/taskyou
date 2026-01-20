# frozen_string_literal: true

require "thor"

module Taskyou
  class CLI < Thor
    class_option :db, type: :string, desc: "Database path"
    class_option :local, aliases: "-l", type: :boolean, desc: "Run in local mode"

    desc "list", "List tasks"
    option :status, type: :string, desc: "Filter by status"
    option :project, type: :string, desc: "Filter by project"
    option :all, type: :boolean, desc: "Include done/archived tasks"
    option :json, type: :boolean, desc: "Output as JSON"
    def list
      db = open_database
      opts = {
        status: options[:status],
        project: options[:project],
        include_closed: options[:all]
      }

      tasks = DB::Task.list(db, opts)

      if options[:json]
        puts JSON.pretty_generate(tasks.map { |t| task_to_hash(t) })
      else
        print_task_table(tasks)
      end
    ensure
      db&.close
    end

    desc "add TITLE", "Add a new task"
    option :body, aliases: "-b", type: :string, desc: "Task body/description"
    option :project, aliases: "-p", type: :string, default: "personal", desc: "Project name"
    option :type, aliases: "-t", type: :string, default: "code", desc: "Task type"
    option :executor, aliases: "-e", type: :string, default: "claude", desc: "Executor (claude/codex)"
    option :queue, aliases: "-q", type: :boolean, desc: "Immediately queue the task"
    def add(title)
      db = open_database

      task = DB::Task.create(db,
        title: title,
        body: options[:body] || "",
        project: options[:project],
        type: options[:type],
        executor: options[:executor],
        status: options[:queue] ? DB::Task::STATUS_QUEUED : DB::Task::STATUS_BACKLOG)

      puts "Created task ##{task.id}: #{task.title}"
      puts "Status: #{task.status}"
      puts "Project: #{task.project}"
    rescue ProjectNotFoundError => e
      puts "Error: #{e.message}"
      exit 1
    ensure
      db&.close
    end

    desc "show ID", "Show task details"
    def show(id)
      db = open_database
      task = DB::Task.find(db, id.to_i)

      if task.nil?
        puts "Task ##{id} not found"
        exit 1
      end

      puts "Task ##{task.id}"
      puts "=" * 40
      puts "Title: #{task.title}"
      puts "Status: #{task.status}"
      puts "Project: #{task.project}"
      puts "Type: #{task.type}"
      puts "Executor: #{task.executor}"
      puts "Created: #{task.created_at}"
      puts "Updated: #{task.updated_at}"
      puts ""
      puts "Body:"
      puts task.body unless task.body.empty?

      # Show recent logs
      logs = DB::TaskLog.recent_for_task(db, task.id, 10)
      unless logs.empty?
        puts ""
        puts "Recent logs:"
        logs.each { |log| puts "  [#{log.line_type}] #{log.content}" }
      end
    ensure
      db&.close
    end

    desc "queue ID", "Queue a task for execution"
    def queue(id)
      db = open_database
      task = DB::Task.find(db, id.to_i)

      if task.nil?
        puts "Task ##{id} not found"
        exit 1
      end

      task.update_status(db, DB::Task::STATUS_QUEUED)
      puts "Task ##{task.id} queued for execution"
    ensure
      db&.close
    end

    desc "done ID", "Mark a task as done"
    def done(id)
      db = open_database
      task = DB::Task.find(db, id.to_i)

      if task.nil?
        puts "Task ##{id} not found"
        exit 1
      end

      task.update_status(db, DB::Task::STATUS_DONE)
      puts "Task ##{task.id} marked as done"
    ensure
      db&.close
    end

    desc "delete ID", "Delete a task"
    def delete(id)
      db = open_database
      task = DB::Task.find(db, id.to_i)

      if task.nil?
        puts "Task ##{id} not found"
        exit 1
      end

      task.delete(db)
      puts "Task ##{id} deleted"
    ensure
      db&.close
    end

    desc "projects", "List projects"
    def projects
      db = open_database
      projects = DB::Project.all(db)

      puts "Projects:"
      projects.each do |p|
        puts "  #{p.name} (#{p.path})"
      end
    ensure
      db&.close
    end

    desc "version", "Show version"
    def version
      puts "TaskYou Ruby v#{VERSION}"
    end

    private

    def open_database
      path = options[:db] || DB::Database.default_path
      DB::Database.new(path)
    end

    def task_to_hash(task)
      {
        id: task.id,
        title: task.title,
        body: task.body,
        status: task.status,
        type: task.type,
        project: task.project,
        executor: task.executor,
        created_at: task.created_at&.iso8601,
        updated_at: task.updated_at&.iso8601
      }
    end

    def print_task_table(tasks)
      if tasks.empty?
        puts "No tasks found"
        return
      end

      # Calculate column widths
      id_width = [tasks.map { |t| t.id.to_s.length }.max, 4].max
      status_width = [tasks.map { |t| t.status.length }.max, 8].max
      project_width = [tasks.map { |t| t.project.length }.max, 10].max

      # Print header
      puts format("%-#{id_width}s  %-#{status_width}s  %-#{project_width}s  %s",
        "ID", "Status", "Project", "Title")
      puts "-" * 80

      # Print tasks
      tasks.each do |task|
        puts format("%-#{id_width}d  %-#{status_width}s  %-#{project_width}s  %s",
          task.id, task.status, task.project, task.title.slice(0, 50))
      end
    end
  end
end
