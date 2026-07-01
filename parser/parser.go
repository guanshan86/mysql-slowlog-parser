package parser

import (
	"bufio"
	"encoding/json"
	stdlog "log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/guanshan86/mysql-slowlog-parser/log"
)

// Regular expressions to match important lines in slow log.
var (
	timeRe        = regexp.MustCompile(`Time: (\S+\s{1,2}\S+)`)
	timeNewRe     = regexp.MustCompile(`Time:\s+(\d{4}-\d{2}-\d{2}\S+)`)
	userRe        = regexp.MustCompile(`User@Host: ([^\[]+|\[[^[]+\]).*?@ (\S*) \[(.*)\]`)
	idRe          = regexp.MustCompile(`\bId:\s*(\d+)`)
	schema        = regexp.MustCompile(`Schema: +(.*?) +Last_errno:`)
	headerRe      = regexp.MustCompile(`^#\s+[A-Z]`)
	metricsRe     = regexp.MustCompile(`(\w+): (\S+|\z)`)
	adminRe       = regexp.MustCompile(`command: (.+)`)
	setRe         = regexp.MustCompile(`^SET (?:last_insert_id|insert_id|timestamp)`)
	//setTs     = regexp.MustCompile(`^SET timestamp=\d{10}`)
	setTs         = regexp.MustCompile(`^SET timestamp=(\d)+`)
	useRe         = regexp.MustCompile(`^(?i)use `)
	optimizeCostRe = regexp.MustCompile(`Optimize_cost:\s*(\{.*\})`)
)

// parseTime parses a # Time line value in either the legacy "060102 15:04:05"
// format or the RFC3339Nano-style "2026-07-01T02:59:56.295810Z" format. It
// returns the zero time and a non-nil error if the value cannot be parsed in
// either form.
func parseTime(value string, loc *time.Location) (time.Time, error) {
	// Newer MySQL/Percona format: 2026-07-01T02:59:56.295810Z
	if t, err := time.ParseInLocation(time.RFC3339Nano, value, loc); err == nil {
		return t, nil
	}
	// Legacy MySQL format: 060102 15:04:05
	if t, err := time.ParseInLocation("060102 15:04:05", value, loc); err == nil {
		return t, nil
	}
	return time.Time{}, &time.ParseError{Layout: "060102 15:04:05 / RFC3339Nano", Value: value}
}

// A SlowLogParser parses a MySQL slow log. It implements the LogParser interface.
type SlowLogParser struct {
	text string
	opt  log.Options
	// --
	inHeader    bool
	inQuery     bool
	headerLines uint
	queryLines  uint64
	event       *log.Event
	err         error // first parse error encountered, if any (exposed via Parser)
}

// NewSlowLogParser returns a new SlowLogParser that reads from the open file.
func NewSlowLogParser(opt log.Options) *SlowLogParser {
	if opt.DefaultLocation == nil {
		// Old MySQL format assumes time is taken from SYSTEM.
		opt.DefaultLocation = time.Local
	}
	p := &SlowLogParser{
		opt: opt,
		// --
		inHeader:    false,
		inQuery:     false,
		headerLines: 0,
		queryLines:  0,
		event:       log.NewEvent(),
		err:         nil,
	}
	return p
}

// setErr records the first parse error (subsequent errors are ignored so the
// caller sees the root cause). Failed sub-pieces fall back to their zero value
// rather than aborting the whole event.
func (p *SlowLogParser) setErr(err error) {
	if p.err == nil {
		p.err = err
	}
}

// logf logs with configured logger.
func (p *SlowLogParser) logf(format string, v ...interface{}) {
	if !p.opt.Debug {
		return
	}
	if p.opt.Debugf != nil {
		p.opt.Debugf(format, v...)
		return
	}
	stdlog.Printf(format, v...)
}

func (p *SlowLogParser) Parser(text string) (*log.Event, error) {

	for _, v := range strings.Split(text, "\n") {
		r := bufio.NewReader(strings.NewReader(v))

		line, _ := r.ReadString('\n')
		lineLen := uint64(len(line))
		if lineLen >= 20 && ((line[0] == '/' && line[lineLen-6:lineLen] == "with:\n") ||
			(line[0:5] == "Time ") ||
			(line[0:4] == "Tcp ") ||
			(line[0:4] == "TCP ")) {
			p.logf("meta")
		}
		// PMM-1834: Filter out empty comments; accumulate MariaDB "# explain:" lines
		// instead of dropping them, so the EXPLAIN plan is preserved on the event.
		if line == "#\n" {
			p.logf("# empty")
		} else if strings.HasPrefix(line, "# explain:") {
			p.logf("# explain")
			if p.event.Explain != "" {
				p.event.Explain += "\n"
			}
			p.event.Explain += strings.TrimPrefix(line, "# explain:")
			continue
		}

		// Remove \n.
		//line = line[0 : lineLen-1]
		// No Need Remove \n
		line = line[0:lineLen]
		if p.inHeader {
			p.parseHeader(line)
		} else if p.inQuery {
			p.parseQuery(line)
		} else if headerRe.MatchString(line) {
			p.inHeader = true
			p.inQuery = false
			p.parseHeader(line)
		}
	}

	return p.event, p.err
}

