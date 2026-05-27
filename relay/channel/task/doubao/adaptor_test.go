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

func intValuePtr(v int) *dto.IntValue {
	value := dto.IntValue(v)
	return &value
}
