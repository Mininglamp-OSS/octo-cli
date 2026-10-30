package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RecordedRPCID preserves the JSON-RPC ID type and original number spelling.
type RecordedRPCID struct {
	Type   string  `json:"type"`
	Value  *string `json:"value,omitempty"`
	Lexeme string  `json:"lexeme,omitempty"`
}

// RequestRecord deliberately has no result, error, status, or completion API.
type RequestRecord struct {
	ReceivedAt         string                     `json:"received_at"`
	RequestID          string                     `json:"request_id"`
	BotUID             string                     `json:"bot_uid"`
	RPCID              RecordedRPCID              `json:"rpc_id"`
	Tool               string                     `json:"tool"`
	OperationID        string                     `json:"operation_id,omitempty"`
	DeclaredSource     *DeclaredSource            `json:"declared_source_im,omitempty"`
	RequestedArguments map[string]json.RawMessage `json:"requested_arguments"`
}

type logItem struct {
	payload []byte
	ctx     context.Context
	ack     chan error
}

// RequestLog has one writer and bounded ownership of encoded queue payloads.
// A timed-out caller cannot be resumed by a later fsync acknowledgement.
type RequestLog struct {
	mu                  sync.Mutex
	queue               chan logItem
	done                chan struct{}
	stopping            chan struct{}
	closed, failed      bool
	queuedBytes         int
	dir, lock, file     *os.File
	name, day           string
	size, total         int64
	quota, segmentLimit int64
	syncFile            func(*os.File) error
	writeFile           func(*os.File, []byte) (int, error)
}

func randomUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}

type quotaExceeded struct{}

func (quotaExceeded) Error() string { return "request storage quota" }

func logUnavailable() error { return admissionFailure(-32004, "REQUEST_LOG_UNAVAILABLE") }

// OpenRequestLog locks and recovers an operator-owned request directory.
func OpenRequestLog(path string) (*RequestLog, error) {
	dir, err := secureDirectory(path, true)
	if err != nil {
		return nil, errors.New("REQUEST_LOG_STARTUP_FAILED")
	}
	l := &RequestLog{dir: dir, queue: make(chan logItem, 128), done: make(chan struct{}), stopping: make(chan struct{}), quota: 2 << 30, segmentLimit: 64 << 20, syncFile: func(f *os.File) error { return f.Sync() }, writeFile: func(f *os.File, p []byte) (int, error) { return f.Write(p) }}
	l.lock, err = lockLogDirectory(dir)
	if err == nil {
		err = l.recoverSegments()
	}
	if err == nil {
		err = l.newSegment()
	}
	if err != nil {
		if l.file != nil {
			closePrivateFile(l.file)
		}
		if l.lock != nil {
			closePrivateFile(l.lock)
		}
		closePrivateFile(dir)
		return nil, errors.New("REQUEST_LOG_STARTUP_FAILED")
	}
	go l.run()
	return l, nil
}

// AppendAndSync waits for durable persistence, with a bounded admission deadline.
func (l *RequestLog) AppendAndSync(ctx context.Context, record RequestRecord) error { //nolint:gocritic // copy the immutable request header before encoding and enqueueing
	if l == nil {
		return logUnavailable()
	}
	payload, err := json.Marshal(record)
	if err != nil || len(payload) >= 8192 || !validStoredRecord(payload) {
		return logUnavailable()
	}
	payload = append(payload, '\n')
	wctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	item := logItem{payload: payload, ctx: wctx, ack: make(chan error, 1)}
	l.mu.Lock()
	if l.closed || l.failed || l.queuedBytes+len(payload) > 1<<20 || wctx.Err() != nil {
		l.mu.Unlock()
		return logUnavailable()
	}
	select {
	case l.queue <- item:
		l.queuedBytes += len(payload)
	default:
		l.mu.Unlock()
		return logUnavailable()
	}
	l.mu.Unlock()
	select {
	case err := <-item.ack:
		l.mu.Lock()
		closed := l.closed
		l.mu.Unlock()
		if wctx.Err() != nil || closed {
			return logUnavailable()
		}
		return err
	case <-wctx.Done():
		return logUnavailable()
	case <-l.stopping:
		return logUnavailable()
	}
}

