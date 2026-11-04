package service

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const (
	mediaDiagnosticContextKey   = "media_diagnostic_capture"
	mediaDiagnosticCaptureLimit = 128 * 1024
	mediaDiagnosticPayloadLimit = 60 * 1024
)

var mediaDiagnosticURLPattern = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)
var mediaDiagnosticBearerPattern = regexp.MustCompile(`(?i)\bBearer\s+[a-z0-9._~+/=-]+`)

type mediaDiagnosticCapture struct {
	RequestID string
	UserID    int
	Secrets   []string
}

// BeginMediaDiagnostics is idempotent across channel retries. Never captures
// ingress headers; request bodies are replayed independently of the relay.
func BeginMediaDiagnostics(c *gin.Context) {
	if c == nil || c.Request == nil || model.DB == nil {
		return
	}
	if _, exists := c.Get(mediaDiagnosticContextKey); exists {
		return
	}
	capture := &mediaDiagnosticCapture{RequestID: c.GetString(common.RequestIdKey), UserID: c.GetInt("id")}
	if capture.RequestID == "" {
		return
	}
	capture.Secrets = mediaDiagnosticRequestSecrets(c.Request)
	c.Set(mediaDiagnosticContextKey, capture)
	payload := map[string]any{"method": c.Request.Method, "url": c.Request.URL.Path}
	storage, err := common.GetBodyStorage(c)
	if err == nil {
		reader, readErr := storage.NewReader()
		if readErr == nil {
			payload["body"], capture.Secrets = readMediaDiagnosticBody(reader, c.GetHeader("Content-Type"), capture.Secrets)
			reader.Close()
		} else {
			payload["body"] = map[string]any{"omitted": "body_unavailable"}
		}
	} else {
		payload["body"] = map[string]any{"omitted": "body_unavailable"}
	}
	saveMediaDiagnostic(model.MediaDiagnostic{RequestID: capture.RequestID, UserID: capture.UserID, Stage: "client_request"}, payload)
}

// DoMediaDiagnosticRequest observes exactly the request sent by the relay. It
// preserves GetBody/retry semantics and never reads a response ahead of callers.
func DoMediaDiagnosticRequest(c *gin.Context, client *http.Client, req *http.Request, channelID int) (*http.Response, error) {
	value, exists := c.Get(mediaDiagnosticContextKey)
	capture, ok := value.(*mediaDiagnosticCapture)
	if !exists || !ok {
		return client.Do(req)
	}
	secrets := append(append([]string{}, capture.Secrets...), mediaDiagnosticRequestSecrets(req)...)
	requestPayload, secrets := mediaDiagnosticRequest(req, secrets)
	payload := map[string]any{"request": requestPayload}
	var requestBuffer *mediaDiagnosticBuffer
	if req.Body != nil && req.GetBody == nil {
		requestBuffer = &mediaDiagnosticBuffer{}
		req.Body = &mediaDiagnosticReadCloser{Reader: io.TeeReader(req.Body, requestBuffer), Closer: req.Body}
	}
	entry := model.MediaDiagnostic{RequestID: capture.RequestID, UserID: capture.UserID, ChannelID: channelID, Stage: "submit"}
	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		payload["error"] = "transport_error"
		payload["elapsed_ms"] = time.Since(started).Milliseconds()
		saveMediaDiagnostic(entry, payload)
		return resp, err
	}
	complete := func(body *mediaDiagnosticBuffer, readErr error) {
		if requestBuffer != nil {
			requestBuffer.mu.Lock()
			secrets = mediaDiagnosticBodySecrets(requestBuffer.data, req.Header.Get("Content-Type"), secrets)
			requestBuffer.mu.Unlock()
			payload["request"].(map[string]any)["body"] = requestBuffer.body(req.Header.Get("Content-Type"), secrets)
		}
		payload["response"] = mediaDiagnosticResponse(resp, body, secrets)
		payload["elapsed_ms"] = time.Since(started).Milliseconds()
		if readErr != nil {
			payload["error"] = "response_read_incomplete"
		}
		saveMediaDiagnostic(entry, payload)
	}
	resp.Body = &mediaDiagnosticResponseBody{ReadCloser: resp.Body, complete: complete, expectedLength: resp.ContentLength}
	return resp, nil
}

