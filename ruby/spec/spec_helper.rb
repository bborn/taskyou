# frozen_string_literal: true

require "taskyou"
require "fileutils"
require "tempfile"

RSpec.configure do |config|
  config.expect_with :rspec do |expectations|
    expectations.include_chain_clauses_in_custom_matcher_descriptions = true
  end

  config.mock_with :rspec do |mocks|
    mocks.verify_partial_doubles = true
  end

  config.shared_context_metadata_behavior = :apply_to_host_groups
  config.filter_run_when_matching :focus
  config.example_status_persistence_file_path = "spec/examples.txt"
  config.disable_monkey_patching!
  config.warnings = true

  config.default_formatter = "doc" if config.files_to_run.one?

  config.order = :random
  Kernel.srand config.seed
end

# Helper module for database tests
module DatabaseHelpers
  def create_test_database
    @temp_db_file = Tempfile.new(["taskyou_test", ".db"])
    @temp_db_path = @temp_db_file.path
    @temp_db_file.close
    Taskyou::DB::Database.new(@temp_db_path)
  end

  def cleanup_test_database
    @db&.close
    FileUtils.rm_f(@temp_db_path) if @temp_db_path
    @temp_db_file&.unlink
  end
end

RSpec.configure do |config|
  config.include DatabaseHelpers

  config.around(:each, :db) do |example|
    @db = create_test_database
    example.run
    cleanup_test_database
  end
end
