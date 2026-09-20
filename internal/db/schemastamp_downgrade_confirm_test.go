package db

import (
	"fmt"
	"path/filepath"
	"testing"
)

// TestSchemaVersionStampDowngrade confirms the schema-version stamp that
// migrate() writes into the settings table is never downgraded by an older
// build re-running migrate() over a file a newer build last touched.
//
// `ty doctor` opens the database read-only and cannot migrate it; the on-disk
// schema-version stamp is the only way it can tell "a newer ty has migrated
// this database — upgrade required" (cmd/task/doctor.go, got > SchemaVersion).
// The stamp-write guard at the end of migrate() must therefore only *raise*
// the stamp, never lower it: an older binary seeing a higher stamp has to
// leave it alone.
//
// Without this guarantee, the very first `ty` subcommand any older build runs
// against the file (every command opens the DB with migrate=true via
// cmd/task/main.go) collapses the stamp down to that build's SchemaVersion and
// erases the signal doctor relies on.
func TestSchemaVersionStampDowngrade(t *testing.T) {
	t.Run("downgrade", func(t *testing.T) {
		// A higher stamp than this build's SchemaVersion, exactly what a newer
		// build's migrate() leaves behind (and the same value
		// TestDoctorSchemaVersionVerdicts's "newer" case uses).
		newerStamp := SchemaVersion + 1

		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")

		// (a) Fresh open: migrate() stamps the file at this build's SchemaVersion.
		database, err := Open(dbPath)
		if err != nil {
			t.Fatalf("initial Open: %v", err)
		}
		if got, ok := database.ReadSchemaVersion(); !ok || got != SchemaVersion {
			t.Fatalf("fresh open: ReadSchemaVersion = (%d, %v), want (%d, true)", got, ok, SchemaVersion)
		}
		if err := database.Close(); err != nil {
			t.Fatalf("close after initial open: %v", err)
		}

		// (b) Simulate a newer build having migrated this file by writing its
		// higher stamp. SetSetting is the exact path a real newer build's
		// migrate() uses to write the stamp; using it directly avoids needing
		// a second binary (none with SchemaVersion > 3 exists in this tree).
		database, err = Open(dbPath)
		if err != nil {
			t.Fatalf("reopen to set newer stamp: %v", err)
		}
		if err := database.SetSetting(SchemaVersionKey, fmt.Sprint(newerStamp)); err != nil {
			t.Fatalf("set newer stamp: %v", err)
		}
		if got, ok := database.ReadSchemaVersion(); !ok || got != newerStamp {
			t.Fatalf("set newer stamp: ReadSchemaVersion = (%d, %v), want (%d, true)", got, ok, newerStamp)
		}
		if err := database.Close(); err != nil {
			t.Fatalf("close after setting newer stamp: %v", err)
		}

		// (c) An *older* build now opens the file. migrate() runs again on Open,
		// and the buggy guard `current != SchemaVersion` would rewrite the
		// stamp down to this build's SchemaVersion. The fix (`<`) must leave
		// the higher stamp untouched.
		database, err = Open(dbPath)
		if err != nil {
			t.Fatalf("older build reopen: %v", err)
		}
		defer database.Close()

		got, ok := database.ReadSchemaVersion()
		if !ok || got != newerStamp {
			t.Errorf("schema stamp was DOWNGRADED by an older build's Open(): got %d, want %d (the newer build's stamp must be preserved so ty doctor can detect the mismatch)", got, newerStamp)
		}
	})

	t.Run("unstamped", func(t *testing.T) {
		// A fresh file must still be stamped at this build's SchemaVersion.
		// This is the fresh-file path the original guard comment cares about and
		// the one the fix must not silently regress: the `!ok` arm fires and
		// writes the stamp at SchemaVersion.
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")

		database, err := Open(dbPath)
		if err != nil {
			t.Fatalf("fresh Open: %v", err)
		}
		defer database.Close()

		got, ok := database.ReadSchemaVersion()
		if !ok || got != SchemaVersion {
			t.Errorf("fresh open did not stamp the file: ReadSchemaVersion = (%d, %v), want (%d, true)", got, ok, SchemaVersion)
		}
	})
}
