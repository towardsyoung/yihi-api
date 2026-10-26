package doubao

import (
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeedance25RequestAndBilling(t *testing.T) {
	for _, tc := range []struct {
		name, resolution string
		cheap, video     bool
		price, want      float64
	}{
		{"original 720p", "720p", false, false, .15, 1.5},
		{"cheap 720p", "720p", true, false, .12, 1.2},
		{"cheap 720p reference video", "720p", true, true, .12, 1.98},
		{"cheap 480p direct", "480p", true, false, .1, 1},
		{"cheap 1080p", "1080p", true, false, .22, 2.2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modelName := "doubao-seedance-2-5-260628"
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
			sourceResolution := tc.resolution
			if tc.cheap {
				modelName = "doubao-seedance-2-5-cheap"
				target := tc.resolution
				sourceResolution = "480p"
				if target == "480p" {
					target = ""
				}
				if target == "1080p" {
					sourceResolution = "720p"
				}
				info.ChannelOtherSettings.SeedanceUpscale = map[string]dto.SeedanceUpscaleModelConfig{
					modelName: {Enabled: true, MapModel: "doubao-seedance-2-5-260628", Rules: map[string]dto.SeedanceUpscaleRuleConfig{
						tc.resolution: {SeedanceResolution: sourceResolution, UpscaleResolution: target},
					}, Upscale: dto.SeedanceUpscaleProviderConfig{APIKey: "test-key"}},
				}
			}
			info.OriginModelName = modelName
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/videos", nil)
			metadata := map[string]any{"resolution": tc.resolution}
			if tc.video {
				metadata["content"] = []ContentItem{{Type: "video_url", VideoURL: &MediaURL{URL: "https://example.com/ref.mp4"}}}
			}
			// The protocol decoder supplies numeric seconds and a canonical request map.
			c.Set("task_request", map[string]any{"model": modelName, "prompt": "a landscape", "seconds": 10, "metadata": metadata})
			adaptor := &TaskAdaptor{}
			require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
			assert.True(t, adaptor.RequiresOwnBillingExpression(info))
			assert.Nil(t, adaptor.EstimateBilling(c, info), "expression pricing must not apply legacy multipliers")
			facts, err := adaptor.ExtractUsageFactsValidated(c, info)
			require.NoError(t, err)
			assert.Equal(t, tc.resolution, facts["resolution"])
			assert.Equal(t, float64(10), facts["seconds"])
			expression := `tier("video", u("seconds") * PRICE) * (u("video_input") == "video" ? 1.65 : 1)`
			expression = strings.ReplaceAll(expression, "PRICE", strconv.FormatFloat(tc.price, 'f', -1, 64))
			cost, _, err := billingexpr.RunExprWithRequest(expression, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: facts})
			require.NoError(t, err)
			assert.InDelta(t, tc.want, cost, 1e-10)
			snapshot := &billingexpr.BillingSnapshot{ExprString: expression, ExprHash: billingexpr.ExprHashString(expression), ExprVersion: 1, TaskUsageBilling: true, UsageFacts: facts, GroupRatio: 1, QuotaPerUnit: 500000}
			body, err := adaptor.BuildRequestBody(c, info)
			require.NoError(t, err)
			data, err := io.ReadAll(body)
			require.NoError(t, err)
			var payload requestPayload
			require.NoError(t, common.Unmarshal(data, &payload))
			assert.Equal(t, "doubao-seedance-2-5-260628", payload.Model)
			assert.Equal(t, sourceResolution, payload.Resolution)
			require.NotNil(t, payload.Duration)
			assert.Equal(t, 10, int(*payload.Duration))
			result, usage, err := service.EvaluateTaskCompletionUsage(snapshot, nil)
			require.NoError(t, err)
			assert.Equal(t, tc.resolution, usage["resolution"])
			assert.Equal(t, common.QuotaRound(tc.want*500000), result.ActualQuotaAfterGroup)
		})
	}
}

func TestSeedanceRequestBillingParameters(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]any
		valid  bool
	}{
		{"size and duration", map[string]any{"size": "1280x720", "duration": 10}, true},
		{"top level resolution", map[string]any{"resolution": "720p", "seconds": 10}, true},
		{"seconds take priority", map[string]any{"resolution": "720p", "seconds": 10, "duration": 20}, true},
		{"missing duration", map[string]any{"resolution": "720p"}, false},
		{"negative seconds", map[string]any{"resolution": "720p", "seconds": -1}, false},
		{"fractional seconds", map[string]any{"resolution": "720p", "seconds": 1.5}, false},
		{"oversized seconds", map[string]any{"resolution": "720p", "seconds": 3601}, false},
		{"invalid size", map[string]any{"size": "fooxbar", "seconds": 10}, false},
		{"frames override", map[string]any{"seconds": 10, "metadata": map[string]any{"resolution": "720p", "frames": 240}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/videos", nil)
			tc.fields["model"] = "doubao-seedance-2-5-260628"
			c.Set("task_request", tc.fields)
			info := &relaycommon.RelayInfo{OriginModelName: "doubao-seedance-2-5-260628", ChannelMeta: &relaycommon.ChannelMeta{}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
			a := &TaskAdaptor{}
			validation := a.ValidateRequestAndSetAction(c, info)
			if !tc.valid {
				require.NotNil(t, validation)
				assert.Equal(t, 400, validation.StatusCode)
				return
			}
			require.Nil(t, validation)
			facts, err := a.ExtractUsageFactsValidated(c, info)
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"seconds": float64(10), "resolution": "720p", "video_input": "none"}, facts)
		})
	}
}
