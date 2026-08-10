package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store 持久化存储（SQLite）
type Store struct {
	db *sql.DB
}

// Open 打开（必要时创建）数据库文件
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "dedup.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite 单写
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS folders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			path TEXT UNIQUE NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			path TEXT UNIQUE NOT NULL,
			size INTEGER NOT NULL,
			hash TEXT DEFAULT '',
			mod_time INTEGER NOT NULL,
			folder_id INTEGER NOT NULL,
			deleted INTEGER DEFAULT 0,
			deleted_at INTEGER DEFAULT 0,
			batch_id TEXT DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_files_hash ON files(hash)`,
		`CREATE INDEX IF NOT EXISTS idx_files_size ON files(size)`,
		`CREATE INDEX IF NOT EXISTS idx_files_deleted ON files(deleted)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			status TEXT NOT NULL,
			progress INTEGER DEFAULT 0,
			message TEXT DEFAULT '',
			started_at INTEGER NOT NULL,
			finished_at INTEGER DEFAULT 0
		)`,
	}
	for _, st := range stmts {
		if _, err := s.db.Exec(st); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	// 为旧库补充新列（幂等），须在创建 batch 索引之前完成
	cols, err := s.tableCols("files")
	if err != nil {
		return err
	}
	for _, col := range []string{"deleted_at INTEGER DEFAULT 0", "batch_id TEXT DEFAULT ''"} {
		name := strings.Fields(col)[0]
		if _, ok := cols[name]; !ok {
			if _, err := s.db.Exec("ALTER TABLE files ADD COLUMN " + col); err != nil {
				return fmt.Errorf("migrate add col %s: %w", name, err)
			}
		}
	}
	// 补充列后再建依赖 batch_id 的索引
	for _, st := range []string{
		`CREATE INDEX IF NOT EXISTS idx_files_batch ON files(batch_id)`,
	} {
		if _, err := s.db.Exec(st); err != nil {
			return fmt.Errorf("migrate index: %w", err)
		}
	}
	return nil
}

func (s *Store) tableCols(table string) (map[string]bool, error) {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func (s *Store) Close() error { return s.db.Close() }

// ---------- folders ----------

func (s *Store) AddFolder(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO folders(path, created_at) VALUES(?, ?)`, abs, time.Now().Unix())
	return err
}

func (s *Store) DeleteFolder(id int64) error {
	_, err := s.db.Exec(`DELETE FROM folders WHERE id = ?`, id)
	return err
}

