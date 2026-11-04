package service

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func mediaDiagnosticTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.MediaDiagnostic{}))
	previous := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previous; sqlDB, _ := db.DB(); sqlDB.Close() })
	return db
}

func TestMediaDiagnosticsSanitize(t *testing.T) {
	tests := []struct {
		name, input, contentType string
		forbidden, retained      []string
	}{
		{"nested credentials and echoed secrets", `{"api_key":"vendor-secret","error":{"message":"bad vendor-secret"},"nested":{"access_token":"private-token","password":"password-secret"},"max_tokens":0,"n":0,"enabled":false,"prompt":"keep me"}`, "application/json", []string{"vendor-secret", "private-token", "password-secret"}, []string{`"max_tokens":0`, `"enabled":false`, "keep me"}},
		{"signed URLs and bearer echoes", `{"url":"https://user:pass@cdn.example/a.png?Signature=secret#fragment","prompt":"see https://cdn.example/b.png?token=secret now","message":"Bearer echoed-secret"}`, "application/json", []string{"user:pass", "Signature=secret", "token=secret", "fragment", "echoed-secret"}, []string{"cdn.example/a.png", "cdn.example/b.png"}},
		{"inline media including short base64", `{"b64_json":"aGVsbG8=","reference":"data:image/png;base64,aGVsbG8=","image_base64":"aGVsbG8=","seconds":6}`, "application/json", []string{"aGVsbG8="}, []string{`"seconds":6`, "OMITTED"}},
		{"non JSON fails closed", `Authorization: super-secret`, "text/plain", []string{"super-secret"}, []string{"non_json_body"}},
		{"SSE fails closed", "data: {\"key\":\"super-secret\"}\n\n", "text/event-stream", []string{"super-secret"}, []string{"event_stream"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			safe := sanitizeMediaDiagnosticBody([]byte(tc.input), tc.contentType, false, nil)
			encoded, err := common.Marshal(safe)
			require.NoError(t, err)
			for _, value := range tc.forbidden {
				assert.NotContains(t, string(encoded), value)
			}
			for _, value := range tc.retained {
				assert.Contains(t, string(encoded), value)
			}
		})
	}
	t.Run("oversized and nested data are explicitly omitted", func(t *testing.T) {
		large := `{"prompt":"` + strings.Repeat("x", mediaDiagnosticCaptureLimit) + `"}`
		safe, _ := readMediaDiagnosticBody(strings.NewReader(large), "application/json", nil)
		encoded, err := common.Marshal(safe)
		require.NoError(t, err)
		assert.Contains(t, string(encoded), "capture_size_limit")
		assert.NotContains(t, string(encoded), strings.Repeat("x", 100))
	})
	t.Run("multipart excludes credentials filenames and file bytes", func(t *testing.T) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		require.NoError(t, writer.WriteField("prompt", "keep this prompt"))
		require.NoError(t, writer.WriteField("api_key", "multipart-secret"))
		file, err := writer.CreateFormFile("image", "private-person-name.png")
		require.NoError(t, err)
		_, err = io.WriteString(file, "private-file-bytes")
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		safe, _ := readMediaDiagnosticBody(&body, writer.FormDataContentType(), nil)
		encoded, err := common.Marshal(safe)
		require.NoError(t, err)
		assert.Contains(t, string(encoded), "keep this prompt")
		assert.Contains(t, string(encoded), "sha256")
		for _, value := range []string{"multipart-secret", "private-person-name", "private-file-bytes"} {
			assert.NotContains(t, string(encoded), value)
		}
	})
}