type mediaDiagnosticReadCloser struct {
	io.Reader
	io.Closer
}

type mediaDiagnosticBuffer struct {
	mu   sync.Mutex
	data []byte
	size int64
}

func (b *mediaDiagnosticBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.size += int64(len(p))
	if remaining := mediaDiagnosticCaptureLimit - len(b.data); remaining > 0 {
		b.data = append(b.data, p[:min(remaining, len(p))]...)
	}
	return len(p), nil
}

func (b *mediaDiagnosticBuffer) body(contentType string, secrets []string) any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return sanitizeMediaDiagnosticBody(b.data, contentType, b.size > int64(len(b.data)), secrets)
}

type mediaDiagnosticResponseBody struct {
	io.ReadCloser
	buffer         mediaDiagnosticBuffer
	once           sync.Once
	complete       func(*mediaDiagnosticBuffer, error)
	expectedLength int64
}

func (b *mediaDiagnosticResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.buffer.Write(p[:n])
	if err != nil {
		b.once.Do(func() {
			if err == io.EOF {
				b.complete(&b.buffer, nil)
			} else {
				b.complete(&b.buffer, err)
			}
		})
	}
	return n, err
}

func (b *mediaDiagnosticResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() {
		b.buffer.mu.Lock()
		complete := b.expectedLength >= 0 && b.buffer.size == b.expectedLength
		b.buffer.mu.Unlock()
		if err != nil {
			b.complete(&b.buffer, err)
		} else if complete {
			b.complete(&b.buffer, nil)
		} else {
			b.complete(&b.buffer, io.ErrUnexpectedEOF)
		}
	})
	return err
}

func mediaDiagnosticRequest(req *http.Request, secrets []string) (map[string]any, []string) {
	result := map[string]any{"method": req.Method, "url": sanitizeMediaDiagnosticString(req.URL.String(), secrets), "headers": mediaDiagnosticHeaders(req.Header, secrets)}
	query := make(map[string]any)
	for key, values := range req.URL.Query() {
		query[key] = strings.Join(values, ",")
	}
	if len(query) > 0 {
		result["query"] = sanitizeMediaDiagnosticValue(query, secrets, 0)
	}
	if req.Body == nil || req.Body == http.NoBody {
		return result, secrets
	}
	if req.GetBody == nil {
		result["body"] = map[string]any{"omitted": "body_not_replayable"}
		return result, secrets
	}
	reader, err := req.GetBody()
	if err != nil {
		result["body"] = map[string]any{"omitted": "body_unavailable"}
		return result, secrets
	}
	defer reader.Close()
	result["body"], secrets = readMediaDiagnosticBody(reader, req.Header.Get("Content-Type"), secrets)
	return result, secrets
}

func mediaDiagnosticResponse(resp *http.Response, body *mediaDiagnosticBuffer, secrets []string) map[string]any {
	return map[string]any{"status": resp.StatusCode, "headers": mediaDiagnosticHeaders(resp.Header, secrets), "body": body.body(resp.Header.Get("Content-Type"), secrets)}
}

// Header allowlist avoids arbitrary vendor credentials and Set-Cookie values.
func mediaDiagnosticHeaders(headers http.Header, secrets []string) map[string]string {
	result := make(map[string]string)
	for _, name := range []string{"Content-Type", "X-Request-Id", "Request-Id", "X-Oneapi-Request-Id", "X-Amzn-Requestid", "X-Tt-Logid", "Retry-After"} {
		if value := headers.Get(name); value != "" {
			result[name] = sanitizeMediaDiagnosticString(value, secrets)
		}
	}
	return result
}

