package vstore

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// 版本内容形态。
const (
	kindFull  = "full"  // Object 里是完整内容
	kindDelta = "delta" // Object 里是相对 Base 版本的增量
)

// versionMeta 描述一个版本如何被还原。
type versionMeta struct {
	ID     string `json:"id"`
	Parent string `json:"parent"` // 父版本标识，首个版本为空
	Base   string `json:"base"`   // 增量基准版本标识；full 时为空
	Kind   string `json:"kind"`   // "full" 或 "delta"
	Object string `json:"object"` // 内容或增量所在的对象标识
	Size   int64  `json:"size"`   // 版本内容原始字节数
	Depth  int    `json:"depth"`  // 距最近快照的增量层数
}

// metaStore 管理某个文件全部版本的元数据，落盘为一份
// 只追加的 JSONL 索引，打开时全量载入内存。
type metaStore struct {
	dir      string
	versions map[string]*versionMeta
}

func openMetaStore(dir string) (*metaStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	m := &metaStore{dir: dir, versions: make(map[string]*versionMeta)}
	f, err := os.Open(m.indexPath())
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var v versionMeta
		if err := json.Unmarshal(line, &v); err != nil {
			return nil, fmt.Errorf("vstore: 元数据索引损坏: %w", err)
		}
		rec := v
		m.versions[rec.ID] = &rec
	}
	return m, sc.Err()
}

func (m *metaStore) indexPath() string {
	return filepath.Join(m.dir, "versions.jsonl")
}

func (m *metaStore) get(id string) (*versionMeta, bool) {
	v, ok := m.versions[id]
	return v, ok
}

// put 追加一条版本元数据并落盘。
func (m *metaStore) put(v *versionMeta) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(m.indexPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	m.versions[v.ID] = v
	return nil
}
