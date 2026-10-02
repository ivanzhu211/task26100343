package vstore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// chunkRef 引用一个块及其原始长度。
type chunkRef struct {
	key chunkKey
	len uint32
}

// manifest 记录一个版本由哪些块按序组成。
type manifest struct {
	parentSeq uint64 // 0 表示没有父版本
	chunks    []chunkRef
}

var manifestMagic = []byte("VMF1")

// versionID 把序号格式化为版本标识。
func versionID(seq uint64) VersionID {
	return VersionID(fmt.Sprintf("v%012d", seq))
}

// parseVersionID 解析版本标识，返回序号。
func parseVersionID(id VersionID) (uint64, error) {
	s := string(id)
	if len(s) != 13 || s[0] != 'v' {
		return 0, fmt.Errorf("vstore: 非法版本标识 %q", id)
	}
	n, err := strconv.ParseUint(s[1:], 10, 64)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("vstore: 非法版本标识 %q", id)
	}
	return n, nil
}

func (s *Store) manifestPath(id VersionID) string {
	return filepath.Join(s.dir, "versions", string(id))
}

// encode 把清单编码为紧凑的二进制形式。
func (m *manifest) encode() []byte {
	buf := make([]byte, 0, 12+len(m.chunks)*20)
	buf = append(buf, manifestMagic...)
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], m.parentSeq)
	buf = append(buf, tmp[:]...)
	binary.BigEndian.PutUint32(tmp[:4], uint32(len(m.chunks)))
	buf = append(buf, tmp[:4]...)
	for _, c := range m.chunks {
		buf = append(buf, c.key[:]...)
		binary.BigEndian.PutUint32(tmp[:4], c.len)
		buf = append(buf, tmp[:4]...)
	}
	return buf
}

// decodeManifest 解析清单的二进制形式。
func decodeManifest(data []byte) (*manifest, error) {
	if len(data) < 16 || string(data[:4]) != string(manifestMagic) {
		return nil, errors.New("vstore: 清单格式错误")
	}
	m := &manifest{parentSeq: binary.BigEndian.Uint64(data[4:12])}
	count := binary.BigEndian.Uint32(data[12:16])
	if len(data) != 16+int(count)*20 {
		return nil, errors.New("vstore: 清单长度不符")
	}
	m.chunks = make([]chunkRef, count)
	off := 16
	for i := range m.chunks {
		copy(m.chunks[i].key[:], data[off:off+16])
		m.chunks[i].len = binary.BigEndian.Uint32(data[off+16 : off+20])
		off += 20
	}
	return m, nil
}

// saveManifest 把清单原子写入数据目录，返回新版本标识。调用方需持有 s.mu。
func (s *Store) saveManifest(m *manifest) (VersionID, error) {
	seq := s.seq + 1
	id := versionID(seq)
	p := s.manifestPath(id)
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(m.encode()); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, p); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	s.seq = seq
	return id, nil
}

// loadManifest 按标识读出清单。
func (s *Store) loadManifest(id VersionID) (*manifest, error) {
	if _, err := parseVersionID(id); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.manifestPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("vstore: 版本不存在 %q", id)
		}
		return nil, err
	}
	return decodeManifest(data)
}
