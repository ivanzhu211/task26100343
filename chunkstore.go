package vstore

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
)

// chunkKey 是块的内容寻址标识（SHA-256 的前 16 字节）。
type chunkKey [16]byte

// chunkStore 管理磁盘上的压缩块文件。
type chunkStore struct {
	dir string
}

func openChunkStore(dir string) (*chunkStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &chunkStore{dir: dir}, nil
}

func (c *chunkStore) path(key chunkKey) string {
	h := hex.EncodeToString(key[:])
	return filepath.Join(c.dir, h[:2], h[2:])
}

// put 写入一个块（内容相同的块已存在则跳过），返回其标识。
func (c *chunkStore) put(data []byte) (chunkKey, error) {
	sum := sha256.Sum256(data)
	var key chunkKey
	copy(key[:], sum[:16])
	p := c.path(key)
	if _, err := os.Stat(p); err == nil {
		return key, nil
	}
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return chunkKey{}, err
	}
	if _, err := w.Write(data); err != nil {
		return chunkKey{}, err
	}
	if err := w.Close(); err != nil {
		return chunkKey{}, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return chunkKey{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return chunkKey{}, err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return chunkKey{}, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return chunkKey{}, err
	}
	if err := os.Rename(tmpName, p); err != nil {
		os.Remove(tmpName)
		return chunkKey{}, err
	}
	return key, nil
}

// get 按标识读出块的原始内容。
func (c *chunkStore) get(key chunkKey) ([]byte, error) {
	raw, err := os.ReadFile(c.path(key))
	if err != nil {
		return nil, err
	}
	r := flate.NewReader(bytes.NewReader(raw))
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return data, nil
}
