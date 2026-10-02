package vstore

// 分块参数：最小 2KiB，平均 8KiB，最大 32KiB。
const (
	minChunkSize = 2 << 10
	maxChunkSize = 32 << 10
	chunkMask    = (1 << 13) - 1 // 平均每 8192 字节出现一个切点
)

// gearTable 是确定性伪随机表，用于滚动哈希。
var gearTable [256]uint64

func init() {
	var s uint64 = 0x243f6a8885a308d3
	for i := range gearTable {
		// splitmix64
		s += 0x9e3779b97f4a7c15
		z := s
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		gearTable[i] = z ^ (z >> 31)
	}
}

// chunkBoundaries 返回内容的分块切点（内容定义分块）。
// 插入或删除少量字节只会影响附近的块，其余块的边界保持不变。
func chunkBoundaries(data []byte) []int {
	n := len(data)
	if n == 0 {
		return nil
	}
	var cuts []int
	start := 0
	for start < n {
		scan := start + minChunkSize
		if scan >= n {
			cuts = append(cuts, n)
			break
		}
		limit := start + maxChunkSize
		if limit > n {
			limit = n
		}
		var fp uint64
		cut := -1
		for i := scan; i < limit; i++ {
			fp = (fp << 1) + gearTable[data[i]]
			if fp&chunkMask == 0 {
				cut = i + 1
				break
			}
		}
		if cut < 0 {
			cut = limit
		}
		cuts = append(cuts, cut)
		start = cut
	}
	return cuts
}