func mediaDiagnosticSensitiveKey(key string) bool {
	key = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, key)
	return key == "key" || strings.Contains(key, "authorization") || key == "auth" || key == "authkey" || key == "authheader" || key == "jwt" || key == "sig" || key == "sign" || key == "sessionid" || (key == "token" || strings.HasSuffix(key, "token") || key == "tokenvalue") || strings.Contains(key, "secret") || strings.Contains(key, "password") || strings.Contains(key, "signature") || strings.Contains(key, "credential") || strings.Contains(key, "cookie") || strings.Contains(key, "apikey") || strings.Contains(key, "privatekey") || strings.Contains(key, "accesskey")
}

func mediaDiagnosticRequestSecrets(req *http.Request) []string {
	secrets := mediaDiagnosticSecrets(req.Header)
	for key, values := range req.URL.Query() {
		if mediaDiagnosticSensitiveKey(key) {
			for _, value := range values {
				if len(value) >= 6 {
					secrets = append(secrets, value)
				}
			}
		}
	}
	if req.URL.User != nil {
		if password, ok := req.URL.User.Password(); ok && len(password) >= 6 {
			secrets = append(secrets, password)
		}
	}
	return secrets
}

func mediaDiagnosticSecrets(headers http.Header) []string {
	var secrets []string
	for key, values := range headers {
		if !mediaDiagnosticSensitiveKey(key) {
			continue
		}
		for _, value := range values {
			if len(value) >= 6 {
				secrets = append(secrets, value)
			}
			if _, token, ok := strings.Cut(value, " "); ok && len(token) >= 6 {
				secrets = append(secrets, token)
			}
		}
	}
	return secrets
}

func sanitizeMediaDiagnosticString(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	value = mediaDiagnosticBearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = mediaDiagnosticURLPattern.ReplaceAllStringFunc(value, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "[URL REDACTED]"
		}
		parsed.User, parsed.RawQuery, parsed.Fragment = nil, "", ""
		parsed.ForceQuery = false
		return parsed.String()
	})
	if strings.HasPrefix(strings.ToLower(value), "data:") || isMediaDiagnosticBase64(value) {
		digest := sha256.Sum256([]byte(value))
		return fmt.Sprintf("[MEDIA OMITTED bytes=%d sha256=%x]", len(value), digest)
	}
	if len(value) > 8192 {
		value = string(bytes.ToValidUTF8([]byte(value[:8192]), nil)) + " [TRUNCATED]"
	}
	return value
}

func isMediaDiagnosticBase64(value string) bool {
	if len(value) < 256 {
		return false
	}
	for _, r := range value {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("+/=_-\r\n", r)) {
			return false
		}
	}
	return true
}

func sanitizeMediaDiagnosticValue(value any, secrets []string, depth int) any {
	if depth > 24 {
		return map[string]any{"omitted": "depth_limit"}
	}
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, item := range value {
			safeKey := sanitizeMediaDiagnosticString(key, secrets)
			if mediaDiagnosticSensitiveKey(key) {
				result[safeKey] = "[REDACTED]"
			} else if strings.Contains(strings.ToLower(key), "base64") || key == "b64_json" || strings.EqualFold(key, "inlineData") || strings.EqualFold(key, "inline_data") {
				result[safeKey] = map[string]any{"omitted": "media_content"}
			} else {
				result[safeKey] = sanitizeMediaDiagnosticValue(item, secrets, depth+1)
			}
		}
		return result
	case []any:
		result := make([]any, 0, min(len(value), 100))
		for _, item := range value[:min(len(value), 100)] {
			result = append(result, sanitizeMediaDiagnosticValue(item, secrets, depth+1))
		}
		if len(value) > 100 {
			result = append(result, map[string]any{"omitted": "array_items", "count": len(value) - 100})
		}
		return result
	case string:
		return sanitizeMediaDiagnosticString(value, secrets)
	default:
		return value
	}
}

func collectMediaDiagnosticSecrets(value any, secrets []string, depth int) []string {
	if depth > 24 || len(secrets) >= 64 {
		return secrets
	}
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			if text, ok := item.(string); ok && mediaDiagnosticSensitiveKey(key) && len(text) >= 6 && len(secrets) < 64 {
				secrets = append(secrets, text)
			}
			secrets = collectMediaDiagnosticSecrets(item, secrets, depth+1)
		}
	case []any:
		for _, item := range value {
			secrets = collectMediaDiagnosticSecrets(item, secrets, depth+1)
		}
	}
	return secrets
}

