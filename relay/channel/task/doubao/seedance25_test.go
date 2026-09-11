package doubao

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeedance25RequestAndBilling(t *testing.T) {
	const modelName = "doubao-seedance-2-5-260628"
	adaptor := &TaskAdaptor{}
	assert.Contains(t, adaptor.GetModelList(), modelName)

	for _, tt := range []struct {
		resolution string
		ratio      float64
	}{
		{"480p", 1},
		{"720p", 1.364},
		{"1080p", 3.27},
	} {
		t.Run(tt.resolution, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("task_request", relaycommon.TaskSubmitReq{
				Model: modelName, Prompt: "a moving landscape", Duration: 10,
				Metadata: map[string]interface{}{"resolution": tt.resolution},
			})
			info := &relaycommon.RelayInfo{
				OriginModelName: modelName,
				ChannelMeta:     &relaycommon.ChannelMeta{},
			}
			assert.Equal(t, map[string]float64{
				"seconds": 10, "resolution": tt.ratio, "video_input": 1,
			}, adaptor.EstimateBilling(c, info))

			body, err := adaptor.BuildRequestBody(c, info)
			require.NoError(t, err)
			data, err := io.ReadAll(body)
			require.NoError(t, err)
			var payload requestPayload
			require.NoError(t, common.Unmarshal(data, &payload))
			assert.Equal(t, modelName, payload.Model)
			assert.Equal(t, tt.resolution, payload.Resolution)
			require.NotNil(t, payload.Duration)
			assert.Equal(t, 10, int(*payload.Duration))

			req, err := relaycommon.GetTaskRequest(c)
			require.NoError(t, err)
			req.Metadata["content"] = []ContentItem{
				{Type: "video_url", VideoURL: &MediaURL{URL: "https://example.com/reference.mp4"}},
			}
			c.Set("task_request", req)
			assert.Equal(t, map[string]float64{
				"seconds": 10, "resolution": tt.ratio, "video_input": 1.65,
			}, adaptor.EstimateBilling(c, info))
		})
	}
}
