package log

import (
	"encoding/json"
	"time"
)

type Event struct {
	Offset        uint64 // byte offset in file at which event starts
	OffsetEnd     uint64 // byte offset in file at which event ends
	Ts            string // timestamp of event (string form, from SET timestamp= or # Time)
	Time          time.Time // parsed timestamp from # Time line (zero if absent)
	Admin         bool   // true if Query is admin command
	Query         string // SQL query or admin command
	Explain       string // accumulated "# explain:" lines, if any
	OptimizeCost  json.RawMessage // MySQL 8.0 "# Optimize_cost: {json}" payload
	User          string
	Host          string
	Db            string
	Server        string
	ThreadId      uint64 // connection/thread Id from User@Host line
	// Strongly-typed copies of frequently-used metrics. The generic maps below
	// are still populated for completeness / unknown metrics.
	QueryTime     float64
	LockTime      float64
	RowsSent      uint64
	RowsExamined  uint64
	RowsAffected  uint64
	BytesSent     uint64
	LastErrno     uint64
	Killed        uint64
	RowsRead      uint64 // Percona
	MergePasses   uint64 // Percona
	FullScan      bool   // Percona Yes/No
	Filesort      bool   // Percona Yes/No
	TmpTable      bool   // Percona Yes/No
	LabelsKey     []string
	LabelsValue   []string
	TimeMetrics   map[string]float64 // *_time and *_wait metrics
	NumberMetrics map[string]uint64  // most metrics
	BoolMetrics   map[string]bool    // yes/no metrics
	RateType      string             // Percona Server rate limit type
	RateLimit     uint               // Percona Server rate limit value
}

// NewEvent returns a new Event with initialized metric maps.
func NewEvent() *Event {
	event := new(Event)
	event.TimeMetrics = make(map[string]float64)
	event.NumberMetrics = make(map[string]uint64)
	event.BoolMetrics = make(map[string]bool)
	return event
}

// Options encapsulate common options for making a new LogParser.
type Options struct {
	StartOffset           uint64                                // byte offset in file at which to start parsing
	FilterAdminCommand    map[string]bool                       // admin commands to ignore
	Debug                 bool                                  // print trace info to STDERR with standard library logger
	Debugf                func(format string, v ...interface{}) // use this function for logging instead of log.Printf (Debug still should be true)
	DefaultLocation       *time.Location                        // DefaultLocation to assume for logs in MySQL < 5.7 format.
	ReplaceNumbersInWords bool
}

// TimeMetric returns a *_time / *_wait metric from the generic map, or 0 if
// absent. Prefer the strongly-typed Event.QueryTime etc. fields for known
// metrics; this is for less-common or unknown metrics.
func (e *Event) TimeMetric(name string) float64 {
	return e.TimeMetrics[name]
}

// NumberMetric returns a numeric metric from the generic map, or 0 if absent.
func (e *Event) NumberMetric(name string) uint64 {
	return e.NumberMetrics[name]
}

// BoolMetric returns a Yes/No metric from the generic map, or false if absent.
func (e *Event) BoolMetric(name string) bool {
	return e.BoolMetrics[name]
}
