// Package vstore 提供文件版本内容存储：内容定义分块 + 内容寻址块存储。
// 小改动只产生少量新块（省空间），读取按清单直接拼块（无 delta 链，速度快）。
package vstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// VersionID 是版本的唯一标识。
type VersionID string

// Store 是版本内容存储库，由上层的版本库服务调用。
type Store struct {
	dir string
	mu  sync.Mutex // 保护提交/恢复的序号分配
	cs  *chunkStore
	seq uint64 // 已分配的最大版本序号
}

// Open 打开（必要时创建）位于 dir 的数据目录。
func Open(dir string) (*Store, error) {
	for _, sub := range []string{"", "versions", "chunks"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	cs, err := openChunkStore(filepath.Join(dir, "chunks"))
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir, cs: cs}
	entries, err := os.ReadDir(filepath.Join(dir, "versions"))
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".tmp-") {
			continue
		}
		seq, err := parseVersionID(VersionID(name))
		if err != nil {
			continue
		}
		if seq > s.seq {
			s.seq = seq
		}
	}
	return s, nil
}

// Close 关闭存储库。所有数据在写入时已落盘，这里只做状态清理。
func (s *Store) Close() error { return nil }

// Commit 以 parent 为父版本提交内容，返回新版本标识。首个版本 parent 传空串。
func (s *Store) Commit(parent VersionID, content []byte) (VersionID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parentSeq, err := s.checkParent(parent)
	if err != nil {
		return "", err
	}
	m := &manifest{parentSeq: parentSeq}
	cuts := chunkBoundaries(content)
	start := 0
	for _, end := range cuts {
		chunk := content[start:end]
		key, err := s.cs.put(chunk)
		if err != nil {
			return "", err
		}
		m.chunks = append(m.chunks, chunkRef{key: key, len: uint32(len(chunk))})
		start = end
	}
	return s.saveManifest(m)
}

// Restore 生成一个新版本：父版本为 parent，内容与 target 版本完全相同。
func (s *Store) Restore(parent, target VersionID) (VersionID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parentSeq, err := s.checkParent(parent)
	if err != nil {
		return "", err
	}
	tm, err := s.loadManifest(target)
	if err != nil {
		return "", err
	}
	m := &manifest{parentSeq: parentSeq, chunks: tm.chunks}
	return s.saveManifest(m)
}

// Read 按标识读回某个版本的完整内容。
func (s *Store) Read(id VersionID) ([]byte, error) {
	m, err := s.loadManifest(id)
	if err != nil {
		return nil, err
	}
	total := 0
	for _, c := range m.chunks {
		total += int(c.len)
	}
	out := make([]byte, 0, total)
	for _, c := range m.chunks {
		data, err := s.cs.get(c.key)
		if err != nil {
			return nil, fmt.Errorf("vstore: 读取版本 %s 的块失败: %w", id, err)
		}
		if len(data) != int(c.len) {
			return nil, fmt.Errorf("vstore: 版本 %s 的块长度不符", id)
		}
		out = append(out, data...)
	}
	return out, nil
}

// checkParent 校验父版本标识，返回其序号。空串表示没有父版本。
func (s *Store) checkParent(parent VersionID) (uint64, error) {
	if parent == "" {
		return 0, nil
	}
	seq, err := parseVersionID(parent)
	if err != nil {
		return 0, err
	}
	if _, err := s.loadManifest(parent); err != nil {
		return 0, fmt.Errorf("vstore: 父版本无效: %w", err)
	}
	return seq, nil
}
