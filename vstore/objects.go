package vstore

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// objectStore 是按内容寻址的对象库，对象以 zlib 压缩形式落盘。
// 相同内容只存一份，天然去重。
type objectStore struct {
	dir string
}

func openObjectStore(dir string) (*objectStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &objectStore{dir: dir}, nil
}

func (o *objectStore) path(id string) string {
	// 按前两位十六进制分桶，避免单目录文件过多。
	return filepath.Join(o.dir, id[:2], id[2:])
}

// put 写入一个对象，返回其对象标识；相同内容重复写入是幂等的。
func (o *objectStore) put(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	id := hex.EncodeToString(sum[:])
	p := o.path(id)
	if _, err := os.Stat(p); err == nil {
		return id, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}

	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return id, nil
}

// get 按标识读出对象内容。
func (o *objectStore) get(id string) ([]byte, error) {
	f, err := os.Open(o.path(id))
	if err != nil {
		return nil, fmt.Errorf("vstore: 读对象 %s: %w", id, err)
	}
	defer f.Close()
	zr, err := zlib.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("vstore: 解压对象 %s: %w", id, err)
	}
	defer zr.Close()
	data, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("vstore: 解压对象 %s: %w", id, err)
	}
	return data, nil
}
