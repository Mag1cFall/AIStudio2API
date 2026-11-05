package sqlitedb

import (
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestOpenAppliesSchema 验证已有库打开后与建表语句一致：补上缺少的列，删除未定义的表与索引，空闲页交还文件系统
func TestOpenAppliesSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE items (id TEXT PRIMARY KEY, name TEXT NOT NULL);
INSERT INTO items VALUES ('a', 'first'), ('b', 'second');
CREATE INDEX items_name_legacy ON items(name);
CREATE TABLE legacy (data BLOB);
INSERT INTO legacy SELECT zeroblob(4096) FROM (WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 256) SELECT i FROM n);`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	schema := `CREATE TABLE IF NOT EXISTS items (id TEXT PRIMARY KEY, name TEXT NOT NULL, size INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS items_size ON items(size);`
	db, err := Open(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	objects, err := schemaObjects(db)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(objects))
	for name, kind := range objects {
		names = append(names, kind+":"+name)
	}
	slices.Sort(names)
	if strings.Join(names, ",") != "index:items_size,table:items" {
		t.Fatalf("表结构对象 = %v", names)
	}
	var sizes string
	if err := db.QueryRow(`SELECT group_concat(id || '=' || size, ',') FROM (SELECT id, size FROM items ORDER BY id)`).Scan(&sizes); err != nil || sizes != "a=0,b=0" {
		t.Fatalf("补列后的记录 = %q err=%v", sizes, err)
	}
	var mode, free int
	if err := db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil || mode != incrementalVacuum {
		t.Fatalf("auto_vacuum = %d err=%v", mode, err)
	}
	if err := db.QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil || free != 0 {
		t.Fatalf("freelist_count = %d err=%v", free, err)
	}
}
