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
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestYunqiaoLegacyVideoEntry(t *testing.T) {
	previousDB, previousRegistry, previousCache := model.DB, jsplugin.DefaultRegistry, common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Task{}))
	model.DB, jsplugin.DefaultRegistry, common.MemoryCacheEnabled = db, jsplugin.NewRegistry(), true
	t.Cleanup(func() {
		model.DB, jsplugin.DefaultRegistry, common.MemoryCacheEnabled = previousDB, previousRegistry, previousCache
		if previousDB != nil {
			model.InitChannelCache()
		}
		require.NoError(t, sqlDB.Close())
	})
	source, err := builtinplugins.Source("yunqiao")
	require.NoError(t, err)
	plugin, err := jsplugin.DefaultRegistry.RegisterFactory(source, jsplugin.Options{Key: "yunqiao"})
	require.NoError(t, err)
	channels := []model.Channel{
		{Id: 35, Type: constant.ChannelTypeGrsai, Name: "Grsai", Status: common.ChannelStatusEnabled, Priority: common.GetPointer(int64(8)), Models: "minimax-h3", Group: "yihi_default", Key: "test-key"},
		{Id: 37, Type: constant.ChannelTypeTaskPlugin, Name: "Yunqiao", Status: common.ChannelStatusEnabled, Priority: common.GetPointer(int64(9)), Models: "minimax-h3", Group: "yihi_default", Key: "test-key", Setting: common.GetPointer(`{"task_plugin_key":"yunqiao"}`)},
		{Id: 38, Type: constant.ChannelTypeTaskPlugin, Name: "Unsupported", Status: common.ChannelStatusEnabled, Priority: common.GetPointer(int64(99)), Models: "minimax-h3", Group: "yihi_default", Key: "test-key", Setting: common.GetPointer(`{"task_plugin_key":"not-a-video-plugin"}`)},
	}
	for i := range channels {
		require.NoError(t, db.Create(&channels[i]).Error)
		require.NoError(t, db.Create(&model.Ability{ChannelId: channels[i].Id, Group: "yihi_default", Model: "minimax-h3", Enabled: true, Priority: channels[i].Priority}).Error)
	}
	model.InitChannelCache()
	request := `{"model":"minimax-h3","prompt":"animate the reference","duration":5,"images":["https://example.com/reference.png"],"metadata":{"resolution":"768p","aspect_ratio":"16:9","generate_audio":false,"audios":[]}}`
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", bytes.NewBufferString(request))
	c.Request.Header.Set("Content-Type", "application/json")
	service.AppendTaskPluginIdentityFilter(c, "")
	filters := service.GetChannelConstraints(c).Filters
	selected, err := model.GetRandomSatisfiedChannel("yihi_default", "minimax-h3", 0, filters)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, 37, selected.Id, "plugin joins the normal priority selection")
	native, err := model.GetRandomSatisfiedChannel("yihi_default", "minimax-h3", 1, filters)
	require.NoError(t, err)
	require.NotNil(t, native)
	assert.Equal(t, 35, native.Id, "native fallback remains eligible")
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, selected, "minimax-h3"))
	assert.Equal(t, constant.TaskPlatform("yunqiao"), relay.GetTaskPlatform(c))
	info := &relaycommon.RelayInfo{OriginModelName: "minimax-h3", ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeTaskPlugin, UpstreamModelName: "minimax-h3", ChannelBaseUrl: "https://provider.example", ApiKey: "test-key"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	adaptor := taskplugin.New(plugin)
	adaptor.Init(info)
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	assert.Equal(t, "image_to_video", info.Action)
	reader, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	var outbound map[string]any
	require.NoError(t, common.DecodeJson(reader, &outbound))
	assert.Equal(t, map[string]any{
		"model": "minimax-h3", "prompt": "animate the reference", "seconds": float64(5), "input_reference": "https://example.com/reference.png",
		"resolution": "768p", "ratio": "16:9", "generate_audio": false,
		"metadata": map[string]any{"resolution": "768p", "aspect_ratio": "16:9", "generate_audio": false, "audios": []any{}},
	}, outbound)
	facts, err := adaptor.ExtractUsageFactsValidated(c, info)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"seconds": float64(5), "resolution": "768p", "video_input": "none"}, facts)
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "multiple images", body: `{"images":["https://example.com/one.png","https://example.com/two.png"]}`},
		{name: "conflicting references", body: `{"images":["https://example.com/one.png"],"input_reference":"https://example.com/two.png"}`},
		{name: "reference audio", body: `{"metadata":{"resolution":"768p","audios":["https://example.com/voice.mp3"]}}`},
		{name: "oversized duration", body: `{"duration":3601}`},
		{name: "conflicting duration", body: `{"duration":5,"seconds":6}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"model": "minimax-h3", "prompt": "animate the reference", "duration": 5, "metadata": map[string]any{"resolution": "768p"}}
			var overrides map[string]any
			require.NoError(t, common.UnmarshalJsonStr(tc.body, &overrides))
			for key, value := range overrides {
				body[key] = value
			}
			encoded, err := common.Marshal(body)
			require.NoError(t, err)
			invalid, _ := gin.CreateTestContext(httptest.NewRecorder())
			invalid.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", bytes.NewReader(encoded))
			invalid.Request.Header.Set("Content-Type", "application/json")
			driver := taskplugin.New(plugin)
			driver.Init(info)
			taskErr := driver.ValidateRequestAndSetAction(invalid, info)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
		})
	}

	// A retry to Grsai must not reuse the previous plugin identity.
	c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: jsplugin.DefaultRegistry.Generation(), Plugin: plugin})
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, native, "minimax-h3"))
	assert.Equal(t, constant.TaskPlatform("64"), relay.GetTaskPlatform(c))
	assert.Nil(t, service.TaskExecutionSnapshotFromContext(c).TaskPlugin)
	require.NoError(t, db.Model(&channels[0]).Update("priority", 10).Error)
	model.InitChannelCache()
	higherNative, err := model.GetRandomSatisfiedChannel("yihi_default", "minimax-h3", 0, filters)
	require.NoError(t, err)
	require.NotNil(t, higherNative)
	assert.Equal(t, 35, higherNative.Id, "native and plugin priorities share one ordering")

	require.NoError(t, db.Model(&channels[0]).Update("status", common.ChannelStatusManuallyDisabled).Error)
	model.InitChannelCache()
	selected, err = model.GetRandomSatisfiedChannel("yihi_default", "minimax-h3", 0, filters)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, 37, selected.Id, "disabling Grsai still leaves Yunqiao available")

	ordinary, _ := gin.CreateTestContext(httptest.NewRecorder())
	ordinary.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(request))
	service.AppendTaskPluginIdentityFilter(ordinary, "")
	allowed, _ := model.ChannelSatisfiesFilters(selected, "minimax-h3", service.GetChannelConstraints(ordinary).Filters)
	assert.False(t, allowed, "ordinary traffic must not select a video-only plugin")

	task := &model.Task{TaskID: "task_legacy_yunqiao", UserId: 2, Platform: "yunqiao", ChannelId: 37, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "minimax-h3"}, PrivateData: model.TaskPrivateData{UpstreamTaskID: "private-id", ResultURL: "https://example.com/video.mp4"}}
	require.NoError(t, db.Create(task).Error)
	recorder := httptest.NewRecorder()
	query, _ := gin.CreateTestContext(recorder)
	query.Request = httptest.NewRequest(http.MethodGet, "/v1/video/generations/task_legacy_yunqiao", nil)
	query.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
	query.Set("id", 2)
	require.Nil(t, relay.RelayTaskFetch(query, relayconstant.RelayModeVideoFetchByID))
	var response map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "success", response["code"])
	data := response["data"].(map[string]any)
	assert.Equal(t, "SUCCESS", data["status"])
	assert.Equal(t, "https://example.com/video.mp4", data["result_url"])
	assert.NotContains(t, data, "private_data")
}

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
