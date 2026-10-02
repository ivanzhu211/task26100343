package vstore

import (
	"bytes"
	"math/rand"
	"testing"
)

func roundTrip(t *testing.T, base, target []byte) {
	t.Helper()
	d := encodeDelta(base, target)
	got, err := applyDelta(base, d)
	if err != nil {
		t.Fatalf("applyDelta: %v", err)
	}
	if !bytes.Equal(got, target) {
		t.Fatalf("round trip mismatch: base=%d target=%d got=%d", len(base), len(target), len(got))
	}
}

func TestDeltaRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	base := make([]byte, 1<<20)
	rng.Read(base)

	roundTrip(t, base, base)                   // 完全相同
	roundTrip(t, base, nil)                    // 目标为空
	roundTrip(t, nil, base)                    // 基准为空
	roundTrip(t, nil, nil)                     // 都为空
	roundTrip(t, []byte("abc"), []byte("abd")) // 小于窗口

	// 随机改 100 个字节
	mod := append([]byte(nil), base...)
	for i := 0; i < 100; i++ {
		mod[rng.Intn(len(mod))] = byte(rng.Intn(256))
	}
	roundTrip(t, base, mod)

	// 中间插入一段
	ins := append(append([]byte(nil), base[:500000]...), append([]byte("INSERTED-CHUNK"), base[500000:]...)...)
	roundTrip(t, base, ins)

	// 删掉一段
	del := append([]byte(nil), base[:100]...)
	del = append(del, base[200:]...)
	roundTrip(t, base, del)
}

func TestDeltaSizeForSmallEdit(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	base := make([]byte, 1<<20)
	rng.Read(base)
	mod := append([]byte(nil), base...)
	copy(mod[12345:], []byte("a small local edit"))
	d := encodeDelta(base, mod)
	if len(d) > 4096 {
		t.Fatalf("小改动的增量过大: %d 字节", len(d))
	}
}