func (s *Store) ListFolders() ([]Folder, error) {
	rows, err := s.db.Query(`SELECT id, path, created_at FROM folders ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Folder, 0)
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.Path, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ---------- files ----------

// SaveFiles 批量写入文件记录（INSERT OR REPLACE 以便重复扫描时更新哈希）
func (s *Store) SaveFiles(files []FileRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO files(path, size, hash, mod_time, folder_id, deleted)
		VALUES(?, ?, ?, ?, ?, 0)
		ON CONFLICT(path) DO UPDATE SET size=excluded.size, hash=excluded.hash, mod_time=excluded.mod_time,
			folder_id=excluded.folder_id, deleted=0`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, f := range files {
		if _, err := stmt.Exec(f.Path, f.Size, f.Hash, f.ModTime, f.FolderID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClearDeleted 删除扫描时清空已标记删除的记录（被移入回收站的文件不再视为在线文件）
func (s *Store) ClearDeleted() error {
	_, err := s.db.Exec(`DELETE FROM files WHERE deleted = 1 AND hash = ''`)
	return err
}

// MarkDeleted 将文件标记为已删除并记录删除时间与批次
func (s *Store) MarkDeleted(path, batch string, ts int64) error {
	_, err := s.db.Exec(`UPDATE files SET deleted = 1, deleted_at = ?, batch_id = ? WHERE path = ?`, ts, batch, path)
	return err
}

// TrashBatches 按批次汇总已删除文件
func (s *Store) TrashBatches() ([]TrashBatch, error) {
	rows, err := s.db.Query(`SELECT batch_id, MAX(deleted_at) AS dt, COUNT(*) AS c, SUM(size) AS total
		FROM files WHERE deleted = 1 GROUP BY batch_id ORDER BY dt DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TrashBatch, 0)
	for rows.Next() {
		var b TrashBatch
		if err := rows.Scan(&b.BatchID, &b.DeletedAt, &b.FileCount, &b.TotalSize); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// TrashFiles 返回某批次（可选某文件夹前缀下）的已删除文件记录
func (s *Store) TrashFiles(batch, folder string) ([]FileRecord, error) {
	q := `SELECT id, path, size, hash, mod_time, folder_id, deleted, deleted_at, batch_id
		FROM files WHERE deleted = 1 AND batch_id = ?`
	args := []any{batch}
	if folder != "" {
		q += ` AND (path = ? OR path LIKE ?)`
		args = append(args, folder, folder+"/%")
	}
	q += ` ORDER BY path`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]FileRecord, 0)
	for rows.Next() {
		var f FileRecord
		if err := rows.Scan(&f.ID, &f.Path, &f.Size, &f.Hash, &f.ModTime, &f.FolderID, &f.Deleted, &f.DeletedAt, &f.BatchID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// RestoreFile 恢复文件：清除删除标记
func (s *Store) RestoreFile(path string) error {
	_, err := s.db.Exec(`UPDATE files SET deleted = 0, deleted_at = 0, batch_id = '' WHERE path = ?`, path)
	return err
}

// ListDupGroups 返回所有重复组（含文件列表）
func (s *Store) ListDupGroups() ([]DupGroup, error) {
	rows, err := s.db.Query(`SELECT hash, size, COUNT(*) AS c
		FROM files WHERE deleted = 0 AND hash != ''
		GROUP BY hash, size HAVING c > 1 ORDER BY size DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]DupGroup, 0)
	for rows.Next() {
		var g DupGroup
		if err := rows.Scan(&g.Hash, &g.Size, &g.FileCount); err != nil {
			return nil, err
		}
		g.TotalSize = g.Size * int64(g.FileCount)
		g.Reclaimable = g.Size * int64(g.FileCount-1)
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range groups {
		fs, err := s.FilesByHash(groups[i].Hash)
		if err != nil {
			return nil, err
		}
		groups[i].Files = fs
	}
	return groups, nil
}

func (s *Store) FilesByHash(hash string) ([]FileRecord, error) {
	rows, err := s.db.Query(`SELECT id, path, size, hash, mod_time, folder_id, deleted, deleted_at, batch_id
		FROM files WHERE hash = ? AND deleted = 0 ORDER BY mod_time DESC`, hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]FileRecord, 0)
	for rows.Next() {
		var f FileRecord
		if err := rows.Scan(&f.ID, &f.Path, &f.Size, &f.Hash, &f.ModTime, &f.FolderID, &f.Deleted, &f.DeletedAt, &f.BatchID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ---------- tasks ----------

func (s *Store) CreateTask() (int64, error) {
	res, err := s.db.Exec(`INSERT INTO tasks(status, progress, started_at) VALUES('running', 0, ?)`, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateTask(id int64, status string, progress int, message string) error {
	_, err := s.db.Exec(`UPDATE tasks SET status = ?, progress = ?, message = ? WHERE id = ?`,
		status, progress, message, id)
	return err
}

func (s *Store) FinishTask(id int64, status string) error {
	_, err := s.db.Exec(`UPDATE tasks SET status = ?, progress = 100, finished_at = ? WHERE id = ?`,
		status, time.Now().Unix(), id)
	return err
}

func (s *Store) GetTask(id int64) (*Task, error) {
	var t Task
	err := s.db.QueryRow(`SELECT id, status, progress, message, started_at, finished_at FROM tasks WHERE id = ?`, id).
		Scan(&t.ID, &t.Status, &t.Progress, &t.Message, &t.StartedAt, &t.FinishedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) LatestTask() (*Task, error) {
	row := s.db.QueryRow(`SELECT id, status, progress, message, started_at, finished_at
		FROM tasks ORDER BY id DESC LIMIT 1`)
	var t Task
	err := row.Scan(&t.ID, &t.Status, &t.Progress, &t.Message, &t.StartedAt, &t.FinishedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}