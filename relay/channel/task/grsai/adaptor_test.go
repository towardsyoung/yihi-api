package grsai

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func taskContext(body string) (*gin.Context, *relaycommon.RelayInfo) {
	req := httptest.NewRequest(http.MethodPost, "/v1/video/generations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return c, &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
		ChannelMeta:   &relaycommon.ChannelMeta{},
	}
}

func TestH3ValidationAndBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adaptor := &TaskAdaptor{}
	tests := []struct {
		resolution string
		duration   int
		ratio      float64
		priceUSD   float64
		valid      bool
	}{
		{"480p", 15, 1, 3, true},
		{"768p", 15, 2, 6, true},
		{"1080p", 10, 3, 6, true},
		{"1080p", 11, 0, 0, false},
		{"768p", 16, 0, 0, false},
		{"720p", 5, 0, 0, false},
	}
	for _, tc := range tests {
		body := fmt.Sprintf(`{"model":"minimax-h3","prompt":"a shot","duration":%d,"metadata":{"resolution":%q,"aspect_ratio":"16:9"}}`, tc.duration, tc.resolution)
		c, info := taskContext(body)
		err := adaptor.ValidateRequestAndSetAction(c, info)
		if !tc.valid {
			require.NotNil(t, err, "%s %ds", tc.resolution, tc.duration)
			continue
		}
		require.Nil(t, err, "%s %ds", tc.resolution, tc.duration)
		factors := adaptor.EstimateBilling(c, info)
		require.Equal(t, float64(tc.duration), factors["seconds"])
		require.Equal(t, tc.ratio, factors["resolution"])
		basePrice, ok := ratio_setting.GetDefaultModelPriceMap()[modelName]
		require.True(t, ok)
		require.InDelta(t, tc.priceUSD, basePrice*factors["seconds"]*factors["resolution"], 1e-9)
	}
}

func TestH3PayloadAndTaskResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adaptor := &TaskAdaptor{}
	c, info := taskContext(`{"model":"minimax-h3","prompt":"a shot","duration":5,"images":["https://example.com/frame.png"],"metadata":{"resolution":"768p","aspect_ratio":"9:16","audios":["https://example.com/sound.mp3"]}}`)
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	info.UpstreamModelName = modelName
	reader, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"minimax-h3","prompt":"a shot","aspectRatio":"portrait","resolution":"768p","duration":5,"images":["https://example.com/frame.png"],"audios":["https://example.com/sound.mp3"],"replyType":"async"}`, string(data))

	result, err := adaptor.ParseTaskResult(nil, nil, []byte(`{"status":"succeeded","results":[{"url":"https://example.com/video.mp4"}]}`))
	require.NoError(t, err)
	require.Equal(t, string(model.TaskStatusSuccess), result.Status)
	require.Equal(t, "https://example.com/video.mp4", result.Url)

	result, err = adaptor.ParseTaskResult(nil, nil, []byte(`{"status":"violation","error":"blocked"}`))
	require.NoError(t, err)
	require.Equal(t, string(model.TaskStatusFailure), result.Status)
	require.Equal(t, "blocked", result.Reason)
}

func TestH3OpenAIVideoInputs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name     string
		fields   string
		duration int
		images   []string
	}{
		{"seconds and input reference", `"seconds":"5","input_reference":"https://example.com/frame.png"`, 5, []string{"https://example.com/frame.png"}},
		{"input reference with duration", `"duration":5,"input_reference":"https://example.com/frame.png"`, 5, []string{"https://example.com/frame.png"}},
		{"explicit duration and images take precedence", `"duration":6,"seconds":"5","images":["https://example.com/explicit.png"],"input_reference":"https://example.com/frame.png"`, 6, []string{"https://example.com/explicit.png"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, info := taskContext(fmt.Sprintf(`{"model":"minimax-h3","prompt":"a shot",%s,"metadata":{"resolution":"768p","aspect_ratio":"16:9"}}`, tc.fields))
			c.Request.URL.Path = "/v1/videos"
			adaptor := &TaskAdaptor{}
			require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
			info.UpstreamModelName = modelName
			reader, err := adaptor.BuildRequestBody(c, info)
			require.NoError(t, err)
			var payload generateRequest
			require.NoError(t, common.DecodeJson(reader, &payload))
			assert.Equal(t, tc.duration, payload.Duration)
			assert.Equal(t, tc.images, payload.Images)
			assert.Equal(t, map[string]float64{"seconds": float64(tc.duration), "resolution": 2}, adaptor.EstimateBilling(c, info))
		})
	}
}

func TestH3RejectsInvalidSeconds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, seconds := range []string{"invalid", "1.5", "0", "-1", "16", "18446744073686646784"} {
		t.Run(seconds, func(t *testing.T) {
			c, info := taskContext(fmt.Sprintf(`{"model":"minimax-h3","prompt":"a shot","seconds":%q,"metadata":{"resolution":"768p","aspect_ratio":"16:9"}}`, seconds))
			err := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info)
			require.NotNil(t, err)
			assert.Equal(t, http.StatusBadRequest, err.StatusCode)
		})
	}
}

func TestH3SubmitAndQueryEndpoints(t *testing.T) {
	service.InitHttpClient()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/api/result", r.URL.Path)
		assert.Equal(t, "task/one", r.URL.Query().Get("id"))
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"status":"running"}`))
	}))
	defer server.Close()

	adaptor := &TaskAdaptor{baseURL: server.URL}
	endpoint, err := adaptor.BuildRequestURL(nil)
	require.NoError(t, err)
	require.Equal(t, server.URL+"/v1/api/generate", endpoint)
	resp, err := adaptor.FetchTask(server.URL, "test-key", &model.Task{TaskID: "task/one"}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	result, err := adaptor.ParseTaskResult(nil, nil, data)
	require.NoError(t, err)
	require.Equal(t, string(model.TaskStatusInProgress), result.Status)
}
