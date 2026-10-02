package vstore

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	perfSizeLimit = 30 << 20 // 30 MiB
	perfReadLimit = 200 * time.Millisecond
)

// genLines 生成约 1 MiB 的确定性文本行。
func genLines(r *rand.Rand) [][]byte {
	var lines [][]byte
	total := 0
	for total < 1<<20 {
		line := fmt.Sprintf("line %06d: %016x %016x %016x\n", len(lines), r.Uint64(), r.Uint64(), r.Uint64())
		lines = append(lines, []byte(line))
		total += len(line)
	}
	return lines
}

func joinLines(lines [][]byte) []byte {
	return bytes.Join(lines, nil)
}

// mutate 随机改写、插入或删除 1~3 行。
func mutate(r *rand.Rand, lines [][]byte, seq int) [][]byte {
	ops := 1 + r.Intn(3)
	for i := 0; i < ops; i++ {
		switch r.Intn(3) {
		case 0: // 改写一行
			idx := r.Intn(len(lines))
			lines[idx] = []byte(fmt.Sprintf("line %06d: modified %016x %016x\n", idx, r.Uint64(), r.Uint64()))
		case 1: // 插入一行
			idx := r.Intn(len(lines) + 1)
			line := []byte(fmt.Sprintf("line %06d: inserted %d %016x\n", idx, seq, r.Uint64()))
			lines = append(lines[:idx], append([][]byte{line}, lines[idx:]...)...)
		case 2: // 删除一行
			if len(lines) > 1 {
				idx := r.Intn(len(lines))
				lines = append(lines[:idx], lines[idx+1:]...)
			}
		}
	}
	return lines
}

func dirSize(t *testing.T, dir string) int64 {
	t.Helper()
	var total int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return total
}

// checkReads 读取所有版本，校验逐字节一致且每次读取都在时限内。
func checkReads(t *testing.T, s *Store, ids []VersionID, contents map[VersionID][]byte) {
	t.Helper()
	var maxDur time.Duration
	for _, id := range ids {
		start := time.Now()
		got, err := s.Read(id)
		dur := time.Since(start)
		if err != nil {
			t.Fatalf("Read(%s) 失败: %v", id, err)
		}
		if !bytes.Equal(got, contents[id]) {
			t.Fatalf("版本 %s 读回不一致", id)
		}
		if dur > maxDur {
			maxDur = dur
		}
		if dur > perfReadLimit {
			t.Fatalf("版本 %s 读取耗时 %v 超过 %v", id, dur, perfReadLimit)
		}
	}
	t.Logf("读取 %d 个版本, 最慢 %v", len(ids), maxDur)
}

// 场景一：1 MiB 文本连续提交 1000 版，每版改 1~3 行。
func TestLongHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过")
	}
	dir := t.TempDir()
	r := rand.New(rand.NewSource(42))
	lines := genLines(r)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]VersionID, 0, 1000)
	contents := make(map[VersionID][]byte)
	var parent VersionID
	for i := 0; i < 1000; i++ {
		lines = mutate(r, lines, i)
		content := joinLines(lines)
		id, err := s.Commit(parent, content)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		contents[id] = content
		parent = id
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	size := dirSize(t, dir)
	t.Logf("数据目录大小: %.2f MiB (%d 字节)", float64(size)/(1<<20), size)
	if size > perfSizeLimit {
		t.Fatalf("存储量 %.2f MiB 超过 30 MiB", float64(size)/(1<<20))
	}

	// 关掉重开后抽查若干版本
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	sample := []VersionID{ids[0], ids[1], ids[499], ids[998], ids[999]}
	for i := 0; i < 20; i++ {
		sample = append(sample, ids[r.Intn(len(ids))])
	}
	checkReads(t, s2, sample, contents)
}

// 场景二：从同一版本拉两个分支，交替各提交 500 次，期间各恢复几次。
func TestBranchesAndRestore(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过")
	}
	dir := t.TempDir()
	r := rand.New(rand.NewSource(7))

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ids := make([]VersionID, 0, 1100)
	contents := make(map[VersionID][]byte)
	record := func(id VersionID, content []byte) {
		ids = append(ids, id)
		contents[id] = content
	}

	linesA := genLines(r)
	linesB := make([][]byte, len(linesA))
	copy(linesB, linesA)
	baseLines := make([][]byte, len(linesA))
	copy(baseLines, linesA)
	baseContent := joinLines(linesA)
	base, err := s.Commit("", baseContent)
	if err != nil {
		t.Fatal(err)
	}
	record(base, baseContent)

	headA, headB := base, base
	for i := 0; i < 500; i++ {
		// 分支 A 提交
		linesA = mutate(r, linesA, i)
		contentA := joinLines(linesA)
		headA, err = s.Commit(headA, contentA)
		if err != nil {
			t.Fatal(err)
		}
		record(headA, contentA)
		// 分支 B 提交
		linesB = mutate(r, linesB, i)
		contentB := joinLines(linesB)
		headB, err = s.Commit(headB, contentB)
		if err != nil {
			t.Fatal(err)
		}
		record(headB, contentB)
		// 每个分支各恢复到分叉前的版本几次
		if i == 120 || i == 300 || i == 470 {
			headA, err = s.Restore(headA, base)
			if err != nil {
				t.Fatal(err)
			}
			record(headA, baseContent)
			linesA = append([][]byte(nil), baseLines...)
			headB, err = s.Restore(headB, base)
			if err != nil {
				t.Fatal(err)
			}
			record(headB, baseContent)
			linesB = append([][]byte(nil), baseLines...)
		}
	}

	size := dirSize(t, dir)
	t.Logf("数据目录大小: %.2f MiB (%d 字节)", float64(size)/(1<<20), size)
	if size > perfSizeLimit {
		t.Fatalf("存储量 %.2f MiB 超过 30 MiB", float64(size)/(1<<20))
	}

	// 任意版本读取都要在时限内：这里逐一遍历全部版本
	checkReads(t, s, ids, contents)
}
