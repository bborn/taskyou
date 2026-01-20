# frozen_string_literal: true

module Taskyou
  class Config
    # Setting keys
    SETTING_PROJECTS_DIR = "projects_dir"
    SETTING_THEME = "theme"
    SETTING_DETAIL_PANE_HEIGHT = "detail_pane_height"
    SETTING_SHELL_PANE_WIDTH = "shell_pane_width"
    SETTING_IDLE_SUSPEND_TIMEOUT = "idle_suspend_timeout"

    attr_reader :db, :projects_dir

    def initialize(database)
      @db = database
      load_config
    end

    def get_project_dir(project)
      return projects_dir if project.nil? || project.empty?

      # Look up project in database
      proj = DB::Project.find_by_name(db, project)
      return expand_path(proj.path) if proj

      # Default: projects_dir/project
      File.join(projects_dir, project)
    end

    def set_projects_dir(dir)
      set_setting(SETTING_PROJECTS_DIR, dir)
      @projects_dir = expand_path(dir)
    end

    def get_setting(key)
      db.get_first_value("SELECT value FROM settings WHERE key = ?", key)
    end

    def set_setting(key, value)
      db.execute(<<~SQL, key, value, value)
        INSERT INTO settings (key, value) VALUES (?, ?)
        ON CONFLICT(key) DO UPDATE SET value = ?
      SQL
    end

    def theme
      get_setting(SETTING_THEME) || "dark"
    end

    def theme=(value)
      set_setting(SETTING_THEME, value)
    end

    def detail_pane_height
      value = get_setting(SETTING_DETAIL_PANE_HEIGHT)
      value ? value.to_i : 50
    end

    def detail_pane_height=(value)
      set_setting(SETTING_DETAIL_PANE_HEIGHT, value.to_s)
    end

    def shell_pane_width
      value = get_setting(SETTING_SHELL_PANE_WIDTH)
      value ? value.to_i : 50
    end

    def shell_pane_width=(value)
      set_setting(SETTING_SHELL_PANE_WIDTH, value.to_s)
    end

    def idle_suspend_timeout
      value = get_setting(SETTING_IDLE_SUSPEND_TIMEOUT)
      value ? value.to_i : 6 * 60 * 60 # 6 hours default
    end

    def idle_suspend_timeout=(value)
      set_setting(SETTING_IDLE_SUSPEND_TIMEOUT, value.to_s)
    end

    private

    def load_config
      dir = get_setting(SETTING_PROJECTS_DIR)
      @projects_dir = if dir && !dir.empty?
                        expand_path(dir)
                      else
                        File.join(Dir.home, "Projects")
                      end
    end

    def expand_path(path)
      return path unless path.start_with?("~")

      File.join(Dir.home, path[1..])
    end
  end
end
