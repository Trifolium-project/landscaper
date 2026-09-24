/*
Copyright © 2022 Aleksandr Ivanov <shamrockspb@gmail.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

//Package auditlog writes a JSON Lines record of everything a landscaper run
//did: the command and its parameters, every call to the tenant with the
//tenant's answer, and the outcome of each artifact.
//
//It is a leaf package - it imports nothing from the project, so cpiclient,
//landscape and cmd can all use it. It deliberately does not import "log":
//the standard logger holds its own mutex across the Write it makes to the
//tee below, so logging from inside a write would deadlock the process.
package auditlog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

//Headers that must never reach the file. Basic authentication is reversible,
//so Authorization carries the tenant password in all but name.
var redactedHeaders = map[string]bool{
	"Authorization": true,
	"X-Csrf-Token":  true,
	"Cookie":        true,
	"Set-Cookie":    true,
}

//Flags that must never reach the file, whatever a command calls them. No
//command defines one today, this is here so that adding one is not a leak.
var redactedFlags = map[string]bool{
	"password": true,
	"secret":   true,
	"token":    true,
}

//Value written instead of anything secret
const redacted = "<redacted>"

//How much of a request body is inspected to find the artifact payload. The
//marshalled artifact puts ArtifactContent last, after four short strings, so
//the key is always within the first few hundred bytes and the multi megabyte
//tail is never touched.
const requestScanBytes = 4 << 10

//Media types that are recorded as text even though they are not text/*
var textMediaTypes = map[string]bool{
	"application/json":                  true,
	"application/xml":                   true,
	"application/atom+xml":              true,
	"application/javascript":            true,
	"application/x-www-form-urlencoded": true,
}

//Logger writes records to one file. A nil *Logger is a working, disabled
//logger: every method returns immediately, so callers never guard the call.
type Logger struct {
	mutex sync.Mutex
	file  *os.File
	path  string
	start time.Time
	//Set once the run has been reported as finished, so that a fatal
	//detected by the tee does not produce a second end record
	ended bool
	//Reported once if the file becomes unwritable, straight to stderr,
	//because this package cannot use the standard logger
	warnOnce sync.Once
}

//Body is what was sent or received, or a note about why it was left out
type Body struct {
	Bytes  int64  `json:"bytes"`
	Text   string `json:"text,omitempty"`
	Binary bool   `json:"binary,omitempty"`
	Note   string `json:"note,omitempty"`
}

//New creates the log directory and opens a file named after the current time.
//The file is opened append only, because the name has second granularity and
//two runs started in the same second must not truncate each other. The mode is
//0600, the file holds complete tenant responses.
func New(dir string) (*Logger, error) {

	if dir == "" {
		return nil, fmt.Errorf("log directory is not set")
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("unable to create log directory %s: %s", dir, err)
	}

	path := filepath.Join(dir, "landscaper-"+time.Now().Format("20060102-150405")+".log")

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("unable to open log file %s: %s", path, err)
	}

	return &Logger{file: file, path: path, start: time.Now()}, nil
}

//Path is the file being written, for the message that tells the user where it is
func (logger *Logger) Path() string {
	if logger == nil {
		return ""
	}
	return logger.path
}

//Close releases the file. Records are written unbuffered, so a run that exits
//without reaching Close loses nothing.
func (logger *Logger) Close() error {
	if logger == nil || logger.file == nil {
		return nil
	}
	return logger.file.Close()
}

//write marshals one record and writes it as one line. It never returns an
//error and never logs: a broken audit file must not change what the command
//does, and reporting through the standard logger would deadlock the tee.
func (logger *Logger) write(record interface{}) {

	if logger == nil || logger.file == nil {
		return
	}

	data, err := json.Marshal(record)
	if err != nil {
		return
	}
	data = append(data, '\n')

	logger.mutex.Lock()
	defer logger.mutex.Unlock()

	//os.File.Write does not loop, and a record carrying a full response body
	//can be large enough for a short write to matter. A partial line would
	//corrupt the stream for every later reader.
	for len(data) > 0 {
		written, err := logger.file.Write(data)
		if written > 0 {
			data = data[written:]
		}
		if err != nil {
			logger.warnOnce.Do(func() {
				fmt.Fprintf(os.Stderr, "Unable to write the audit log %s: %s\n", logger.path, err)
			})
			return
		}
	}
}

func timestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}

//RunStart records the command and the parameters it was given
func (logger *Logger) RunStart(command string, environment string, flags map[string]string) {
	if logger == nil {
		return
	}
	logger.write(map[string]interface{}{
		"ts":      timestamp(),
		"type":    "run",
		"phase":   "start",
		"command": command,
		"env":     environment,
		"flags":   flags,
	})
}

//RunEnd records how the run finished. Only the first call is written, so a
//fatal spotted by the tee does not race the normal end of a command.
func (logger *Logger) RunEnd(status string, detail string) {

	if logger == nil {
		return
	}

	logger.mutex.Lock()
	if logger.ended {
		logger.mutex.Unlock()
		return
	}
	logger.ended = true
	logger.mutex.Unlock()

	record := map[string]interface{}{
		"ts":          timestamp(),
		"type":        "run",
		"phase":       "end",
		"status":      status,
		"duration_ms": time.Since(logger.start).Milliseconds(),
	}
	if detail != "" {
		record["detail"] = detail
	}

	logger.write(record)
}

//Item records the outcome of one artifact or package, using the same status
//vocabulary the command prints in its table
func (logger *Logger) Item(fields map[string]interface{}) {

	if logger == nil || fields == nil {
		return
	}

	record := map[string]interface{}{
		"ts":   timestamp(),
		"type": "item",
	}
	for key, value := range fields {
		record[key] = value
	}

	logger.write(record)
}

//Message records a line of ordinary program output. Used by the tee.
func (logger *Logger) Message(level string, text string) {
	if logger == nil {
		return
	}
	logger.write(map[string]interface{}{
		"ts":    timestamp(),
		"type":  "log",
		"level": level,
		"msg":   text,
	})
}

//HTTPCall accumulates one request and its answer. A nil *HTTPCall is valid and
//does nothing, which is what a disabled logger returns.
type HTTPCall struct {
	logger  *Logger
	started time.Time

	method          string
	url             string
	requestHeaders  map[string]string
	requestBody     *Body
	status          int
	responseHeaders map[string]string
	responseBody    *Body
	failure         string
}

//StartHTTP begins recording a request. The request must already carry its
//headers, so the record shows what was really sent - with the secrets removed.
func (logger *Logger) StartHTTP(request *http.Request) *HTTPCall {

	if logger == nil || request == nil {
		return nil
	}

	call := &HTTPCall{
		logger:         logger,
		started:        time.Now(),
		method:         request.Method,
		requestHeaders: RedactHeaders(request.Header),
		requestBody:    CaptureRequestBody(request),
	}
	if request.URL != nil {
		call.url = request.URL.String()
	}

	return call
}

//SetResponse records the tenant's answer
func (call *HTTPCall) SetResponse(status int, headers http.Header, body []byte) {
	if call == nil {
		return
	}
	call.status = status
	call.responseHeaders = RedactHeaders(headers)
	call.responseBody = responseBody(headers.Get("Content-Type"), body)
}

//SetError records a request that never produced an answer
func (call *HTTPCall) SetError(err error) {
	if call == nil || err == nil {
		return
	}
	call.failure = err.Error()
}

//Flush writes the record. It is called from a defer, so the call is recorded
//even when the caller exits through log.Fatal or panics.
func (call *HTTPCall) Flush() {

	if call == nil || call.logger == nil {
		return
	}

	record := map[string]interface{}{
		"ts":              timestamp(),
		"type":            "http",
		"method":          call.method,
		"url":             call.url,
		"duration_ms":     time.Since(call.started).Milliseconds(),
		"request_headers": call.requestHeaders,
	}
	if call.requestBody != nil {
		record["request_body"] = call.requestBody
	}
	if call.status != 0 {
		record["status"] = call.status
		record["response_headers"] = call.responseHeaders
	}
	if call.responseBody != nil {
		record["response_body"] = call.responseBody
	}
	if call.failure != "" {
		record["error"] = call.failure
	}

	call.logger.write(record)
	//Flush may be reached twice if a caller also flushes explicitly
	call.logger = nil
}

//RedactHeaders copies headers, replacing the value of every header that can
//carry a credential. The key is kept, so the record still shows that
//authentication was sent.
func RedactHeaders(headers http.Header) map[string]string {

	if headers == nil {
		return nil
	}

	result := map[string]string{}
	for key, values := range headers {
		if redactedHeaders[http.CanonicalHeaderKey(key)] {
			result[key] = redacted
			continue
		}
		result[key] = strings.Join(values, ", ")
	}

	return result
}

//RedactFlags copies command parameters, hiding the value of anything whose
//name suggests a credential
func RedactFlags(flags map[string]string) map[string]string {

	if flags == nil {
		return nil
	}

	result := map[string]string{}
	for name, value := range flags {
		if redactedFlags[strings.ToLower(name)] {
			result[name] = redacted
			continue
		}
		result[name] = value
	}

	return result
}

//CaptureRequestBody reads the body the request will send. http.NewRequest
//populates GetBody for the bytes.Buffer bodies this client uses, so the body
//can be read without disturbing the request itself.
func CaptureRequestBody(request *http.Request) *Body {

	if request == nil || request.GetBody == nil {
		return nil
	}

	reader, err := request.GetBody()
	if err != nil {
		return &Body{Bytes: request.ContentLength, Note: "unavailable"}
	}
	defer reader.Close()

	head := make([]byte, requestScanBytes)
	read, _ := io.ReadFull(reader, head)
	head = head[:read]

	//An uploaded artifact is a base64 zip of several megabytes. It is the
	//payload, not information about the operation, and it is never written.
	if index := bytes.Index(head, []byte(`"ArtifactContent"`)); index >= 0 {
		return &Body{
			Bytes: request.ContentLength,
			Text:  string(head[:index]) + `"ArtifactContent":"` + redacted + `"}`,
			Note:  "artifact content omitted",
		}
	}

	rest, _ := io.ReadAll(reader)
	content := append(head, rest...)

	if !isTextual("", content) {
		return &Body{Bytes: int64(len(content)), Binary: true, Note: "binary body omitted"}
	}

	return &Body{Bytes: int64(len(content)), Text: string(content)}
}

//responseBody records a textual answer in full and reduces anything binary to
//its size. The artifact download returns a multi megabyte zip through the same
//code path as every JSON reply.
func responseBody(contentType string, body []byte) *Body {

	if len(body) == 0 {
		return &Body{Bytes: 0}
	}

	if !isTextual(contentType, body) {
		return &Body{Bytes: int64(len(body)), Binary: true, Note: "binary body omitted"}
	}

	return &Body{Bytes: int64(len(body)), Text: string(body)}
}

//isTextual decides whether content can be written to the log as text. The
//declared media type wins; when there is none the content itself is examined.
func isTextual(contentType string, content []byte) bool {

	mediaType := ""
	if contentType != "" {
		parsed, _, err := mime.ParseMediaType(contentType)
		if err == nil {
			mediaType = strings.ToLower(parsed)
		}
	}

	if mediaType != "" {
		if strings.HasPrefix(mediaType, "text/") ||
			strings.HasSuffix(mediaType, "+json") ||
			strings.HasSuffix(mediaType, "+xml") ||
			textMediaTypes[mediaType] {
			return true
		}
		//An explicit application/zip or octet-stream is trusted
		return false
	}

	//No media type. A NUL byte never appears in the tenant's textual answers
	//but appears immediately in a zip.
	return utf8.Valid(content) && bytes.IndexByte(content, 0) < 0
}

//LogWriter returns the writer to give to log.SetOutput. Everything the program
//logs is passed through to terminal unchanged and recorded as a log record, so
//the 59 log.Fatalln call sites in the CLI need no modification to be audited.
func (logger *Logger) LogWriter(terminal io.Writer) io.Writer {
	return &logWriter{logger: logger, terminal: terminal}
}

type logWriter struct {
	logger   *Logger
	terminal io.Writer
}

//Write records each line and passes the bytes on. Errors are swallowed on
//purpose: io.MultiWriter would abandon the remaining writers after the first
//failure, so a closed stdout - landscaper ... | head - would silently cost us
//the audit record.
func (writer *logWriter) Write(data []byte) (int, error) {

	fatal := fatalOnStack()

	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		level := "info"
		if fatal {
			level = "fatal"
		}
		writer.logger.Message(level, line)
	}

	//A fatal message is followed by os.Exit, which runs no defer and no cobra
	//hook. The end record is written here, while the process is still alive,
	//so that a failed run is not merely a run with a missing end record.
	if fatal {
		writer.logger.RunEnd("failed", "")
	}

	if writer.terminal != nil {
		writer.terminal.Write(data)
	}

	return len(data), nil
}

//fatalOnStack reports whether the standard logger is on its way to os.Exit
func fatalOnStack() bool {

	var counters [32]uintptr
	depth := runtime.Callers(2, counters[:])
	frames := runtime.CallersFrames(counters[:depth])

	for {
		frame, more := frames.Next()
		if strings.HasPrefix(frame.Function, "log.Fatal") ||
			strings.HasPrefix(frame.Function, "log.(*Logger).Fatal") {
			return true
		}
		if !more {
			return false
		}
	}
}