func (l *RequestLog) run() {
	defer close(l.done)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case item, ok := <-l.queue:
			if !ok {
				l.mu.Lock()
				failed := l.failed
				l.mu.Unlock()
				if !failed {
					if l.finishSegment() != nil {
						l.markFailed()
					}
				} else if l.file != nil {
					closePrivateFile(l.file)
				}
				closePrivateFile(l.lock)
				closePrivateFile(l.dir)
				return
			}
			l.mu.Lock()
			l.queuedBytes -= len(item.payload)
			failed := l.failed
			l.mu.Unlock()
			if failed || item.ctx.Err() != nil {
				item.ack <- logUnavailable()
				continue
			}
			err := l.append(item.payload)
			if err != nil {
				if _, quota := err.(quotaExceeded); !quota {
					l.markFailed()
				}
				err = logUnavailable()
			}
			item.ack <- err
		case <-ticker.C:
			if l.expireSegments(time.Now()) != nil {
				l.markFailed()
			}
		}
	}
}
func (l *RequestLog) markFailed() { l.mu.Lock(); l.failed = true; l.mu.Unlock() }

// Close stops admission and waits at most five seconds for the writer.
func (l *RequestLog) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	if !l.closed {
		l.closed = true
		close(l.stopping)
		close(l.queue)
	}
	l.mu.Unlock()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-l.done:
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.failed {
			return errors.New("REQUEST_LOG_CLOSE_FAILED")
		}
		return nil
	case <-timer.C:
		return errors.New("REQUEST_LOG_CLOSE_TIMEOUT")
	}
}

func closePrivateFile(f *os.File) {
	if f != nil {
		_ = f.Close() //nolint:errcheck // cleanup after a primary failure or read-only use must not replace that failure or emit file details
	}
}

func (l *RequestLog) newSegment() error {
	id, err := randomUUID()
	if err != nil {
		return err
	}
	l.name = fmt.Sprintf("request-%019d-%s.open", time.Now().UnixNano(), id)
	l.file, err = logOpenAt(l.dir, l.name, true)
	if err != nil {
		return err
	}
	l.size = 0
	l.day = time.Now().UTC().Format("2006-01-02")
	return l.dir.Sync()
}
func (l *RequestLog) finishSegment() error {
	if l.file == nil {
		return nil
	}
	if err := l.syncFile(l.file); err != nil {
		closePrivateFile(l.file)
		l.file = nil
		return err
	}
	if err := l.file.Close(); err != nil {
		l.file = nil
		return err
	}
	l.file = nil
	id, err := randomUUID()
	if err != nil {
		return err
	}
	name := fmt.Sprintf("request-%019d-%s.jsonl", time.Now().UnixNano(), id)
	if err := renameLogFile(l.dir, l.name, name); err != nil {
		return err
	}
	return l.dir.Sync()
}
func (l *RequestLog) append(payload []byte) error {
	if l.total+int64(len(payload)) > l.quota {
		if err := l.expireSegments(time.Now()); err != nil {
			return err
		}
		if l.total+int64(len(payload)) > l.quota {
			return quotaExceeded{}
		}
	}
	if l.size+int64(len(payload)) > l.segmentLimit || l.day != time.Now().UTC().Format("2006-01-02") {
		if err := l.finishSegment(); err != nil {
			return err
		}
		if err := l.newSegment(); err != nil {
			return err
		}
	}

	n, err := l.writeFile(l.file, payload)
	l.size += int64(n)
	l.total += int64(n)
	if err != nil {
		return err
	}
	if n != len(payload) {
		return io.ErrShortWrite
	}
	return l.syncFile(l.file)
}

var segmentName = regexp.MustCompile(`^(request|quarantine)-([0-9]{19})-([a-f0-9-]{36})\.(open|jsonl)$`)

func (l *RequestLog) entries() ([]os.DirEntry, error) {
	if _, e := l.dir.Seek(0, 0); e != nil {
		return nil, e
	}
	return l.dir.ReadDir(-1)
}
func (l *RequestLog) expireSegments(now time.Time) error {
	entries, err := l.entries()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		m := segmentName.FindStringSubmatch(entry.Name())
		if len(m) != 5 || m[4] != "jsonl" {
			continue
		}
		n, parseErr := strconv.ParseInt(m[2], 10, 64)
		if parseErr != nil {
			return parseErr
		}
		if now.Sub(time.Unix(0, n)) < 7*24*time.Hour {
			continue
		}
		f, e := logOpenAt(l.dir, entry.Name(), false)
		if e != nil {
			return e
		}
		st, e := f.Stat()
		closePrivateFile(f)
		if e != nil {
			return e
		}
		if e := removeLogFile(l.dir, entry.Name()); e != nil {
			return e
		}
		l.total -= st.Size()
		if e := l.dir.Sync(); e != nil {
			return e
		}
	}
	return nil
}

