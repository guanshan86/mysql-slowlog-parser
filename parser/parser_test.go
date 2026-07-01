package parser

import (
	"testing"
	"time"

	"github.com/eopenio/slowlog-parser/log"
)

func mustParse(t *testing.T, text string) *log.Event {
	t.Helper()
	p := NewSlowLogParser(log.Options{DefaultLocation: time.UTC})
	e, err := p.Parser(text)
	if err != nil {
		t.Fatalf("Parser returned error: %v", err)
	}
	if e == nil {
		t.Fatalf("Parser returned nil event")
	}
	return e
}

func TestBasicEvent(t *testing.T) {
	text := "# Time: 2026-07-01T02:59:56.295810Z\n" +
		"# User@Host: h_metadata_rw[h_metadata_rw] @  [10.90.52.125]  Id: 1301119143\n" +
		"# Schema: metadata  Last_errno: 66  Killed: 88\n" +
		"# Query_time: 1.010292  Lock_time: 0.000151  Rows_sent: 1  Rows_examined: 1149878  Rows_affected: 0\n" +
		"# Bytes_sent: 58\n" +
		"SET timestamp=1782874796;\n" +
		"SELECT * FROM t WHERE id = 1;"

	e := mustParse(t, text)

	if e.User != "h_metadata_rw" {
		t.Errorf("User = %q, want h_metadata_rw", e.User)
	}
	if e.Host != "10.90.52.125" {
		t.Errorf("Host = %q, want 10.90.52.125", e.Host)
	}
	if e.ThreadId != 1301119143 {
		t.Errorf("ThreadId = %d, want 1301119143", e.ThreadId)
	}
	if e.Db != "metadata" {
		t.Errorf("Db = %q, want metadata", e.Db)
	}

	// Strongly-typed metrics from # Schema and # Query_time lines.
	if e.LastErrno != 66 {
		t.Errorf("LastErrno = %d, want 66", e.LastErrno)
	}
	if e.Killed != 88 {
		t.Errorf("Killed = %d, want 88", e.Killed)
	}
	if e.QueryTime != 1.010292 {
		t.Errorf("QueryTime = %v, want 1.010292", e.QueryTime)
	}
	if e.LockTime != 0.000151 {
		t.Errorf("LockTime = %v, want 0.000151", e.LockTime)
	}
	if e.RowsSent != 1 {
		t.Errorf("RowsSent = %d, want 1", e.RowsSent)
	}
	if e.RowsExamined != 1149878 {
		t.Errorf("RowsExamined = %d, want 1149878", e.RowsExamined)
	}
	if e.BytesSent != 58 {
		t.Errorf("BytesSent = %d, want 58", e.BytesSent)
	}

	// Native # Time line parsed (UTC).
	wantTime := time.Date(2026, 7, 1, 2, 59, 56, 295810000, time.UTC)
	if !e.Time.Equal(wantTime) {
		t.Errorf("Time = %v, want %v", e.Time, wantTime)
	}
	if e.Ts == "" {
		t.Errorf("Ts should be populated from # Time, got empty")
	}

	// Generic maps still populated.
	if e.NumberMetrics["Killed"] != 88 {
		t.Errorf("NumberMetrics[Killed] = %d, want 88", e.NumberMetrics["Killed"])
	}
	if e.BoolMetric("Nonexistent") != false {
		t.Errorf("BoolMetric of absent key should be false")
	}
}

func TestLegacyTimeFormat(t *testing.T) {
	// Old MySQL < 5.7 format: # Time: 260102 15:04:05
	text := "# Time: 260102 15:04:05\n" +
		"# User@Host: u[u] @  [1.2.3.4]  Id: 7\n" +
		"SET timestamp=1737500000;\n" +
		"SELECT 1;"

	e := mustParse(t, text)
	wantTime := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	if !e.Time.Equal(wantTime) {
		t.Errorf("Time = %v, want %v", e.Time, wantTime)
	}
}

func TestOptimizeCost(t *testing.T) {
	text := "# Time: 2026-07-01T02:59:56.295810Z\n" +
		"# User@Host: u[u] @  [1.2.3.4]  Id: 1\n" +
		"# Optimize_cost: {\"rows\": 100}\n" +
		"SET timestamp=1737500000;\n" +
		"SELECT * FROM t;"

	e := mustParse(t, text)
	if string(e.OptimizeCost) != `{"rows": 100}` {
		t.Errorf("OptimizeCost = %q, want {\"rows\": 100}", string(e.OptimizeCost))
	}
}

