package vstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrNotFound 表示文件或版本不存在。
var ErrNotFound = errors.New("vstore: 版本不存在")

// versionID 由父版本与内容共同决定，相同提交幂等。
func versionID(parent string, content []byte) string {
	h := sha256.New()
	h.Write([]byte(parent))
	h.Write([]byte{0})
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// Commit 提交 fileID 的一个新版本。parent 为父版本标识，
// 首个版本传空串。返回新版本的标识。
func (s *Store) Commit(fileID, parent string, content []byte) (string, error) {
	return s.commit(fileID, parent, parent, content)
}

// Restore 把文件恢复到 target 版本的内容，生成一个以 parent
// 为父版本的新版本，返回新版本标识。
// 增量直接以 target 为基准，恢复几乎不占额外空间。
func (s *Store) Restore(fileID, parent, target string) (string, error) {
	content, err := s.Read(fileID, target)
	if err != nil {
		return "", err
	}
	return s.commit(fileID, parent, target, content)
}

// commit 是提交的内部实现：parent 是历史父版本，base 是增量基准版本。
func (s *Store) commit(fileID, parent, base string, content []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.metaFor(fileID)
	if err != nil {
		return "", err
	}
	id := versionID(parent, content)
	if _, ok := meta.get(id); ok {
		return id, nil // 幂等：相同父版本 + 相同内容
	}

	v := &versionMeta{ID: id, Parent: parent, Size: int64(len(content))}
	switch {
	case parent == "":
		v.Kind = kindFull
	case base == "":
		return "", fmt.Errorf("vstore: 非首个版本缺少增量基准: %w", ErrNotFound)
	default:
		baseMeta, ok := meta.get(base)
		if !ok {
			return "", fmt.Errorf("vstore: 基准版本 %s: %w", base, ErrNotFound)
		}
		if _, ok := meta.get(parent); !ok {
			return "", fmt.Errorf("vstore: 父版本 %s: %w", parent, ErrNotFound)
		}
		if baseMeta.Depth+1 >= snapshotInterval {
			v.Kind = kindFull
			break
		}
		baseContent, err := s.readLocked(meta, baseMeta)
		if err != nil {
			return "", err
		}
		delta := encodeDelta(baseContent, content)
		if len(delta) >= len(content) {
			v.Kind = kindFull // 增量不划算，直接存全量
			break
		}
		obj, err := s.objects.put(delta)
		if err != nil {
			return "", err
		}
		v.Kind = kindDelta
		v.Base = base
		v.Object = obj
		v.Depth = baseMeta.Depth + 1
	}
	if v.Kind == kindFull {
		obj, err := s.objects.put(content)
		if err != nil {
			return "", err
		}
		v.Object = obj
	}
	if err := meta.put(v); err != nil {
		return "", err
	}
	return id, nil
}

// Read 按版本标识读回内容，保证与提交时逐字节一致。
func (s *Store) Read(fileID, version string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.metaFor(fileID)
	if err != nil {
		return nil, err
	}
	v, ok := meta.get(version)
	if !ok {
		return nil, fmt.Errorf("vstore: 版本 %s: %w", version, ErrNotFound)
	}
	return s.readLocked(meta, v)
}

// readLocked 沿增量基准链回放到最近快照，再正向应用增量。调用方须持有 s.mu。
func (s *Store) readLocked(meta *metaStore, v *versionMeta) ([]byte, error) {
	var chain []*versionMeta
	cur := v
	for cur.Kind == kindDelta {
		chain = append(chain, cur)
		base, ok := meta.get(cur.Base)
		if !ok {
			return nil, fmt.Errorf("vstore: 基准版本 %s 元数据缺失", cur.Base)
		}
		cur = base
	}
	content, err := s.objects.get(cur.Object)
	if err != nil {
		return nil, err
	}
	for i := len(chain) - 1; i >= 0; i-- {
		delta, err := s.objects.get(chain[i].Object)
		if err != nil {
			return nil, err
		}
		content, err = applyDelta(content, delta)
		if err != nil {
			return nil, err
		}
	}
	if int64(len(content)) != v.Size {
		return nil, fmt.Errorf("vstore: 版本 %s 内容长度校验失败", v.ID)
	}
	return content, nil
}

// Exists 报告版本是否存在。
func (s *Store) Exists(fileID, version string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.metaFor(fileID)
	if err != nil {
		return false
	}
	_, ok := meta.get(version)
	return ok
}
