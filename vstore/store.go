// Package vstore 提供文件版本内容存储：以增量链 + 定期快照的方式
// 把每个文件的各个版本内容持久化到本地数据目录。
//
// 数据目录布局：
//
//	<dir>/objects/            全局内容寻址对象库（zlib 压缩）
//	<dir>/files/<hash>/       每个文件一个目录，内含 versions.jsonl 元数据索引
package vstore

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
)

// snapshotInterval 是沿增量链每隔多少个版本强制写一次完整快照，
// 把单次读取需要重放的增量层数限制在该值以内。
const snapshotInterval = 64

// Store 是一个打开的数据目录，负责所有文件的版本内容存取。
type Store struct {
	dir     string
	objects *objectStore

	mu    sync.Mutex
	metas map[string]*metaStore // fileID -> 元数据索引（懒加载）
}

// Open 打开（必要时创建）一个数据目录。
func Open(dir string) (*Store, error) {
	objects, err := openObjectStore(filepath.Join(dir, "objects"))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o755); err != nil {
		return nil, err
	}
	return &Store{
		dir:     dir,
		objects: objects,
		metas:   make(map[string]*metaStore),
	}, nil
}

// Close 关闭数据目录。所有写入在提交时已落盘，这里只释放内存索引。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metas = make(map[string]*metaStore)
	return nil
}

// fileDir 返回 fileID 对应的目录名（哈希隔离，调用方可传任意字符串）。
func (s *Store) fileDir(fileID string) string {
	sum := sha256.Sum256([]byte(fileID))
	return filepath.Join(s.dir, "files", hex.EncodeToString(sum[:]))
}

// metaFor 懒加载某个文件的元数据索引。调用方须持有 s.mu。
func (s *Store) metaFor(fileID string) (*metaStore, error) {
	if m, ok := s.metas[fileID]; ok {
		return m, nil
	}
	m, err := openMetaStore(s.fileDir(fileID))
	if err != nil {
		return nil, err
	}
	s.metas[fileID] = m
	return m, nil
}