func TestExplainAccumulation(t *testing.T) {
	text := "# Time: 2026-07-01T02:59:56.295810Z\n" +
		"# User@Host: u[u] @  [1.2.3.4]  Id: 1\n" +
		"# explain: id select_type\n" +
		"# explain: 1  SIMPLE\n" +
		"SET timestamp=1737500000;\n" +
		"SELECT 1;"

	e := mustParse(t, text)
	if e.Explain == "" {
		t.Fatalf("Explain should be accumulated, got empty")
	}
	// Both explain lines joined by newline.
	wantLines := 2
	if got := countLines(e.Explain); got != wantLines {
		t.Errorf("Explain has %d lines, want %d (got %q)", got, wantLines, e.Explain)
	}
}

func TestPerconaBooleanMetrics(t *testing.T) {
	text := "# Time: 2026-07-01T02:59:56.295810Z\n" +
		"# User@Host: u[u] @  [1.2.3.4]  Id: 1\n" +
		"# Filesort: Yes  Full_scan: No  Tmp_table: Yes\n" +
		"# Query_time: 0.5  Lock_time: 0.0  Rows_sent: 0  Rows_examined: 0\n" +
		"SET timestamp=1737500000;\n" +
		"SELECT * FROM t;"

	e := mustParse(t, text)
	if !e.Filesort {
		t.Errorf("Filesort should be true")
	}
	if e.FullScan {
		t.Errorf("FullScan should be false")
	}
	if !e.TmpTable {
		t.Errorf("TmpTable should be true")
	}
	// Map mirror.
	if !e.BoolMetrics["Filesort"] {
		t.Errorf("BoolMetrics[Filesort] should be true")
	}
}

func TestPerconaNumberMetrics(t *testing.T) {
	text := "# Time: 2026-07-01T02:59:56.295810Z\n" +
		"# User@Host: u[u] @  [1.2.3.4]  Id: 1\n" +
		"# Rows_read: 42  Merge_passes: 3\n" +
		"SET timestamp=1737500000;\n" +
		"SELECT * FROM t;"

	e := mustParse(t, text)
	if e.RowsRead != 42 {
		t.Errorf("RowsRead = %d, want 42", e.RowsRead)
	}
	if e.MergePasses != 3 {
		t.Errorf("MergePasses = %d, want 3", e.MergePasses)
	}
}

func TestFingerprintAndId(t *testing.T) {
	text := "# Time: 2026-07-01T02:59:56.295810Z\n" +
		"# User@Host: u[u] @  [1.2.3.4]  Id: 1\n" +
		"SET timestamp=1737500000;\n" +
		"SELECT * FROM t WHERE name = 'Bob' AND age = 25;"

	p := NewSlowLogParser(log.Options{DefaultLocation: time.UTC})
	if _, err := p.Parser(text); err != nil {
		t.Fatalf("Parser error: %v", err)
	}
	fp := p.Fingerprint()
	// values like 'Bob' and 25 should be replaced with ?
	if !contains(fp, "?") {
		t.Errorf("Fingerprint should contain ? placeholders, got %q", fp)
	}
	id := p.Id()
	if len(id) != 16 {
		t.Errorf("Id length = %d, want 16 (got %q)", len(id), id)
	}
}

// TestParseError ensures a malformed time still yields an error from Parser
// rather than being silently swallowed (P3: error exposure).
func TestParseErrorExposed(t *testing.T) {
	// Unparseable timestamp that matches neither known format.
	text := "# Time: not-a-date\n" +
		"# User@Host: u[u] @  [1.2.3.4]  Id: 1\n" +
		"SET timestamp=1737500000;\n" +
		"SELECT 1;"

	p := NewSlowLogParser(log.Options{DefaultLocation: time.UTC})
	_, err := p.Parser(text)
	if err == nil {
		t.Fatalf("expected error for unparseable # Time, got nil")
	}
}

func countLines(s string) int {
	n := 0
	for _, c := range s {
		if c == '\n' {
			n++
		}
	}
	if len(s) > 0 && s[len(s)-1] != '\n' {
		n++
	}
	return n
}

func contains(s, sub string) bool {
	return indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}