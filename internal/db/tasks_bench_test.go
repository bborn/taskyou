package db

import (
	"fmt"
	"path/filepath"
	"testing"
)

// Exercise completed-history growth without reducing the active board's workload.
func BenchmarkTaskBoardHistory(b *testing.B) {
	for _, count := range []int{2131, 50000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			database, err := Open(filepath.Join(b.TempDir(), "tasks.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer database.Close()
			_, err = database.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO tasks(title,body,status,created_at,updated_at) SELECT 'Task '||x, printf('%01024d',x),CASE WHEN x<124 THEN 'blocked' ELSE 'done' END,datetime('now'),datetime('now') FROM n`, count)
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := database.ListTasks(ListTasksOptions{}); err != nil {
					b.Fatal(err)
				}
				if _, err := database.ListTasks(ListTasksOptions{Status: StatusDone, Limit: 20, OrderByRecency: true}); err != nil {
					b.Fatal(err)
				}
				if _, err := database.CountTasksByStatus(StatusDone); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