func readMediaDiagnosticBody(reader io.Reader, contentType string, secrets []string) (any, []string) {
	data, err := io.ReadAll(io.LimitReader(reader, mediaDiagnosticCaptureLimit+1))
	if err != nil {
		return map[string]any{"omitted": "body_read_failed"}, secrets
	}
	secrets = mediaDiagnosticBodySecrets(data, contentType, secrets)
	return sanitizeMediaDiagnosticBody(data[:min(len(data), mediaDiagnosticCaptureLimit)], contentType, len(data) > mediaDiagnosticCaptureLimit, secrets), secrets
}

// Keep credentials found in request bodies for redacting provider error echoes.
func mediaDiagnosticBodySecrets(data []byte, contentType string, secrets []string) []string {
	if len(data) > mediaDiagnosticCaptureLimit {
		return secrets
	}
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if mediaType == "multipart/form-data" {
		reader := multipart.NewReader(bytes.NewReader(data), params["boundary"])
		for range 100 {
			part, err := reader.NextPart()
			if err != nil {
				break
			}
			if part.FileName() != "" || !mediaDiagnosticSensitiveKey(part.FormName()) {
				continue
			}
			text, err := io.ReadAll(io.LimitReader(part, 8193))
			if err == nil && len(text) >= 6 && len(text) <= 8192 && len(secrets) < 64 {
				secrets = append(secrets, string(text))
			}
		}
		return secrets
	}
	var value any
	if common.Unmarshal(data, &value) == nil {
		return collectMediaDiagnosticSecrets(value, secrets, 0)
	}
	return secrets
}

func sanitizeMediaDiagnosticBody(data []byte, contentType string, truncated bool, secrets []string) any {
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if mediaType == "multipart/form-data" {
		reader := multipart.NewReader(bytes.NewReader(data), params["boundary"])
		parts := make([]any, 0)
		for range 100 {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				truncated = true
				break
			}
			field := map[string]any{"name": sanitizeMediaDiagnosticString(part.FormName(), secrets)}
			if part.FileName() != "" {
				digest := sha256.New()
				size, copyErr := io.Copy(digest, part)
				if copyErr != nil {
					truncated = true
				}
				field["file"] = map[string]any{"content_type": sanitizeMediaDiagnosticString(part.Header.Get("Content-Type"), secrets), "captured_bytes": size, "omitted": true}
				if copyErr == nil && !truncated {
					field["file"].(map[string]any)["sha256"] = fmt.Sprintf("%x", digest.Sum(nil))
				}
			} else {
				text, _ := io.ReadAll(io.LimitReader(part, 8193))
				if mediaDiagnosticSensitiveKey(part.FormName()) {
					field["value"] = "[REDACTED]"
				} else {
					field["value"] = sanitizeMediaDiagnosticString(string(text), secrets)
				}
			}
			parts = append(parts, field)
		}
		return map[string]any{"parts": parts, "truncated": truncated}
	}
	if truncated {
		return map[string]any{"omitted": "capture_size_limit", "limit_bytes": mediaDiagnosticCaptureLimit}
	}
	if mediaType == "text/event-stream" {
		return map[string]any{"omitted": "event_stream"}
	}
	var value any
	if len(data) == 0 {
		return nil
	}
	if !utf8.Valid(data) || common.Unmarshal(data, &value) != nil {
		return map[string]any{"omitted": "non_json_body", "bytes": len(data)}
	}
	secrets = collectMediaDiagnosticSecrets(value, secrets, 0)
	if len(secrets) >= 64 {
		return map[string]any{"omitted": "sensitive_field_limit"}
	}
	return sanitizeMediaDiagnosticValue(value, secrets, 0)
}

