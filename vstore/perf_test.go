package vstore

import (
	"crypto/sha256"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	maxStorage = 30 << 20 // 30 MiB
	maxReadLat = 200 * time.Millisecond
)

// genText 生成约 1 MiB、每行约 80 字节的文本。
func genText(rng *rand.Rand) []byte {
	var b []byte
	for len(b) < 1<<20 {
		b = append(b, []byte(fmt.Sprintf("line-%08d-%s\n", rng.Int63(), randChars(rng, 60)))...)
	}
	return b
}

func randChars(rng *rand.Rand, n int) string {
	const alpha = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, n)
	for i := range out {
		out[i] = alpha[rng.Intn(len(alpha))]
	}
	return string(out)
}

// mutateLines 对文本做 1-3 次改写/插入/删除行操作。
func mutateLines(rng *rand.Rand, content []byte) []byte {
	lines := splitLines(content)
	ops := 1 + rng.Intn(3)
	for i := 0; i < ops; i++ {
		switch rng.Intn(3) {
		case 0: // 改写
			j := rng.Intn(len(lines))
			lines[j] = []byte(randChars(rng, 40+rng.Intn(40)) + "\n")
		case 1: // 插入
			j := rng.Intn(len(lines) + 1)
			lines = append(lines, nil)
			copy(lines[j+1:], lines[j:])
			lines[j] = []byte("inserted-" + randChars(rng, 50) + "\n")
		case 2: // 删除
			if len(lines) > 1 {
				j := rng.Intn(len(lines))
				lines = append(lines[:j], lines[j+1:]...)
			}
		}
	}
	var out []byte
	for _, l := range lines {
		out = append(out, l...)
	}
	return out
}

func splitLines(b []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			lines = append(lines, b[start:i+1])
			start = i + 1
		}
	}
	if start < len(b) {
		lines = append(lines, b[start:])
	}
	return lines
}

// dirSize 统计数据目录里所有文件的字节数之和。
func dirSize(t *testing.T, dir string) int64 {
	t.Helper()
	var total int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return total
}

// recorder 记录每个版本的内容哈希，并抽查保留完整内容。
type recorder struct {
	hashes map[string][32]byte
	full   map[string][]byte
}

func newRecorder() *recorder {
	return &recorder{hashes: make(map[string][32]byte), full: make(map[string][]byte)}
}

func (r *recorder) record(id string, content []byte, keepFull bool) {
	r.hashes[id] = sha256.Sum256(content)
	if keepFull {
		r.full[id] = append([]byte(nil), content...)
	}
}

// verifyAll 读回全部版本，逐版本校验字节一致性与读取耗时。
func verifyAll(t *testing.T, s *Store, file string, r *recorder) time.Duration {
	t.Helper()
	var worst time.Duration
	for id, want := range r.hashes {
		start := time.Now()
		got, err := s.Read(file, id)
		lat := time.Since(start)
		if err != nil {
			t.Fatalf("Read %s: %v", id, err)
		}
		if sha256.Sum256(got) != want {
			t.Fatalf("版本 %s 内容哈希不一致", id)
		}
		if full, ok := r.full[id]; ok {
			if string(got) != string(full) {
				t.Fatalf("版本 %s 内容逐字节比对失败", id)
			}
		}
		if lat > worst {
			worst = lat
		}
		if lat > maxReadLat {
			t.Fatalf("版本 %s 读取耗时 %v 超过 %v", id, lat, maxReadLat)
		}
	}
	return worst
}

// 场景一：长历史。1 MiB 文本连续提交 1000 版，每版改 1-3 行。
func TestLongHistory(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(42))
	rec := newRecorder()

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	content := genText(rng)
	parent, err := s.Commit("file", "", content)
	if err != nil {
		t.Fatal(err)
	}
	rec.record(parent, content, true)

	for i := 1; i < 1000; i++ {
		content = mutateLines(rng, content)
		parent, err = s.Commit("file", parent, content)
		if err != nil {
			t.Fatal(err)
		}
		rec.record(parent, content, i%100 == 0)
	}

	size := dirSize(t, dir)
	t.Logf("长历史存储量: %.2f MiB / 上限 30 MiB", float64(size)/float64(1<<20))
	if size > maxStorage {
		t.Fatalf("存储量 %d 字节超过上限 %d", size, maxStorage)
	}

	worst := verifyAll(t, s, "file", rec)
	t.Logf("关闭前全部版本读取校验通过，最慢 %.2f ms", float64(worst.Microseconds())/1000)

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	worst = verifyAll(t, s, "file", rec)
	t.Logf("重开后全部版本读取校验通过，最慢 %.2f ms", float64(worst.Microseconds())/1000)
}

// 场景二：分支和恢复。同一版本拉出两个分支，交替各提交 500 次，
// 期间每个分支各恢复到分叉前的版本几次。
func TestBranchesAndRestore(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(43))
	rec := newRecorder()

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := genText(rng)
	baseID, err := s.Commit("file", "", base)
	if err != nil {
		t.Fatal(err)
	}
	rec.record(baseID, base, true)

	contentA := append([]byte(nil), base...)
	contentB := append([]byte(nil), base...)
	tipA, tipB := baseID, baseID

	for i := 0; i < 500; i++ {
		// 两个分支交替各提交一次
		contentA = mutateLines(rng, contentA)
		tipA, err = s.Commit("file", tipA, contentA)
		if err != nil {
			t.Fatal(err)
		}
		rec.record(tipA, contentA, i%100 == 0)

		contentB = mutateLines(rng, contentB)
		tipB, err = s.Commit("file", tipB, contentB)
		if err != nil {
			t.Fatal(err)
		}
		rec.record(tipB, contentB, i%100 == 0)

		// 每个分支各恢复到分叉前的版本几次
		if i == 120 || i == 300 || i == 450 {
			tipA, err = s.Restore("file", tipA, baseID)
			if err != nil {
				t.Fatal(err)
			}
			contentA = append([]byte(nil), base...)
			rec.record(tipA, contentA, true)
		}
		if i == 200 || i == 380 {
			tipB, err = s.Restore("file", tipB, baseID)
			if err != nil {
				t.Fatal(err)
			}
			contentB = append([]byte(nil), base...)
			rec.record(tipB, contentB, true)
		}
	}

	size := dirSize(t, dir)
	t.Logf("分支历史存储量: %.2f MiB / 上限 30 MiB", float64(size)/float64(1<<20))
	if size > maxStorage {
		t.Fatalf("存储量 %d 字节超过上限 %d", size, maxStorage)
	}

	worst := verifyAll(t, s, "file", rec)
	t.Logf("关闭前全部版本读取校验通过，最慢 %.2f ms", float64(worst.Microseconds())/1000)

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	worst = verifyAll(t, s, "file", rec)
	t.Logf("重开后全部版本读取校验通过，最慢 %.2f ms", float64(worst.Microseconds())/1000)
}