// --------------------------------------------------------------------------

func (p *SlowLogParser) parseHeader(line string) {
	p.logf("header")
	if !headerRe.MatchString(line) {
		p.inHeader = false
		p.inQuery = true
		p.parseQuery(line)
		return
	}

	p.headerLines++

	if strings.HasPrefix(line, "# Time") {
		p.logf("time")
		m := timeRe.FindStringSubmatch(line)
		if len(m) != 2 {
			m = timeNewRe.FindStringSubmatch(line)
		}
		if len(m) == 2 {
			// Parse the real event timestamp (# Time line). Previously this was
			// commented out, so the raw timestamp/timezone/ms were lost and Ts
			// only got filled later by "SET timestamp=". Parse into event.Time
			// and also populate Ts as a string for backward compatibility.
			if t, err := parseTime(m[1], p.opt.DefaultLocation); err == nil {
				p.event.Time = t
				if p.event.Ts == "" {
					p.event.Ts = t.Format("2006-01-02 15:04:05")
				}
			} else {
				p.setErr(err)
			}
		} else {
			// The line is clearly a "# Time:" header but its value matched neither
			// the legacy nor the RFC3339 pattern. Surface this as a parse error
			// rather than silently dropping the timestamp.
			p.setErr(&time.ParseError{Layout: "060102 15:04:05 / RFC3339Nano", Value: strings.TrimSpace(strings.TrimPrefix(line, "# Time"))})
		}
		// "bad format" fallback: some older logs put User@Host on the same line
		// as "# Time". Only honor it if User/Host haven't been set yet, to avoid
		// clobbering the canonical "# User" line parsed below.
		if p.event.User == "" && userRe.MatchString(line) {
			p.logf("user (bad format)")
			mu := userRe.FindStringSubmatch(line)
			if len(mu) >= 4 {
				p.event.User = mu[1]
				p.event.Host = mu[3]
			}
		}
	} else if strings.HasPrefix(line, "# Optimize_cost") {
		p.logf("optimize_cost")
		// MySQL 8.0 emits "# Optimize_cost: {json}". Capture the JSON payload.
		if m := optimizeCostRe.FindStringSubmatch(line); len(m) == 2 {
			p.event.OptimizeCost = json.RawMessage(m[1])
		}
	} else if strings.HasPrefix(line, "# User") {
		p.logf("user")
		m := userRe.FindStringSubmatch(line)
		if len(m) < 3 {
			return
		}
		p.event.User = m[1]
		p.event.Host = m[3]

		// Parse the trailing "Id: <number>" on the User@Host line, e.g.
		// "# User@Host: h_metadata_rw[h_metadata_rw] @  [10.90.52.125]  Id: 1301119143"
		// This line takes the "# User" branch above and never reaches the generic
		// metricsRe parsing in the else branch, so we parse Id explicitly here.
		if idMatch := idRe.FindStringSubmatch(line); len(idMatch) == 2 {
			if id, err := strconv.ParseUint(idMatch[1], 10, 64); err == nil {
				p.event.ThreadId = id
			} else {
				p.setErr(err)
			}
		}
	} else if strings.HasPrefix(line, "# admin") {
		p.parseAdmin(line)
	} else {
		p.logf("metrics")
		submatch := schema.FindStringSubmatch(line)
		if len(submatch) == 2 {
			p.event.Db = submatch[1]
		}

		m := metricsRe.FindAllStringSubmatch(line, -1)
		for _, smv := range m {
			// [String, Metric, Value], e.g. ["Query_time: 2", "Query_time", "2"]
			if strings.HasSuffix(smv[1], "_time") || strings.HasSuffix(smv[1], "_wait") {
				// microsecond value
				val, err := strconv.ParseFloat(smv[2], 64)
				if err != nil {
					p.setErr(err)
				}
				p.event.TimeMetrics[smv[1]] = val
				p.copyTimeMetric(smv[1], val)
			} else if smv[2] == "Yes" || smv[2] == "No" {
				// boolean value
				b := smv[2] == "Yes"
				p.event.BoolMetrics[smv[1]] = b
				p.copyBoolMetric(smv[1], b)
			} else if smv[1] == "Schema" {
				p.event.Db = smv[2]
			} else if smv[1] == "Log_slow_rate_type" {
				p.event.RateType = smv[2]
			} else if smv[1] == "Log_slow_rate_limit" {
				val, err := strconv.ParseUint(smv[2], 10, 64)
				if err != nil {
					p.setErr(err)
				}
				p.event.RateLimit = uint(val)
			} else {
				// integer value
				val, err := strconv.ParseUint(smv[2], 10, 64)
				if err != nil {
					p.setErr(err)
				}
				p.event.NumberMetrics[smv[1]] = val
				p.copyNumberMetric(smv[1], val)
			}
		}
	}
}

