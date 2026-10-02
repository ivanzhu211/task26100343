# vstore — 文件版本内容存储库

供上层版本库服务调用的 Go 库，把每个文件的各个版本内容持久化到本地数据目录。
无接口层、无外部服务、仅依赖标准库。

## 用法

```go
s, _ := vstore.Open("/path/to/data")
defer s.Close()

v1, _ := s.Commit("file-id", "", content)      // 首个版本的父版本传空串
v2, _ := s.Commit("file-id", v1, newContent)   // 之后每次提交指明父版本
b1, _ := s.Commit("file-id", v1, otherContent) // 从任意旧版本继续提交即成分支
r1, _ := s.Restore("file-id", v2, v1)          // 恢复：以 v2 为父、内容与 v1 一致的新版本

data, _ := s.Read("file-id", v1)               // 任意版本按标识读回，逐字节一致
```

## 存储设计

- **增量链 + 定期快照**：每个版本存为相对基准版本的二进制增量；
  沿链每 64 个版本强制写一次完整快照，单次读取最多重放 63 层增量。
- **二进制增量**（`delta.go`）：对基准内容建 16 字节窗口哈希索引，
  生成 复制/字面量 操作流，对文本小改动和任意二进制都紧凑。
- **内容寻址对象库**（`objects.go`）：所有内容对象按 SHA-256 命名、
  zlib 压缩落盘，相同内容（如恢复产生的重复内容）全局只存一份；
  临时文件 + rename 原子写入。
- **元数据**（`meta.go`）：每个文件一份只追加的 `versions.jsonl` 索引，
  打开数据目录时懒加载进内存；每条记录落盘即 fsync。

数据目录布局：

```
<dir>/objects/<hh>/<rest>     全局对象库（按哈希前两位分桶）
<dir>/files/<hash>/versions.jsonl   每个文件的版本元数据
```

## 验收指标（`go test` 实测，Apple Silicon）

| 场景 | 存储量 | 任意版本读取 |
|---|---|---|
| 1 MiB 文本 × 1000 版长历史 | 11.6 MiB（上限 30 MiB） | 最慢 42 ms（上限 200 ms） |
| 双分支交替各 500 版 + 多次恢复 | 8.9 MiB（上限 30 MiB） | 最慢 27 ms（上限 200 ms） |

两个场景均覆盖：全部版本读回逐字节一致、关闭重开数据目录后再次全量校验。

## 测试

```sh
go test ./...        # 全部（含两个验收场景，约 2 分钟）
go test -run TestLongHistory -v ./...
go test -run TestBranchesAndRestore -v ./...
```
