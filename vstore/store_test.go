package vstore

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

func mustCommit(t *testing.T, s *Store, file, parent string, content []byte) string {
	t.Helper()
	id, err := s.Commit(file, parent, content)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return id
}

func mustRead(t *testing.T, s *Store, file, version string) []byte {
	t.Helper()
	b, err := s.Read(file, version)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return b
}

func TestCommitReadBasic(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	v1 := mustCommit(t, s, "f", "", []byte("hello"))
	v2 := mustCommit(t, s, "f", v1, []byte("hello world"))
	v3 := mustCommit(t, s, "f", v2, []byte("hello world!"))

	if got := mustRead(t, s, "f", v1); string(got) != "hello" {
		t.Fatalf("v1 = %q", got)
	}
	if got := mustRead(t, s, "f", v2); string(got) != "hello world" {
		t.Fatalf("v2 = %q", got)
	}
	if got := mustRead(t, s, "f", v3); string(got) != "hello world!" {
		t.Fatalf("v3 = %q", got)
	}
	if v1 == v2 || v2 == v3 {
		t.Fatal("版本标识应互不相同")
	}
}

func TestBinaryEmptyAndNoTrailingNewline(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	rng := rand.New(rand.NewSource(7))
	bin := make([]byte, 300*1024)
	rng.Read(bin)

	vEmpty := mustCommit(t, s, "f", "", nil)
	vBin := mustCommit(t, s, "f", vEmpty, bin)
	vNoNL := mustCommit(t, s, "f", vBin, []byte("no trailing newline"))
	vEmpty2 := mustCommit(t, s, "f", vNoNL, []byte{})

	if got := mustRead(t, s, "f", vEmpty); len(got) != 0 {
		t.Fatalf("空版本读到 %d 字节", len(got))
	}
	if got := mustRead(t, s, "f", vBin); !bytes.Equal(got, bin) {
		t.Fatal("二进制内容不一致")
	}
	if got := mustRead(t, s, "f", vNoNL); string(got) != "no trailing newline" {
		t.Fatalf("无换行结尾内容 = %q", got)
	}
	if got := mustRead(t, s, "f", vEmpty2); len(got) != 0 {
		t.Fatalf("空版本2读到 %d 字节", len(got))
	}
}

func TestBranchAndRestore(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	base := mustCommit(t, s, "f", "", []byte("base content"))
	a1 := mustCommit(t, s, "f", base, []byte("branch A v1"))
	b1 := mustCommit(t, s, "f", base, []byte("branch B v1"))
	a2 := mustCommit(t, s, "f", a1, []byte("branch A v2"))

	// 从 a2 恢复到 base 的内容
	r1, err := s.Restore("f", a2, base)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := mustRead(t, s, "f", r1); string(got) != "base content" {
		t.Fatalf("恢复后内容 = %q", got)
	}
	// 恢复出的版本还能继续提交
	r2 := mustCommit(t, s, "f", r1, []byte("after restore"))
	if got := mustRead(t, s, "f", r2); string(got) != "after restore" {
		t.Fatalf("恢复后续提交 = %q", got)
	}
	// 旧分支不受影响
	if got := mustRead(t, s, "f", b1); string(got) != "branch B v1" {
		t.Fatalf("b1 = %q", got)
	}
	if got := mustRead(t, s, "f", a2); string(got) != "branch A v2" {
		t.Fatalf("a2 = %q", got)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	v1 := mustCommit(t, s, "f", "", []byte("version one"))
	v2 := mustCommit(t, s, "f", v1, []byte("version two"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := mustRead(t, s2, "f", v1); string(got) != "version one" {
		t.Fatalf("重开后 v1 = %q", got)
	}
	if got := mustRead(t, s2, "f", v2); string(got) != "version two" {
		t.Fatalf("重开后 v2 = %q", got)
	}
	// 重开后继续提交
	v3 := mustCommit(t, s2, "f", v2, []byte("version three"))
	if got := mustRead(t, s2, "f", v3); string(got) != "version three" {
		t.Fatalf("重开后 v3 = %q", got)
	}
}

func TestErrors(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.Commit("f", "nonexistent-parent", []byte("x")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未知父版本应报 ErrNotFound, got %v", err)
	}
	if _, err := s.Read("f", "nonexistent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未知版本应报 ErrNotFound, got %v", err)
	}
	if s.Exists("f", "nonexistent") {
		t.Fatal("Exists 应返回 false")
	}
}

func TestIdempotentCommit(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := mustCommit(t, s, "f", "", []byte("same"))
	b := mustCommit(t, s, "f", "", []byte("same"))
	if a != b {
		t.Fatal("相同父版本+相同内容应得到相同版本标识")
	}
}

func TestMultipleFiles(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	va := mustCommit(t, s, "file-a", "", []byte("aaa"))
	vb := mustCommit(t, s, "file-b", "", []byte("bbb"))
	if got := mustRead(t, s, "file-a", va); string(got) != "aaa" {
		t.Fatalf("file-a = %q", got)
	}
	if got := mustRead(t, s, "file-b", vb); string(got) != "bbb" {
		t.Fatalf("file-b = %q", got)
	}
}
