package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// AttachmentDirectory is durable storage owned by this database. Back up both.
// Worktrees hold execution copies and may be deleted independently.
func (db *DB) AttachmentDirectory() string { return db.path + ".attachments" }

func (db *DB) storeAttachmentFile(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	dir := db.AttachmentDirectory()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, hash)); err != nil {
		return "", err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return "", err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return "", err
	}
	return hash, nil
}

func (db *DB) readAttachmentFile(hash string) ([]byte, error) {
	decoded, err := hex.DecodeString(hash)
	if err != nil || len(decoded) != sha256.Size {
		return nil, fmt.Errorf("invalid attachment checksum")
	}
	data, err := os.ReadFile(filepath.Join(db.AttachmentDirectory(), hash))
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != hash {
		return nil, fmt.Errorf("attachment checksum mismatch")
	}
	return data, nil
}

// MigrateAttachmentFiles moves legacy blobs only after their files are durable.
// It is restartable. SQLite reuses the freed pages; shrinking the database is a
// separate maintenance operation. Legacy blobs remain readable until migrated.
func (db *DB) MigrateAttachmentFiles() error {
	if db.path == ":memory:" {
		return nil
	}
	for {
		var id int64
		var data []byte
		err := db.QueryRow(`SELECT id, data FROM task_attachments WHERE content_hash = '' LIMIT 1`).Scan(&id, &data)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		hash, err := db.storeAttachmentFile(data)
		if err != nil {
			return err
		}
		if _, err = db.Exec(`UPDATE task_attachments SET content_hash = ?, data = X'' WHERE id = ? AND content_hash = ''`, hash, id); err != nil {
			return err
		}
	}
}