func saveMediaDiagnostic(entry model.MediaDiagnostic, payload map[string]any) {
	if model.DB == nil {
		return
	}
	encoded, err := common.Marshal(payload)
	if err != nil {
		return
	}
	if len(encoded) > mediaDiagnosticPayloadLimit {
		marker := map[string]any{"omitted": "payload_size_limit", "limit_bytes": mediaDiagnosticPayloadLimit}
		if response, ok := payload["response"].(map[string]any); ok {
			response["body"] = marker
		}
		encoded, _ = common.Marshal(payload)
		if len(encoded) > mediaDiagnosticPayloadLimit {
			if request, ok := payload["request"].(map[string]any); ok {
				request["body"] = marker
			}
			if _, ok := payload["body"]; ok {
				payload["body"] = marker
			}
			encoded, _ = common.Marshal(payload)
		}
		if len(encoded) > mediaDiagnosticPayloadLimit {
			encoded, _ = common.Marshal(marker)
		}
	}
	if entry.ID == "" {
		entry.ID = common.NewRequestId()
	}
	entry.CreatedAt = time.Now().Unix()
	if entry.ExpiresAt == 0 {
		entry.ExpiresAt = time.Now().Add(model.MediaDiagnosticRetention).Unix()
	}
	if entry.ExpiresAt <= entry.CreatedAt {
		return
	}
	entry.Payload = string(encoded)
	if err := model.SaveMediaDiagnostic(&entry); err != nil {
		common.SysError("failed to save media diagnostic")
	}
}

// Polling keeps the latest response and latest failure separately, rather than
// accumulating every polling round. Expiry is anchored to task submission.
func RecordMediaPollDiagnostic(task *model.Task, resp *http.Response, body []byte, pollErr error) {
	stage := "poll_latest"
	if pollErr != nil || resp != nil && resp.StatusCode >= 400 {
		stage = "poll_error"
	}
	var req *http.Request
	if resp != nil {
		req = resp.Request
	}
	recordMediaTaskHTTPDiagnostic(task, req, resp, body, pollErr, stage)
}

// Post-processing can submit a second provider job without a client request.
// Preserve each such attempt, including retries, under the original task.
func RecordMediaTaskSubmitDiagnostic(task *model.Task, req *http.Request, resp *http.Response, body []byte, requestErr error) {
	recordMediaTaskHTTPDiagnostic(task, req, resp, body, requestErr, "submit")
}

func recordMediaTaskHTTPDiagnostic(task *model.Task, req *http.Request, resp *http.Response, body []byte, requestErr error, stage string) {
	if task == nil || model.DB == nil {
		return
	}
	entry := model.MediaDiagnostic{TaskID: task.TaskID, ChannelID: task.ChannelId, UserID: task.UserId, Stage: stage, ExpiresAt: task.SubmitTime + int64(model.MediaDiagnosticRetention/time.Second)}
	if stage != "submit" {
		entry.ID = fmt.Sprintf("%x", sha256.Sum256([]byte(task.TaskID+stage)))
	}
	if task.PrivateData.Execution != nil {
		entry.RequestID = task.PrivateData.Execution.RequestID
	}
	payload := make(map[string]any)
	var secrets []string
	if req != nil {
		secrets = mediaDiagnosticRequestSecrets(req)
		requestPayload, requestSecrets := mediaDiagnosticRequest(req, secrets)
		payload["request"] = requestPayload
		secrets = requestSecrets
	}
	if resp != nil {
		capture := &mediaDiagnosticBuffer{}
		capture.Write(body)
		payload["response"] = mediaDiagnosticResponse(resp, capture, secrets)
	}
	if requestErr != nil {
		payload["error"] = "upstream_transport_or_read_error"
	}
	saveMediaDiagnostic(entry, payload)
}

func StartMediaDiagnosticCleanup() {
	if !common.IsMasterNode {
		return
	}
	go func() {
		for {
			if err := model.DeleteExpiredMediaDiagnostics(time.Now().Unix()); err != nil {
				common.SysError("failed to clean media diagnostics")
			}
			time.Sleep(time.Hour)
		}
	}()
}
