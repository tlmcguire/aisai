package ast

import (
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkParseCorpus(b *testing.B) {
	var srcs []string
	_ = filepath.Walk("../../bench/train", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".py" {
			return nil
		}
		d, _ := os.ReadFile(path)
		srcs = append(srcs, string(d))
		return nil
	})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, s := range srcs {
			_, _ = ParseString("x.py", s)
		}
	}
	b.ReportMetric(float64(len(srcs)), "files/op")
}
