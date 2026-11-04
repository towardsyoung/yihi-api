package plugins_test

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relay/channel"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYunqiaoResponsesProtocol(t *testing.T) {
	testVideoResponsesProtocol(t, videoResponsesTestCase{
		pluginKey: "yunqiao", model: "minimax-h3-lite",
		requestBody:   map[string]any{"model": "minimax-h3-lite", "input": "waves at sunset", "seconds": "5", "resolution": "768p"},
		wantAction:    "text_to_video",
		wantRequest:   map[string]any{"model": "minimax-h3-lite", "prompt": "waves at sunset", "seconds": float64(5), "resolution": "768p"},
		wantUsageKeys: []string{"seconds"}, wantVendorName: "yunqiao",
	})
}

func TestYunqiaoVideoContract(t *testing.T) {
	source, err := builtinplugins.Source("yunqiao")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "yunqiao"})
	require.NoError(t, err)
	decode := func(t *testing.T, body map[string]any) (map[string]any, error) {
		t.Helper()
		value, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"model": "my-video", "body": body})
		if callErr != nil {
			return nil, callErr
		}
		encoded, marshalErr := common.Marshal(value)
		require.NoError(t, marshalErr)
		var result map[string]any
		require.NoError(t, common.Unmarshal(encoded, &result))
		return result, nil
	}
	t.Run("Responses preserves optional audio through submission", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			audio   bool
			present bool
		}{
			{name: "disabled", audio: false, present: true},
			{name: "enabled", audio: true, present: true},
			{name: "omitted"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				body := map[string]any{"model": "minimax-h3-lite", "input": "waves"}
				if tc.present {
					body["generate_audio"] = tc.audio
				}
				intent, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_responses", "decodeRequest"}, map[string]any{
					"model": "minimax-h3-lite", "body": map[string]any{"kind": "json", "value": body},
				})
				require.NoError(t, err)
				outbound, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{
					"model": "minimax-h3-lite", "upstreamModel": "minimax-h3-lite", "baseUrl": "https://provider.example", "apiKey": "test-key",
					"requestBody": intent.(map[string]any)["requestBody"],
				})
				require.NoError(t, err)
				request := outbound.(map[string]any)["body"].(map[string]any)
				if tc.present {
					assert.Equal(t, tc.audio, request["generate_audio"])
				} else {
					assert.NotContains(t, request, "generate_audio")
				}
			})
		}
	})
	t.Run("multipart image bytes survive model mapping", func(t *testing.T) {
		files := []any{map[string]any{"field": "input_reference", "ref": "request_file:input_reference", "filename": "first.png"}}
		intent, err := decode(t, map[string]any{"kind": "multipart", "fields": map[string][]string{"model": {"my-video"}, "prompt": {"waves"}, "seconds": {"5"}, "resolution": {"768p"}}, "files": files})
		require.NoError(t, err)
		assert.Equal(t, "my-video", intent["model"])
		assert.Equal(t, "image_to_video", intent["action"])
		var input bytes.Buffer
		writer := multipart.NewWriter(&input)
		file, err := writer.CreateFormFile("input_reference", "first.png")
		require.NoError(t, err)
		_, err = file.Write([]byte("actual-image-bytes"))
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(input.Bytes()))
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		c.Set("task_request", intent["requestBody"])
		info := &relaycommon.RelayInfo{OriginModelName: "my-video", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "minimax-h3-lite", ChannelBaseUrl: "https://provider.example", ApiKey: "test-key"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
		adaptor := taskplugin.New(plugin)
		adaptor.Init(info)
		body, err := adaptor.BuildRequestBody(c, info)
		require.NoError(t, err)
		content, err := io.ReadAll(body)
		require.NoError(t, err)
		_, params, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		require.NoError(t, err)
		form, err := multipart.NewReader(bytes.NewReader(content), params["boundary"]).ReadForm(1024)
		require.NoError(t, err)
		defer form.RemoveAll()
		assert.Equal(t, []string{"minimax-h3-lite"}, form.Value["model"])
		assert.Equal(t, []string{"5"}, form.Value["seconds"])
		assert.Equal(t, []string{"768p"}, form.Value["resolution"])
		require.Len(t, form.File["input_reference"], 1)
		opened, err := form.File["input_reference"][0].Open()
		require.NoError(t, err)
		defer opened.Close()
		uploaded, err := io.ReadAll(opened)
		require.NoError(t, err)
		assert.Equal(t, "actual-image-bytes", string(uploaded))
	})
	t.Run("duration validation covers JSON and form", func(t *testing.T) {
		for _, value := range []any{0, -1, 3601, 1.5, "1e309", true, nil, ""} {
			_, err := decode(t, map[string]any{"kind": "json", "value": map[string]any{"prompt": "waves", "seconds": value}})
			require.Error(t, err)
		}
		for _, seconds := range []string{"0", "3601", "NaN", "1.5"} {
			_, err := decode(t, map[string]any{"kind": "multipart", "fields": map[string][]string{"prompt": {"waves"}, "seconds": {seconds}}})
			require.Error(t, err)
		}
		_, err := decode(t, map[string]any{"kind": "json", "value": map[string]any{"prompt": "waves", "seconds": 5, "duration": 6}})
		require.ErrorContains(t, err, "must match")
		intent, err := decode(t, map[string]any{"kind": "json", "value": map[string]any{"prompt": "waves"}})
		require.NoError(t, err)
		assert.Equal(t, float64(5), intent["requestBody"].(map[string]any)["seconds"])
	})
	t.Run("polls unified task and parses nested video", func(t *testing.T) {
		value, err := plugin.Engine.Call(t.Context(), "buildQueryRequest", map[string]any{"baseUrl": "https://provider.example", "taskId": "upstream/id", "apiKey": "key"})
		require.NoError(t, err)
		assert.Equal(t, "https://provider.example/v1/tasks/upstream%2Fid", value.(map[string]any)["url"])
		for status, expected := range map[string]string{"pending": "QUEUED", "processing": "IN_PROGRESS", "completed": "SUCCESS", "failed": "FAILURE", "new_status": "UNKNOWN", "toString": "UNKNOWN"} {
			body := map[string]any{"status": status, "result": map[string]any{"videos": []any{map[string]any{"index": 0, "url": "https://provider.example/v1/videos/upstream/content"}}}, "error": map[string]any{"message": "provider failure"}}
			value, err := plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{}, body)
			require.NoError(t, err)
			result := value.(map[string]any)
			assert.Equal(t, expected, result["status"])
			if status == "completed" {
				assert.Equal(t, "https://provider.example/v1/videos/upstream/content", result["url"])
			}
			if status == "failed" {
				assert.Equal(t, "provider failure", result["reason"])
			}
		}
	})
	t.Run("content uses private upstream id and channel credentials", func(t *testing.T) {
		adaptor := taskplugin.New(plugin)
		adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://provider.example", ApiKey: "test-key"}})
		task := &model.Task{TaskID: "task_public", Status: model.TaskStatusSuccess, PrivateData: model.TaskPrivateData{UpstreamTaskID: "private/id"}}
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			request, err := adaptor.BuildContentRequest(task, "video", channel.TaskArtifactClientRequest{Method: method})
			require.NoError(t, err)
			assert.Equal(t, "https://provider.example/v1/videos/private%2Fid/content", request.URL)
			assert.Equal(t, "Bearer test-key", request.Headers["Authorization"])
			assert.Equal(t, method, request.Method)
		}
	})
	t.Run("video renderer preserves Yunqiao result envelope", func(t *testing.T) {
		body := map[string]any{"id": "task_public", "object": "media.task", "status": "completed", "result": map[string]any{"videos": []any{map[string]any{"index": 0, "url": "https://provider.example/v1/videos/private/content"}}}}
		value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "render"}, map[string]any{}, map[string]any{"data": body})
		require.NoError(t, err)
		encoded, err := common.Marshal(value)
		require.NoError(t, err)
		var rendered map[string]any
		require.NoError(t, common.Unmarshal(encoded, &rendered))
		assert.Equal(t, "https://provider.example/v1/videos/private/content", rendered["result"].(map[string]any)["videos"].([]any)[0].(map[string]any)["url"])
	})
	t.Run("shared model spelling maps to Yunqiao wire name and preserves false", func(t *testing.T) {
		value, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"model": "my-video", "upstreamModel": "MiniMax-H3", "baseUrl": "https://provider.example", "apiKey": "key", "requestBody": map[string]any{"prompt": "waves", "duration": 5, "generate_audio": false}})
		require.NoError(t, err)
		body := value.(map[string]any)["body"].(map[string]any)
		assert.Equal(t, "minimax-h3", body["model"])
		assert.Equal(t, false, body["generate_audio"])
		assert.NotContains(t, body, "duration")
	})
	t.Run("vendor quota never becomes local usage", func(t *testing.T) {
		for _, body := range []map[string]any{{"usage": map[string]any{"quota": 2206}}, {"seconds": 3601}, {"seconds": -5}} {
			value, err := plugin.Engine.Call(t.Context(), "extractUsageOnComplete", map[string]any{}, map[string]any{}, body)
			require.NoError(t, err)
			assert.Empty(t, value)
		}
	})
}