func (l *RequestLog) recoverSegments() error { //nolint:gocyclo // recovery distinguishes complete records, partial tails, quarantine and corruption
	entries, err := l.entries()
	if err != nil {
		return err
	}
	openCount := 0
	for _, entry := range entries {
		name := entry.Name()
		if name == ".lock" {
			continue
		}
		m := segmentName.FindStringSubmatch(name)
		if m == nil {
			return errors.New("unknown storage entry")
		}
		f, e := logOpenAt(l.dir, name, false)
		if e != nil {
			return e
		}
		st, e := f.Stat()
		if e != nil {
			closePrivateFile(f)
			return e
		}
		l.total += st.Size()
		if m[1] == "quarantine" {
			closePrivateFile(f)
			if m[4] != "jsonl" {
				return errors.New("invalid quarantine")
			}
			continue
		}
		active := m[4] == "open"
		if active {
			openCount++
			if openCount > 1 {
				closePrivateFile(f)
				return errors.New("multiple active segments")
			}
		}
		reader := bufio.NewReaderSize(f, 8193)
		offset := int64(0)
		for {
			line, re := reader.ReadSlice('\n')
			if len(line) > 8192 || errors.Is(re, bufio.ErrBufferFull) {
				closePrivateFile(f)
				return errors.New("oversized record")
			}
			if re != nil {
				if !errors.Is(re, io.EOF) {
					closePrivateFile(f)
					return re
				}
				if len(line) > 0 {
					if !active {
						closePrivateFile(f)
						return errors.New("incomplete closed segment")
					}
					if e = l.quarantine(f); e != nil {
						closePrivateFile(f)
						return e
					}
					if e = f.Truncate(offset); e == nil {
						e = f.Sync()
					}
					if e != nil {
						closePrivateFile(f)
						return e
					}
					l.total -= st.Size() - offset
				}
				break
			}
			if !validStoredRecord(bytes.TrimSuffix(line, []byte{'\n'})) {
				closePrivateFile(f)
				return errors.New("invalid stored record")
			}
			offset += int64(len(line))
		}
		closePrivateFile(f)
		if active {
			id, e := randomUUID()
			if e != nil {
				return e
			}
			closed := fmt.Sprintf("request-%019d-%s.jsonl", time.Now().UnixNano(), id)
			if e := renameLogFile(l.dir, name, closed); e != nil {
				return e
			}
			if e := l.dir.Sync(); e != nil {
				return e
			}
		}
	}
	if err := l.expireSegments(time.Now()); err != nil {
		return err
	}
	if l.total > l.quota {
		return errors.New("storage quota")
	}
	return nil
}
func (l *RequestLog) quarantine(source *os.File) error {
	st, err := source.Stat()
	if err != nil || l.total+st.Size() > l.quota {
		return errors.New("quarantine quota")
	}
	id, err := randomUUID()
	if err != nil {
		return err
	}
	name := fmt.Sprintf("quarantine-%019d-%s.jsonl", time.Now().UnixNano(), id)
	dst, err := logOpenAt(l.dir, name, true)
	if err != nil {
		return err
	}
	defer closePrivateFile(dst)
	if _, err = source.Seek(0, 0); err != nil {
		return err
	}
	n, err := io.Copy(dst, source)
	l.total += n
	if err != nil {
		return err
	}
	if err := dst.Sync(); err != nil {
		return err
	}
	return l.dir.Sync()
}

func validStoredRecord(raw []byte) bool {
	if len(raw) > 8191 {
		return false
	}
	root, err := strictObject(raw)
	if err != nil {
		return false
	}
	for _, k := range []string{"received_at", "request_id", "bot_uid", "rpc_id", "tool", "requested_arguments"} {
		if _, ok := root[k]; !ok {
			return false
		}
	}
	for k := range root {
		switch k {
		case "received_at", "request_id", "bot_uid", "rpc_id", "tool", "operation_id", "declared_source_im", "requested_arguments":
		default:
			return false
		}
	}
	var r RequestRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&r) != nil || r.RequestedArguments == nil || !validID(r.BotUID, 40) {
		return false
	}
	if _, err = time.Parse(time.RFC3339Nano, r.ReceivedAt); err != nil || !strings.HasSuffix(r.ReceivedAt, "Z") {
		return false
	}
	return validRecordUUID(r.RequestID) && validRecordedRPCID(root["rpc_id"], r.RPCID) && validRecordedSource(root, r.DeclaredSource) && validRecordedArguments(r, root["requested_arguments"])
}