func TestMediaDiagnosticsRelayPreservesBodiesAndAttempts(t *testing.T) {
	mediaDiagnosticTestDB(t)
	const input = `{"model":"original","prompt":"draw this","n":0}`
	const outbound = `{"model":"mapped","prompt":"draw this","n":0,"watermark":false,"api_key":"body-only-secret"}`
	const response = `{"id":"upstream-task","message":"invalid body-only-secret","url":"https://cdn.example/result?signature=private-signature"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actual, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, outbound, string(actual))
		assert.Equal(t, "Bearer private-api-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "vendor-request-id")
		w.Header().Set("Set-Cookie", "private-cookie")
		_, _ = io.WriteString(w, response)
	}))
	defer server.Close()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(input))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(common.RequestIdKey, "gateway-request")
	c.Set("id", 7)
	defer common.CleanupBodyStorage(c)
	BeginMediaDiagnostics(c)
	BeginMediaDiagnostics(c)
	original, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	originalBytes, err := original.Bytes()
	require.NoError(t, err)
	assert.Equal(t, input, string(originalBytes))
	for _, channelID := range []int{3, 5, 7} {
		req, err := http.NewRequest(http.MethodPost, server.URL+"/generate?api_key=private-api-key&model=mapped", strings.NewReader(outbound))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer private-api-key")
		// Exercise the single-use body path as well as net/http's replayable path.
		if channelID == 5 {
			req.GetBody = nil
		}
		resp, err := DoMediaDiagnosticRequest(c, server.Client(), req, channelID)
		require.NoError(t, err)
		var actual []byte
		switch channelID {
		case 5:
			// A caller that reads Content-Length bytes need not perform an extra EOF read.
			actual = make([]byte, len(response))
			_, err = io.ReadFull(resp.Body, actual)
		case 7:
			actual = make([]byte, 10)
			_, err = io.ReadFull(resp.Body, actual)
		default:
			actual, err = io.ReadAll(resp.Body)
		}
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		if channelID != 7 {
			assert.Equal(t, response, string(actual))
		}
	}
	entries, err := model.GetMediaDiagnostics("gateway-request", "")
	require.NoError(t, err)
	require.Len(t, entries, 4)
	submitCount := 0
	for _, entry := range entries {
		assert.LessOrEqual(t, len(entry.Payload), mediaDiagnosticPayloadLimit)
		assert.NotContains(t, entry.Payload, "private-api-key")
		assert.NotContains(t, entry.Payload, "private-cookie")
		assert.NotContains(t, entry.Payload, "body-only-secret")
		assert.NotContains(t, entry.Payload, "private-signature")
		if entry.Stage == "submit" {
			submitCount++
			if entry.ChannelID == 7 {
				assert.Contains(t, entry.Payload, "response_read_incomplete")
			} else {
				assert.NotContains(t, entry.Payload, "response_read_incomplete")
			}
			assert.Contains(t, entry.Payload, `"watermark":false`)
			assert.Contains(t, entry.Payload, "vendor-request-id")
			assert.Contains(t, entry.Payload, `"n":0`)
		}
	}
	assert.Equal(t, 3, submitCount)
}

type mediaDiagnosticFailedTransport struct{}

func (mediaDiagnosticFailedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("https://provider.example?key=do-not-log")
}

func TestMediaDiagnosticsTransportFailureAndStorageFailure(t *testing.T) {
	db := mediaDiagnosticTestDB(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos", strings.NewReader(`{"prompt":"test"}`))
	c.Set(common.RequestIdKey, "failed-request")
	defer common.CleanupBodyStorage(c)
	BeginMediaDiagnostics(c)
	req, err := http.NewRequest("POST", "https://provider.example/generate", strings.NewReader(`{"seconds":6}`))
	require.NoError(t, err)
	_, err = DoMediaDiagnosticRequest(c, &http.Client{Transport: mediaDiagnosticFailedTransport{}}, req, 3)
	require.Error(t, err)
	entries, err := model.GetMediaDiagnostics("failed-request", "")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	found := false
	for _, entry := range entries {
		assert.NotContains(t, entry.Payload, "do-not-log")
		found = found || strings.Contains(entry.Payload, "transport_error")
	}
	assert.True(t, found)
	require.NoError(t, db.Migrator().DropTable(&model.MediaDiagnostic{}))
	// A failed diagnostic write must never change the provider result.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"successful-upstream-result"}`)
	}))
	defer server.Close()
	req, err = http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"prompt":"test"}`))
	require.NoError(t, err)
	resp, err := DoMediaDiagnosticRequest(c, server.Client(), req, 3)
	require.NoError(t, err)
	actual, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"id":"successful-upstream-result"}`, string(actual))
}

