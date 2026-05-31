package clonefs

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAliasResolutionAndBilling(t *testing.T) {
	require.True(t, isFixedPriceModel("seedance-2.0-720p"))
	require.True(t, isFixedPriceModel("seedance-2.0-fast-480p"))

	adaptor := &TaskAdaptor{}
	body, err := adaptor.convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt:   "make a video",
		Model:    "seedance-2.0-480p",
		Duration: 8,
		Metadata: map[string]interface{}{
			"resolution": "720p",
		},
	}, "seedance-2.0-480p")
	require.NoError(t, err)
	require.Equal(t, "480p", body.Resolution)

	got, err := billingFactors(body)
	require.NoError(t, err)
	require.Equal(t, map[string]float64{
		"seconds":     8,
		"resolution":  1.0,
		"video_input": 1.0,
	}, got)
}

func TestBuildRequestBodyUsesCloneFSPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:   "make a video",
		Model:    "seedance-2.0-fast-720p",
		Duration: 10,
		Images:   []string{"data:image/png;base64,abc"},
		Metadata: map[string]interface{}{
			"ratio":          "16:9",
			"generate_audio": true,
		},
	})

	info := &relaycommon.RelayInfo{
		OriginModelName: "seedance-2.0-fast-720p",
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	adaptor := &TaskAdaptor{}
	reader, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)

	var body requestPayload
	require.NoError(t, common.Unmarshal(data, &body))
	require.Equal(t, "seedance-2.0-fast-720p", body.Model)
	require.Equal(t, "720p", body.Resolution)
	require.Equal(t, "16:9", body.Ratio)
	require.NotNil(t, body.GenerateAudio)
	require.True(t, bool(*body.GenerateAudio))
	require.Len(t, body.Content, 2)
	require.Equal(t, "text", body.Content[0].Type)
	require.Equal(t, "image_url", body.Content[1].Type)
	require.Equal(t, "first_frame", body.Content[1].Role)
	require.Equal(t, "person", body.Content[1].SubjectType)
	require.Equal(t, "seedance-2.0-fast-720p", info.UpstreamModelName)
}

func TestTaskIDAndResultParsing(t *testing.T) {
	require.Equal(t, "vid_123", submitResponse{VideoID: "vid_123"}.taskID())
	require.Equal(t, "vid_456", submitResponse{
		Data: struct {
			ID      string `json:"id"`
			VideoID string `json:"video_id"`
			TaskID  string `json:"task_id"`
		}{VideoID: "vid_456"},
	}.taskID())

	adaptor := &TaskAdaptor{}
	result, err := adaptor.ParseTaskResult([]byte(`{
		"status":"completed",
		"data":{"video_url":"https://cdn.example.com/video.mp4"}
	}`))
	require.NoError(t, err)
	require.Equal(t, string(model.TaskStatusSuccess), result.Status)
	require.Equal(t, "https://cdn.example.com/video.mp4", result.Url)

	result, err = adaptor.ParseTaskResult([]byte(`{
		"state":"failed",
		"error":"upstream failed"
	}`))
	require.NoError(t, err)
	require.Equal(t, string(model.TaskStatusFailure), result.Status)
	require.Equal(t, "upstream failed", result.Reason)
}

func TestBuildRequestURL(t *testing.T) {
	adaptor := &TaskAdaptor{baseURL: "https://www.clonefs.top/"}
	url, err := adaptor.BuildRequestURL(nil)
	require.NoError(t, err)
	require.Equal(t, "https://www.clonefs.top/v1/videos", url)
}
