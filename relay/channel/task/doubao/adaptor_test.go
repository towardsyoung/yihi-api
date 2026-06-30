package doubao

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestSeedanceBillingFactors(t *testing.T) {
	tests := []struct {
		name       string
		body       *requestPayload
		want       map[string]float64
		wantErr    bool
		errMessage string
	}{
		{
			name:       "rejects missing explicit duration",
			body:       &requestPayload{Resolution: "480p"},
			wantErr:    true,
			errMessage: "seedance duration is required",
		},
		{
			name:       "rejects missing explicit resolution",
			body:       &requestPayload{Duration: intValuePtr(10)},
			wantErr:    true,
			errMessage: "seedance resolution is required",
		},
		{
			name: "720p with video input",
			body: &requestPayload{
				Duration:   intValuePtr(10),
				Resolution: "720p",
				Content: []ContentItem{
					{Type: "video_url", VideoURL: &MediaURL{URL: "https://example.com/video.mp4"}},
				},
			},
			want: map[string]float64{
				"seconds":     10,
				"resolution":  2.2,
				"video_input": 1.65,
			},
		},
		{
			name: "rejects unsupported resolution",
			body: &requestPayload{
				Duration:   intValuePtr(10),
				Resolution: "1080p",
			},
			wantErr:    true,
			errMessage: "seedance resolution must be 480p or 720p",
		},
		{
			name: "rejects non-positive explicit duration",
			body: &requestPayload{
				Duration: intValuePtr(0),
			},
			wantErr:    true,
			errMessage: "seedance duration must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := seedanceBillingFactors(tt.body)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMessage)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestConvertToRequestPayloadUsesTopLevelDurationForSeedanceBilling(t *testing.T) {
	adaptor := &TaskAdaptor{}
	body, err := adaptor.convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Prompt:   "make a video",
		Model:    "doubao-seedance-2-0-fast-260128",
		Duration: 10,
		Metadata: map[string]interface{}{
			"resolution": "720p",
			"content": []interface{}{
				map[string]interface{}{
					"type": "video_url",
					"video_url": map[string]interface{}{
						"url": "https://example.com/video.mp4",
					},
				},
			},
		},
	})
	require.NoError(t, err)

	got, err := seedanceBillingFactors(body)
	require.NoError(t, err)
	require.Equal(t, map[string]float64{
		"seconds":     10,
		"resolution":  2.2,
		"video_input": 1.65,
	}, got)
}

func TestSeedanceHelpers(t *testing.T) {
	require.True(t, IsSeedanceFixedPriceModel("doubao-seedance-2-0-260128"))
	require.True(t, IsSeedanceFixedPriceModel("doubao-seedance-2-0-fast-260128"))
	require.False(t, IsSeedanceFixedPriceModel("doubao-seedance-1-5-pro-251215"))

	resolution, ratio, ok := NormalizeSeedanceResolution(" 720P ")
	require.True(t, ok)
	require.Equal(t, "720p", resolution)
	require.Equal(t, 2.2, ratio)
}

func TestConfiguredSeedanceUpscalePlan(t *testing.T) {
	info := &relaycommon.RelayInfo{
		TokenGroup: "yihi-default",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{
				SeedanceUpscale: map[string]dto.SeedanceUpscaleModelConfig{
					"doubao-seedance-2-0-260128-g1": {
						Enabled:  true,
						MapModel: "doubao-seedance-2-0-260128",
						Groups:   []string{"yihi-default", "zevi_default"},
						Rules: map[string]dto.SeedanceUpscaleRuleConfig{
							"720p": {
								SeedanceResolution: "480p",
								UpscaleResolution:  "720p",
								BillingModel:       "doubao-seedance-2-upscale-720p",
							},
							"1080p": {
								SeedanceResolution: "720p",
								UpscaleResolution:  "1080p",
								BillingModel:       "doubao-seedance-2-upscale-1080p",
							},
						},
						Upscale: dto.SeedanceUpscaleProviderConfig{
							Provider:   "doubao",
							APIKey:     "upscale-key-g1",
							MaxRetries: 2,
						},
					},
					"doubao-seedance-2-0-260128-g2": {
						Enabled:  true,
						MapModel: "ep-20260508144423-qrvlk",
						Groups:   []string{"yihi-default"},
						Rules: map[string]dto.SeedanceUpscaleRuleConfig{
							"720p": {
								SeedanceResolution: "480p",
								UpscaleResolution:  "720p",
								BillingModel:       "doubao-seedance-2-upscale-720p",
							},
						},
						Upscale: dto.SeedanceUpscaleProviderConfig{
							APIKey: "upscale-key-g2",
						},
					},
					"doubao-seedance-2-0-260128-g3": {
						Enabled:  true,
						MapModel: "ep-no-upscale",
						Groups:   []string{"yihi-default"},
						Rules: map[string]dto.SeedanceUpscaleRuleConfig{
							"480p": {
								SeedanceResolution: "480p",
								BillingModel:       "doubao-seedance-2-upscale-480p",
							},
						},
					},
				},
			},
		},
	}

	plan, ok, err := resolveConfiguredSeedanceUpscalePlan(info, "doubao-seedance-2-0-260128-g1", "720p")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "doubao-seedance-2-0-260128", plan.UpstreamModelName)
	require.Equal(t, "doubao-seedance-2-upscale-720p", plan.BillingModelName)
	require.Equal(t, "upscale-key-g1", plan.APIKey)
	require.Equal(t, "480p", plan.SourceResolution)
	require.Equal(t, "720p", plan.BillingResolution)
	require.Equal(t, "720p", plan.TargetResolution)
	require.Equal(t, 2, plan.MaxRetries)
	factors, err := seedanceBillingFactors(&requestPayload{
		Duration:   intValuePtr(6),
		Resolution: plan.BillingResolution,
	})
	require.NoError(t, err)
	require.Equal(t, 2.2, factors["resolution"])

	plan, ok, err = resolveConfiguredSeedanceUpscalePlan(info, "doubao-seedance-2-0-260128-g1", "1080p")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "720p", plan.SourceResolution)
	require.Equal(t, "720p", plan.BillingResolution)
	require.Equal(t, "1080p", plan.TargetResolution)
	require.Equal(t, "doubao-seedance-2-upscale-1080p", plan.BillingModelName)

	plan, ok, err = resolveConfiguredSeedanceUpscalePlan(info, "doubao-seedance-2-0-260128-g2", "720p")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "ep-20260508144423-qrvlk", plan.UpstreamModelName)
	require.Equal(t, "upscale-key-g2", plan.APIKey)

	plan, ok, err = resolveConfiguredSeedanceUpscalePlan(info, "doubao-seedance-2-0-260128-g3", "480p")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "ep-no-upscale", plan.UpstreamModelName)
	require.Equal(t, "doubao-seedance-2-upscale-480p", plan.BillingModelName)
	require.Equal(t, "480p", plan.SourceResolution)
	require.Equal(t, "480p", plan.BillingResolution)
	require.Empty(t, plan.TargetResolution)
	require.Empty(t, plan.APIKey)
	require.Zero(t, plan.MaxRetries)

	info.TokenGroup = "not-allowed"
	_, ok, err = resolveConfiguredSeedanceUpscalePlan(info, "doubao-seedance-2-0-260128-g1", "720p")
	require.NoError(t, err)
	require.False(t, ok)

	info.TokenGroup = "yihi-default"
	_, ok, err = resolveConfiguredSeedanceUpscalePlan(info, "doubao-seedance-2-0-260128-g1", "480p")
	require.NoError(t, err)
	require.False(t, ok)

	info.TokenGroup = "yihi-default"
	missingKeyConfig := info.ChannelMeta.ChannelOtherSettings.SeedanceUpscale["doubao-seedance-2-0-260128-g2"]
	missingKeyConfig.Upscale.APIKey = ""
	info.ChannelMeta.ChannelOtherSettings.SeedanceUpscale["doubao-seedance-2-0-260128-g2"] = missingKeyConfig
	_, ok, err = resolveConfiguredSeedanceUpscalePlan(info, "doubao-seedance-2-0-260128-g2", "720p")
	require.Error(t, err)
	require.False(t, ok)
	require.Contains(t, err.Error(), "must configure upscale.api_key")
}