func validRecordUUID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	b, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil && len(b) == 16
}

func validRecordedRPCID(raw []byte, id RecordedRPCID) bool {
	switch id.Type {
	case "null":
		_, err := strictObject(raw, "type")
		return err == nil && id.Value == nil && id.Lexeme == ""
	case "string":
		_, err := strictObject(raw, "type", "value")
		return err == nil && id.Value != nil && len(*id.Value) <= 256 && id.Lexeme == ""
	case "number":
		_, err := strictObject(raw, "type", "lexeme")
		return err == nil && id.Value == nil && id.Lexeme != "" && validRPCID([]byte(id.Lexeme)) && id.Lexeme != "null" && !strings.HasPrefix(id.Lexeme, "\"")
	default:
		return false
	}
}

func validRecordedSource(root map[string]json.RawMessage, source *DeclaredSource) bool {
	raw, present := root["declared_source_im"]
	if source == nil {
		return !present
	}
	if _, err := strictObject(raw, "version", "sender", "channel", "channel_type", "space_id", "message_id"); err != nil {
		return false
	}
	return source.Version == 1 && validID(source.Sender, 128) && validID(source.Channel, 256) && validID(source.SpaceID, 40) && positiveDecimal(source.MessageID, 63) && (source.ChannelType == 1 || source.ChannelType == 2 || source.ChannelType == 5)
}

func validRecordedArguments(r RequestRecord, raw []byte) bool { //nolint:gocritic,gocyclo // immutable request snapshots are copied at this boundary; exhaustive tool-specific closed field sets
	if r.OperationID != "" && !mcpExposed(r.OperationID) {
		return false
	}
	args, err := strictObject(raw)
	if err != nil || len(raw) > 2048 {
		return false
	}
	allowed := map[string]bool{}
	switch r.Tool {
	case "search_ops":
		allowed["domain"] = true
	case "describe_op":
		allowed["operation_id"] = true
	case "get_skill":
		for _, k := range []string{"domain", "intent", "operation_id", "depth"} {
			allowed[k] = true
		}
	case "call_op", "execute":
		for _, k := range requestArgumentFields[r.OperationID] {
			allowed[k] = true
		}
		if r.Tool == "execute" {
			allowed["tool_options"] = true
		}
	case "get_secret_download_url":
	default:
		return false
	}
	for k, v := range args {
		if !allowed[k] || !validStoredArgument(k, v, r.OperationID) {
			return false
		}
	}
	return true
}

func validStoredArgument(key string, value json.RawMessage, operation string) bool { //nolint:gocyclo // exhaustive permitted log value types and enums
	if key == "tool_options" {
		o, err := strictObject(value, "dry_run")
		return err == nil && (string(o["dry_run"]) == "true" || string(o["dry_run"]) == "false")
	}
	if len(value) == 0 || string(value) == "null" || value[0] == '{' {
		return false
	}
	if string(value) == `"***REDACTED***"` {
		return true
	}
	switch key {
	case "domain", "operation_id", "intent", "depth":
		var s string
		if json.Unmarshal(value, &s) != nil || !validID(s, 128) {
			return false
		}
		switch key {
		case "operation_id":
			return s == operation
		case "intent":
			return s == "search" || s == "describe"
		case "depth":
			return s == "summary" || s == "full"
		}
		return true
	case "message_ids":
		var ids []string
		if json.Unmarshal(value, &ids) != nil || len(ids) > 100 {
			return false
		}
		for _, s := range ids {
			if s != "***REDACTED***" && !positiveDecimal(s, 63) {
				return false
			}
		}
		return true
	default:
		typ := "string"
		switch key {
		case "channel_type", "start_message_seq", "end_message_seq", "limit", "pull_mode", "page_index", "page_size":
			typ = "integer"
		}
		return validLoggedArgument(key, value, typ)
	}
}