// copyTimeMetric mirrors a *_time metric into its strongly-typed Event field.
func (p *SlowLogParser) copyTimeMetric(name string, val float64) {
	switch name {
	case "Query_time":
		p.event.QueryTime = val
	case "Lock_time":
		p.event.LockTime = val
	}
}

// copyNumberMetric mirrors a number metric into its strongly-typed Event field.
func (p *SlowLogParser) copyNumberMetric(name string, val uint64) {
	switch name {
	case "Rows_sent":
		p.event.RowsSent = val
	case "Rows_examined":
		p.event.RowsExamined = val
	case "Rows_affected":
		p.event.RowsAffected = val
	case "Bytes_sent":
		p.event.BytesSent = val
	case "Last_errno":
		p.event.LastErrno = val
	case "Killed":
		p.event.Killed = val
	case "Rows_read":
		p.event.RowsRead = val
	case "Merge_passes":
		p.event.MergePasses = val
	}
}

// copyBoolMetric mirrors a Yes/No metric into its strongly-typed Event field.
func (p *SlowLogParser) copyBoolMetric(name string, val bool) {
	switch name {
	case "Full_scan":
		p.event.FullScan = val
	case "Filesort":
		p.event.Filesort = val
	case "Tmp_table":
		p.event.TmpTable = val
	}
}

func (p *SlowLogParser) parseQuery(line string) {
	p.logf("query")

	if strings.HasPrefix(line, "# admin") {
		p.parseAdmin(line)
		return
	} else if headerRe.MatchString(line) {
		p.logf("next event")
		p.inHeader = true
		p.inQuery = false
		// todo
		p.parseEvent(true, false)
		p.parseHeader(line)
		return
	}

	isUse := useRe.FindString(line)
	if p.queryLines == 0 && isUse != "" {
		p.logf("use db")
		db := strings.TrimPrefix(line, isUse)
		db = strings.TrimRight(db, ";")
		db = strings.Trim(db, "`")
		p.event.Db = db
		p.event.Query = line
	} else if setTs.MatchString(line) {
		m := setTs.FindString(line)
		tsStr := strings.Split(m, "=")
		p.logf("len = %v", len(tsStr))
		if len(tsStr) != 2 {
			p.event.Ts = time.Now().Format("2006-01-02 15:04:15")
		}
		p.logf("tsStr = %s", tsStr[1])
		ts, _ := strconv.ParseInt(tsStr[1], 10, 64)
		p.event.Ts = time.Unix(ts, 0).Format("2006-01-02 15:04:05")
	} else {
		p.logf("query")
		if p.queryLines > 0 {
			p.event.Query += "\n" + line
		} else {
			p.event.Query = line
		}
		p.queryLines++
	}
}

func (p *SlowLogParser) parseAdmin(line string) {
	p.logf("admin")
	p.event.Admin = true
	m := adminRe.FindStringSubmatch(line)
	p.event.Query = m[1]
	p.event.Query = strings.TrimSuffix(p.event.Query, ";") // makes FilterAdminCommand work

	// admin commands should be the last line of the event.
	if filtered := p.opt.FilterAdminCommand[p.event.Query]; !filtered {
		p.logf("not filtered")
		// todo
		p.parseEvent(false, false)
	} else {
		p.inHeader = false
		p.inQuery = false
	}
}

func (p *SlowLogParser) parseEvent(inHeader bool, inQuery bool) error {

	// Make a new event and reset our metadata.
	defer func() {
		p.event = log.NewEvent()
		p.headerLines = 0
		p.queryLines = 0
		p.inHeader = inHeader
		p.inQuery = inQuery
	}()

	// Clean up the event.
	p.event.Db = strings.TrimSuffix(p.event.Db, ";\n")
	p.event.Query = strings.TrimSuffix(p.event.Query, ";")

	return nil
}