func TestMediaDiagnosticDatabaseMatrix(t *testing.T) {
	dialects := []struct {
		name      string
		dialector gorm.Dialector
	}{{"sqlite", sqlite.Open(":memory:")}}
	if dsn := os.Getenv("MEDIA_DIAGNOSTIC_MYSQL_DSN"); dsn != "" {
		dialects = append(dialects, struct {
			name      string
			dialector gorm.Dialector
		}{"mysql", mysql.Open(dsn)})
	}
	if dsn := os.Getenv("MEDIA_DIAGNOSTIC_POSTGRES_DSN"); dsn != "" {
		dialects = append(dialects, struct {
			name      string
			dialector gorm.Dialector
		}{"postgres", postgres.Open(dsn)})
	}
	for _, dialect := range dialects {
		t.Run(dialect.name, func(t *testing.T) {
			db, err := gorm.Open(dialect.dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			previous := model.DB
			model.DB = db
			defer func() { model.DB = previous; sqlDB, _ := db.DB(); sqlDB.Close() }()
			versionQuery := "SELECT version()"
			if dialect.name == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database version: %s", version)
			// Only run against disposable databases: the DSNs above are explicitly opt-in.
			for _, upgrade := range []bool{false, true} {
				t.Run(fmt.Sprintf("upgrade=%v", upgrade), func(t *testing.T) {
					require.NoError(t, db.Migrator().DropTable(&model.MediaDiagnostic{}, &model.Task{}))
					if upgrade {
						// Task's schema is unchanged by this feature. This is a representative
						// pre-feature task with its existing uniqueness/index definitions.
						require.NoError(t, db.AutoMigrate(&model.Task{}))
						require.NoError(t, db.Create(&model.Task{TaskID: "historical-task", UserId: 7, ClientTaskID: common.GetPointer("unique-request"), Data: []byte(`{"id":"historical-result"}`)}).Error)
					}
					for range 2 {
						require.NoError(t, db.AutoMigrate(&model.MediaDiagnostic{}))
					}
					if upgrade {
						var task model.Task
						require.NoError(t, db.Where("task_id = ?", "historical-task").First(&task).Error)
						assert.JSONEq(t, `{"id":"historical-result"}`, string(task.Data))
						assert.True(t, db.Migrator().HasIndex(&model.Task{}, "idx_tasks_user_client_task"))
						require.Error(t, db.Create(&model.Task{TaskID: "duplicate", UserId: 7, ClientTaskID: common.GetPointer("unique-request")}).Error)
					}
					now := time.Now().Unix()
					entries := []model.MediaDiagnostic{
						{ID: "submit-1", RequestID: "request-1", Stage: "submit", Payload: `{"n":0,"prompt":"你好"}`, CreatedAt: now, ExpiresAt: now + 3600},
						{ID: "expired", RequestID: "request-1", TaskID: "task-1", Payload: `{}`, CreatedAt: now - 3600, ExpiresAt: now - 1},
						{ID: "other", RequestID: "other", Payload: `{}`, CreatedAt: now, ExpiresAt: now + 3600},
					}
					for i := range entries {
						require.NoError(t, model.SaveMediaDiagnostic(&entries[i]))
					}
					loaded, err := model.GetMediaDiagnostics("request-1", "task-1")
					require.NoError(t, err)
					require.Len(t, loaded, 1)
					assert.JSONEq(t, entries[0].Payload, loaded[0].Payload)
					task := &model.Task{TaskID: "task-1", UserId: 7, ChannelId: 3, SubmitTime: now}
					task.PrivateData.Execution = &model.TaskExecutionSnapshot{RequestID: "request-1"}
					req := httptest.NewRequest("GET", "https://vendor.example/result?id=upstream", nil)
					resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Request: req}
					RecordMediaPollDiagnostic(task, resp, []byte(`{"status":"running"}`), nil)
					RecordMediaPollDiagnostic(task, resp, []byte(`{"status":"done"}`), nil)
					resp.StatusCode = 503
					RecordMediaPollDiagnostic(task, resp, []byte(`{"error":"busy"}`), nil)
					loaded, err = model.GetMediaDiagnostics("request-1", "task-1")
					require.NoError(t, err)
					require.Len(t, loaded, 3)
					for _, row := range loaded {
						if row.Stage == "poll_latest" {
							assert.Contains(t, row.Payload, "done")
							assert.NotContains(t, row.Payload, "running")
						}
					}
					submitReq := httptest.NewRequest("POST", "https://vendor.example/upscale", strings.NewReader(`{"resolution":"1080p"}`))
					submitReq.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(`{"resolution":"1080p"}`)), nil }
					for range 2 {
						RecordMediaTaskSubmitDiagnostic(task, submitReq, resp, []byte(`{"error":"busy"}`), nil)
					}
					loaded, err = model.GetMediaDiagnostics("request-1", "task-1")
					require.NoError(t, err)
					require.Len(t, loaded, 5)
					require.NoError(t, model.DeleteExpiredMediaDiagnostics(now))
					var count int64
					require.NoError(t, db.Model(&model.MediaDiagnostic{}).Where("id = ?", "expired").Count(&count).Error)
					assert.Zero(t, count)
					require.NoError(t, db.Model(&model.MediaDiagnostic{}).Count(&count).Error)
					assert.EqualValues(t, 6, count)
					// Oversized diagnostic payloads remain valid JSON with an explicit marker.
					saveMediaDiagnostic(model.MediaDiagnostic{RequestID: "bounded"}, map[string]any{"data": strings.Repeat("x", mediaDiagnosticPayloadLimit+1)})
					bounded, err := model.GetMediaDiagnostics("bounded", "")
					require.NoError(t, err)
					require.Len(t, bounded, 1)
					assert.Contains(t, bounded[0].Payload, "payload_size_limit")
				})
			}
			require.NoError(t, db.Migrator().DropTable(&model.MediaDiagnostic{}, &model.Task{}))
		})
	}
}