func TestBuildTaskPrivateDataPatchForSeedanceUpscale(t *testing.T) {
	adaptor := &TaskAdaptor{}
	patch := adaptor.BuildTaskPrivateDataPatch(&relaycommon.RelayInfo{
		BillingModelName: "doubao-seedance-2-upscale-720p",
		VideoUpscale: &relaycommon.VideoUpscaleRelayInfo{
			Enabled:          true,
			Stage:            seedanceUpscaleStageSeedance,
			BillingModelName: "doubao-seedance-2-upscale-720p",
			APIKey:           "upscale-key",
			SourceResolution: "480p",
			TargetResolution: "720p",
			MaxRetries:       2,
		},
	}, "seedance-task-id", nil)

	require.NotNil(t, patch.Upscale)
	require.True(t, patch.Upscale.Enabled)
	require.Equal(t, seedanceUpscaleStageSeedance, patch.Upscale.Stage)
	require.Equal(t, "doubao-seedance-2-upscale-720p", patch.Upscale.BillingModelName)
	require.Equal(t, "upscale-key", patch.Upscale.APIKey)
	require.Equal(t, "seedance-task-id", patch.Upscale.SeedanceTaskID)
	require.Equal(t, "480p", patch.Upscale.SourceResolution)
	require.Equal(t, "720p", patch.Upscale.TargetResolution)
	require.Equal(t, 2, patch.Upscale.MaxRetries)

	info := &relaycommon.RelayInfo{}
	applySeedanceUpscalePlan(info, seedanceUpscalePlan{
		BillingModelName: "doubao-seedance-2-upscale-480p",
		SourceResolution: "480p",
	})
	require.Equal(t, "doubao-seedance-2-upscale-480p", info.BillingModelName)
	require.Nil(t, info.VideoUpscale)
	require.Nil(t, adaptor.BuildTaskPrivateDataPatch(info, "seedance-task-id", nil).Upscale)
}

func TestParseUpscaleTaskResult(t *testing.T) {
	completed := []byte(`{"success":true,"task_id":"amk-tool-enhance-video-1","task_type":"enhance-video","status":"completed","result":{"resolution":"720p","video_url":"https://example.com/upscaled.mp4"}}`)
	taskResult, ok, err := parseUpscaleTaskResult(completed)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "SUCCESS", taskResult.Status)
	require.Equal(t, "https://example.com/upscaled.mp4", taskResult.Url)

	legacyRunning := []byte(`{"success":true,"task_id":"amk-tool-enhance-video-generative-1","task_type":"enhance-video-generative","status":"running"}`)
	taskResult, ok, err = parseUpscaleTaskResult(legacyRunning)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "IN_PROGRESS", taskResult.Status)
	require.Equal(t, seedanceUpscaleProgress, taskResult.Progress)

	failed := []byte(`{"success":true,"task_id":"amk-tool-enhance-video-1","task_type":"enhance-video","status":"failed","message":"bad video"}`)
	taskResult, ok, err = parseUpscaleTaskResult(failed)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "FAILURE", taskResult.Status)
	require.Equal(t, "bad video", taskResult.Reason)
}

func intValuePtr(v int) *dto.IntValue {
	value := dto.IntValue(v)
	return &value
}
