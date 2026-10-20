package dbunit

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/viant/dbunit/fixture"
	"testing"
)

func BenchmarkPrepareBatch(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		for _, mixed := range []bool{false, true} {
			b.Run(fmt.Sprintf("rows=%d/mixed=%t", count, mixed), func(b *testing.B) {
				db, err := sql.Open("sqlite", ":memory:")
				if err != nil {
					b.Fatal(err)
				}
				defer db.Close()
				db.SetMaxOpenConns(1)
				if _, err := db.Exec("CREATE TABLE bench(id INTEGER PRIMARY KEY,name TEXT DEFAULT 'default',qty INTEGER DEFAULT 9)"); err != nil {
					b.Fatal(err)
				}
				e, err := New(db, WithDialect("sqlite"))
				if err != nil {
					b.Fatal(err)
				}
				rows := make([]map[string]any, count)
				for i := range rows {
					rows[i] = map[string]any{"id": i, "name": "fixture"}
					if !mixed || i%2 == 0 {
						rows[i]["qty"] = i
					}
				}
				req := PrepareRequest{Datasets: []Dataset{{Table: Table("bench"), Operation: Replace, Source: fixture.Maps(rows), Load: LoadOptions{Strategy: Batch, BatchSize: 500}}}}
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					if _, err := e.Prepare(context.Background(), req); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(count*b.N)/b.Elapsed().Seconds(), "rows/s")
			})
		}
	}
}
func TestBatchParameterBounds(t *testing.T) {
	for _, tt := range []struct{ requested, width, limit, want int }{{100000, 20, 994, 49}, {100000, 100, 65535, 655}, {0, 10, 999, 99}, {1, 2000, 999, 0}} {
		got, err := chooseBatchSize(tt.requested, tt.width, tt.limit)
		if tt.want == 0 {
			if err == nil {
				t.Fatal("expected width limit")
			}
			continue
		}
		if err != nil || got != tt.want || got*tt.width > tt.limit {
			t.Fatal(tt, got, err)
		}
	}
}
