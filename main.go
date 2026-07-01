package main

import (
	"fmt"

	"github.com/eopenio/slowlog-parser/log"
	"github.com/eopenio/slowlog-parser/parser"
)

func main() {
	var opt log.Options
	SlowLogParser := parser.NewSlowLogParser(opt)
	text := "# Time: 2026-07-01T02:59:56.295810Z\n" +
		"# User@Host: h_metadata_rw[h_metadata_rw] @  [10.90.52.125]  Id: 1301119143\n" +
		"# Schema: metadata  Last_errno: 66  Killed: 88\n" +
		"# Query_time: 1.010292  Lock_time: 0.000151  Rows_sent: 1  Rows_examined: 1149878  Rows_affected: 0\n" +
		"# Bytes_sent: 58\n" +
		"# Optimize_cost: {\"rows\": 100}\n" +
		"# explain: id select_type table type possible_keys key key_len ref rows Extra\n" +
		"# explain: 1  SIMPLE  users  ALL  NULL  NULL  NULL  NULL  100000  Using where; Using filesort\n" +
		"SET timestamp=1782874796;\n" +
		"SELECT * FROM users WHERE name LIKE '%abc%' ORDER BY id DESC LIMIT 100;"
	event, err := SlowLogParser.Parser(text)
	if err != nil {
		return
	}
	fmt.Printf("具体SQL:%#v \n", event.Query)
	fmt.Printf("SQL样例:%#v  \n", SlowLogParser.Fingerprint())
	fmt.Printf("SQLID:%#v \n", SlowLogParser.Id())

	fmt.Printf("时间戳:%#v \n", event.Ts)
	fmt.Printf("原生时间:%#v \n", event.Time)
	fmt.Printf("是否管理员命令:%#v \n", event.Admin)
	fmt.Printf("用户:%#v \n", event.User)
	fmt.Printf("主机:%#v \n", event.Host)
	fmt.Printf("数据库:%#v \n", event.Db)
	fmt.Printf("线程ID:%d \n", event.ThreadId)
	fmt.Printf("查询耗时:%v \n", event.QueryTime)
	fmt.Printf("锁耗时:%v \n", event.LockTime)
	fmt.Printf("返回行数:%d \n", event.RowsSent)
	fmt.Printf("扫描行数:%d \n", event.RowsExamined)
	fmt.Printf("影响行数:%d \n", event.RowsAffected)
	fmt.Printf("发送字节:%d \n", event.BytesSent)
	fmt.Printf("最后错误码:%d \n", event.LastErrno)
	fmt.Printf("被杀死:%d \n", event.Killed)
	fmt.Printf("EXPLAIN计划:%#v \n", event.Explain)
	fmt.Printf("优化成本JSON:%s \n", string(event.OptimizeCost))

	// 展开打印时间类指标（Query_time、Lock_time 等）
	for k, v := range event.TimeMetrics {
		fmt.Printf("时间类指标[%s]:%v \n", k, v)
	}
	// 展开打印数字类指标（Rows_sent、Rows_examined 等）
	for k, v := range event.NumberMetrics {
		fmt.Printf("数字类指标[%s]:%d \n", k, v)
	}
	// 展开打印布尔类指标
	for k, v := range event.BoolMetrics {
		fmt.Printf("布尔类指标[%s]:%v \n", k, v)
	}

}
