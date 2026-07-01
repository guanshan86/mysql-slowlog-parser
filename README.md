# slowlog-parser

[![Go Version](https://img.shields.io/badge/Go-1.16+-00ADD8)](https://go.dev/)
[![MySQL](https://img.shields.io/badge/MySQL-5.6%20%7C%205.7%20%7C%208.0-4479A1)](https://www.mysql.com/)

A Go library for parsing MySQL slow-query logs into structured `Event`s, with built-in SQL **fingerprint** normalization and **Query ID** generation for aggregating and de-duplicating slow SQL.

MySQL 慢查询日志（slow log）解析库，使用 Go 实现。可将一段原始慢日志文本解析为结构化的 `Event`，并支持 **SQL 指纹（fingerprint）** 归一化与 **Query ID** 生成，方便对慢 SQL 做聚合统计与去重分析。

## Compatibility / 兼容性

- MySQL `[5.6, 5.7, 8.0]`
- Percona `[5.6, 5.7, 8.0]`

Supported log elements / 支持解析的日志元素：

- `# Time` timestamps (legacy `060102 15:04:05` and new `2026-07-01T02:59:56.295810Z` formats) / 时间戳（兼容旧格式与新格式）
- `# User@Host` user, host, connection `Id` / 用户、主机、线程 ID
- `# Schema` / `Last_errno` / `Killed`
- `# Query_time` / `Lock_time` / `Rows_sent` / `Rows_examined` / `Rows_affected` / `Bytes_sent`
- Percona extended metrics: `Rows_read` / `Merge_passes` / `Full_scan` / `Filesort` / `Tmp_table` / `Log_slow_rate_type` / `Log_slow_rate_limit` / Percona 扩展指标
- MySQL 8.0 `# Optimize_cost: {json}` optimizer cost JSON / 优化成本 JSON
- MariaDB / Percona `# explain:` plans (multi-line accumulation) / 执行计划（多行累积）
- `# admin` administrator commands (e.g. `administrator command: Init DB`) / 管理员命令
- `SET timestamp=` / `USE db` lines / 语句行

## Installation / 安装

```bash
go get github.com/guanshan86/mysql-slowlog-parser
```

## Quick Start / 快速开始

```go
package main

import (
    "fmt"

    "github.com/guanshan86/mysql-slowlog-parser/log"
    "github.com/guanshan86/mysql-slowlog-parser/parser"
)

func main() {
    text := "# Time: 2026-07-01T02:59:56.295810Z\n" +
        "# User@Host: h_metadata_rw[h_metadata_rw] @  [10.90.52.125]  Id: 1301119143\n" +
        "# Schema: metadata  Last_errno: 66  Killed: 88\n" +
        "# Query_time: 1.010292  Lock_time: 0.000151  Rows_sent: 1  Rows_examined: 1149878  Rows_affected: 0\n" +
        "# Bytes_sent: 58\n" +
        "# Optimize_cost: {\"rows\": 100}\n" +
        "SET timestamp=1782874796;\n" +
        "SELECT * FROM users WHERE name LIKE '%abc%' ORDER BY id DESC LIMIT 100;"

    p := parser.NewSlowLogParser(log.Options{})

    event, err := p.Parser(text)
    if err != nil {
        panic(err)
    }

    fmt.Println("Query    :", event.Query)
    fmt.Println("Finger   :", p.Fingerprint())
    fmt.Println("QueryID  :", p.Id())
    fmt.Println("Duration :", event.QueryTime)
    fmt.Println("Examined :", event.RowsExamined)
}
```

Run [`main.go`](main.go) for full field output / 运行 [`main.go`](main.go) 可查看完整字段输出：

```bash
go run main.go
```

## Core Concepts / 核心概念

### The Event struct / Event 结构

The parsed result lives in `log.Event` / 解析结果保存在 `log.Event` 中：

| Field / 字段 | Type / 类型 | Description / 说明 |
| --- | --- | --- |
| `Time` / `Ts` | `time.Time` / `string` | Native time from `# Time`; `Ts` is the string form for backward compatibility / `# Time` 解析出的原生时间；`Ts` 为字符串形式（兼容旧字段） |
| `User` / `Host` / `Db` / `ThreadId` | `string` / `string` / `string` / `uint64` | Connection source and database / 连接来源与数据库 |
| `Query` | `string` | Raw SQL or admin command / 原始 SQL 或管理命令 |
| `Explain` | `string` | `# explain:` lines accumulated / `# explain:` 多行累积的执行计划 |
| `OptimizeCost` | `json.RawMessage` | MySQL 8.0 optimizer cost JSON / 优化成本 JSON |
| `QueryTime` / `LockTime` | `float64` | Query / lock duration in seconds / 查询 / 锁耗时（秒） |
| `RowsSent` / `RowsExamined` / `RowsAffected` / `BytesSent` | `uint64` | Row counts and bytes / 行数与字节数 |
| `LastErrno` / `Killed` | `uint64` | Error code and kill flag / 错误码与被 kill 标记 |
| `RowsRead` / `MergePasses` / `FullScan` / `Filesort` / `TmpTable` | Percona extended metrics / Percona 扩展指标 | |
| `TimeMetrics` / `NumberMetrics` / `BoolMetrics` | `map` | Generic metric maps holding unmapped metrics / 通用指标映射，存放未明确映射的指标 |
| `Admin` | `bool` | Whether the query is an admin command / 是否为管理员命令 |

Known metrics are written to both a strongly-typed field (e.g. `QueryTime`) and the generic `map` (e.g. `TimeMetrics["Query_time"]`), so they are both convenient to use and complete. Access unknown metrics with `event.TimeMetric(name)`, `event.NumberMetric(name)`, `event.BoolMetric(name)`.

针对已知的常用指标，库会同时写入强类型字段（如 `QueryTime`）与通用 `map`（`TimeMetrics["Query_time"]`），既方便直接取用，也保留了完整性。访问未知的指标可用 `event.TimeMetric(name)`、`event.NumberMetric(name)`、`event.BoolMetric(name)`。

### Fingerprint / 指纹归一化

`p.Fingerprint()` normalizes SQL into an aggregatable canonical form. Main transformations:

- Replace literals (strings, numbers, `NULL`) with `?`
- Fold `IN (...)` / `VALUES (...)` lists into `(?+)`
- Collapse whitespace and remove comments
- Lowercase everything
- Drop syntax noise that doesn't affect performance (e.g. `ASC` in `ORDER BY ... ASC`)

`p.Fingerprint()` 将 SQL 归一化为可聚合的规范形式，主要变换：

- 字面量（字符串、数字、`NULL`）替换为 `?`
- `IN (...)` / `VALUES (...)` 列表折叠为 `(?+)`
- 折叠多余空白、移除注释
- 全部转小写
- 去除不影响性能的语法噪音（如 `ORDER BY ... ASC` 中的 `ASC`）

Example: `SELECT * FROM t WHERE name='Bob' AND age=25` → `select * from t where name=? and age=?`. With `Options.ReplaceNumbersInWords` enabled, digits in identifiers are also replaced, e.g. `db37.t` → `db?.t`.

### Query ID

`p.Id()` returns the last 16 hex characters (upper case) of the MD5 of the fingerprint, a compact unique identifier for that SQL shape — handy for aggregation by SQL pattern.

`p.Id()` 返回指纹 MD5 的后 16 位十六进制字符（大写），作为该类 SQL 的紧凑唯一标识，便于按 SQL 形态聚合统计。

## Options / 配置选项

Configure the parser via `log.Options` / 通过 `log.Options` 配置解析器：

```go
parser.NewSlowLogParser(log.Options{
    DefaultLocation:       time.UTC,                        // timezone for legacy timestamps, defaults to time.Local / 旧格式时间戳的时区，默认 time.Local
    FilterAdminCommand:    map[string]bool{"Init DB": true}, // admin commands to filter out / 需过滤的管理命令
    Debug:                 false,                            // print char-by-char parse trace / 开启后输出逐字符解析轨迹
    Debugf:                nil,                              // custom debug logger / 自定义 debug 日志函数
    ReplaceNumbersInWords: false,                            // replace digits in identifiers / 是否替换标识符中的数字
})
```

- `DefaultLocation` — old logs (MySQL < 5.7) have no timezone in the timestamp; parsed against this location. / 旧版日志时间未带时区，按此 location 解析。
- `FilterAdminCommand` — matching admin commands do not produce an event. / 命中后该 admin 命令不生成事件。
- `Debug` / `Debugf` — for debugging the fingerprint state machine, which prints state transitions char by char when enabled. / 调试指纹状态机时使用；指纹算法较复杂，开启会逐字符打印状态机迁移。

## Error Handling / 错误处理

`Parser` returns the **first** parse error encountered (e.g. an unrecognized `# Time` format). Sub-piece failures fall back to zero values instead of aborting the whole event. On error the `Event` is still returned with whatever fields were parsed; the caller decides whether to discard based on `err`.

`Parser` 返回解析过程中遇到的**第一个**错误（如无法识别的 `# Time` 时间格式），子片段解析失败时回退到零值而不会中断整条事件。遇到错误时 `Event` 仍会返回已解析到的字段，调用方可根据 `err` 决定是否丢弃。

## Tests / 运行测试

```bash
go test ./parser/...
```

The test suite covers basic events, legacy/new time formats, `Optimize_cost`, EXPLAIN accumulation, Percona boolean/number metrics, fingerprint & QueryID, and parse-error exposure.

测试覆盖了基础事件、旧/新时间格式、`Optimize_cost`、EXPLAIN 累积、Percona 布尔/数值指标、指纹与 QueryID、以及解析错误暴露等场景。

## Project Structure / 项目结构

```
.
├── main.go              # usage example / 使用示例
├── go.mod
├── log/
│   └── log.go           # Event / Options definitions / Event / Options 数据结构定义
└── parser/
    ├── parser.go        # main slow-log parsing logic / 慢日志解析主逻辑
    ├── finger.go        # SQL fingerprint normalization & Query ID / SQL 指纹归一化与 Query ID
    └── parser_test.go   # unit tests / 单元测试
```

## License

MIT