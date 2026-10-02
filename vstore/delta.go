package vstore

import (
	"encoding/binary"
	"fmt"
)

// 增量格式：
//   uvarint(targetLen) 后跟一串操作，直到输入耗尽：
//   tag=0 字面量：uvarint(n) + n 字节原始数据
//   tag=1 复制：  uvarint(baseOffset) + uvarint(n)

const (
	deltaWindow    = 16 // 匹配窗口字节数
	deltaMaxChain  = 8  // 哈希链上最多尝试的候选数
	deltaHashShift = 64 - 21
)

func deltaHash(b []byte) uint64 {
	h := binary.LittleEndian.Uint64(b)
	h *= 0x9E3779B97F4A7C15
	return h >> deltaHashShift
}

// encodeDelta 计算从 base 到 target 的二进制增量。
func encodeDelta(base, target []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(len(target)))
	if len(target) == 0 {
		return out
	}
	if len(base) < deltaWindow || len(target) < deltaWindow {
		return appendLiteral(out, target)
	}

	// 为 base 建哈希索引：heads 是开地址表头，prev 是链。
	heads := make([]int32, 1<<21)
	for i := range heads {
		heads[i] = -1
	}
	prev := make([]int32, len(base)-deltaWindow+1)
	for i := 0; i+deltaWindow <= len(base); i++ {
		h := deltaHash(base[i:])
		prev[i] = heads[h]
		heads[h] = int32(i)
	}

	var lit []byte
	flush := func() {
		if len(lit) > 0 {
			out = appendLiteral(out, lit)
			lit = lit[:0]
		}
	}

	i := 0
	for i < len(target) {
		bestLen, bestOff := 0, 0
		if i+deltaWindow <= len(target) {
			h := deltaHash(target[i:])
			for j, tries := heads[h], 0; j >= 0 && tries < deltaMaxChain; tries++ {
				off := int(j)
				n := matchLen(base[off:], target[i:])
				if n > bestLen {
					bestLen, bestOff = n, off
				}
				j = prev[j]
			}
		}
		if bestLen >= deltaWindow {
			flush()
			out = append(out, 1)
			out = binary.AppendUvarint(out, uint64(bestOff))
			out = binary.AppendUvarint(out, uint64(bestLen))
			i += bestLen
		} else {
			lit = append(lit, target[i])
			i++
		}
	}
	flush()
	return out
}

func matchLen(a, b []byte) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func appendLiteral(out, data []byte) []byte {
	out = append(out, 0)
	out = binary.AppendUvarint(out, uint64(len(data)))
	return append(out, data...)
}

// applyDelta 把增量应用到 base 上，还原出 target。
func applyDelta(base, delta []byte) ([]byte, error) {
	targetLen, n := binary.Uvarint(delta)
	if n <= 0 {
		return nil, fmt.Errorf("vstore: 增量头损坏")
	}
	delta = delta[n:]
	out := make([]byte, 0, targetLen)
	for len(delta) > 0 {
		tag := delta[0]
		delta = delta[1:]
		switch tag {
		case 0:
			l, m := binary.Uvarint(delta)
			if m <= 0 || uint64(len(delta)-m) < l {
				return nil, fmt.Errorf("vstore: 增量字面量损坏")
			}
			out = append(out, delta[m:m+int(l)]...)
			delta = delta[m+int(l):]
		case 1:
			off, m1 := binary.Uvarint(delta)
			if m1 <= 0 {
				return nil, fmt.Errorf("vstore: 增量复制损坏")
			}
			l, m2 := binary.Uvarint(delta[m1:])
			if m2 <= 0 || off+l > uint64(len(base)) {
				return nil, fmt.Errorf("vstore: 增量复制越界")
			}
			out = append(out, base[int(off):int(off)+int(l)]...)
			delta = delta[m1+m2:]
		default:
			return nil, fmt.Errorf("vstore: 未知增量操作 %d", tag)
		}
	}
	if uint64(len(out)) != targetLen {
		return nil, fmt.Errorf("vstore: 增量结果长度不符")
	}
	return out, nil
}
