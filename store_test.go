package vstore

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func mustCommit(t *testing.T, s *Store, parent VersionID, content []byte) VersionID {
	t.Helper()
	id, err := s.Commit(parent, content)
	if err != nil {
		t.Fatalf("Commit 失败: %v", err)
	}
	return id
}

func mustRead(t *testing.T, s *Store, id VersionID) []byte {
	t.Helper()
	data, err := s.Read(id)
	if err != nil {
		t.Fatalf("Read(%s) 失败: %v", id, err)
	}
	return data
}

func TestRoundtripBasic(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	cases := [][]byte{
		{},                               // 空内容
		[]byte("hello"),                  // 无结尾换行
		[]byte("line1\nline2\n"),         // 有结尾换行
		bytes.Repeat([]byte("x"), 1<<20), // 1 MiB 重复文本
	}
	bin := make([]byte, 100000)
	if _, err := rand.Read(bin); err != nil {
		t.Fatal(err)
	}
	cases = append(cases, bin) // 随机二进制

	var parent VersionID
	for i, c := range cases {
		id := mustCommit(t, s, parent, c)
		got := mustRead(t, s, id)
		if !bytes.Equal(got, c) {
			t.Fatalf("用例 %d 读回不一致: 得 %d 字节, 要 %d 字节", i, len(got), len(c))
		}
		parent = id
	}
}

func TestReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("持久化测试\n没有结尾换行")
	id := mustCommit(t, s, "", content)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got := mustRead(t, s2, id)
	if !bytes.Equal(got, content) {
		t.Fatal("重开后读回不一致")
	}
	// 重开后继续提交，父版本有效
	id2 := mustCommit(t, s2, id, []byte("第二版"))
	if !bytes.Equal(mustRead(t, s2, id2), []byte("第二版")) {
		t.Fatal("重开后提交读回不一致")
	}
}

func TestBranchAndRestore(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	base := mustCommit(t, s, "", []byte("base"))
	a1 := mustCommit(t, s, base, []byte("branch A v1"))
	b1 := mustCommit(t, s, base, []byte("branch B v1"))
	a2 := mustCommit(t, s, a1, []byte("branch A v2"))

	// 从 a2 恢复到 base 的内容
	r, err := s.Restore(a2, base)
	if err != nil {
		t.Fatalf("Restore 失败: %v", err)
	}
	if got := mustRead(t, s, r); !bytes.Equal(got, []byte("base")) {
		t.Fatalf("恢复内容不一致: %q", got)
	}
	// 各分支互不影响
	if got := mustRead(t, s, b1); !bytes.Equal(got, []byte("branch B v1")) {
		t.Fatalf("分支 B 内容不一致: %q", got)
	}
	if got := mustRead(t, s, a2); !bytes.Equal(got, []byte("branch A v2")) {
		t.Fatalf("分支 A 内容不一致: %q", got)
	}
	// 在恢复出的版本上继续提交
	r2 := mustCommit(t, s, r, []byte("after restore"))
	if got := mustRead(t, s, r2); !bytes.Equal(got, []byte("after restore")) {
		t.Fatal("恢复后继续提交读回不一致")
	}
}

func TestInvalidParent(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Commit("v999999999999", []byte("x")); err == nil {
		t.Fatal("不存在的父版本应当报错")
	}
	if _, err := s.Commit("not-a-version", []byte("x")); err == nil {
		t.Fatal("非法父版本标识应当报错")
	}
	if _, err := s.Read("v000000000001"); err == nil {
		t.Fatal("读不存在的版本应当报错")
	}
}
