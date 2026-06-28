package doubao

import "strings"

var ModelList = []string{
	"doubao-seedance-1-0-pro-250528",
	"doubao-seedance-1-0-lite-t2v",
	"doubao-seedance-1-0-lite-i2v",
	"doubao-seedance-1-5-pro-251215",
	"doubao-seedance-2-0-260128",
	"doubao-seedance-2-0-fast-260128",
}

var ChannelName = "doubao-video"

const (
	seedanceResolution480pRatio = 1.0
	seedanceResolution720pRatio = 2.2
	seedanceNoVideoInputRatio   = 1.0
	seedanceHasVideoInputRatio  = 1.65

	seedanceUpscaleStageSeedance     = "seedance"
	seedanceUpscaleStageUpscale      = "upscale"
	seedanceUpscaleProgress          = "70%"
	seedanceUpscaleDefaultMaxRetries = 3
	seedanceUpscaleTaskPrefix        = "amk-tool-enhance-video-generative-"
	seedanceUpscaleBaseURL           = "https://mediakit.cn-beijing.volces.com"
)

var seedanceFixedPriceModels = map[string]bool{
	"doubao-seedance-2-0-260128":      true,
	"doubao-seedance-2-0-fast-260128": true,
}

func IsSeedanceFixedPriceModel(modelName string) bool {
	return seedanceFixedPriceModels[modelName]
}

func NormalizeSeedanceResolution(resolution string) (string, float64, bool) {
	normalized := strings.ToLower(strings.TrimSpace(resolution))
	switch normalized {
	case "480p":
		return normalized, seedanceResolution480pRatio, true
	case "720p":
		return normalized, seedanceResolution720pRatio, true
	default:
		return normalized, 0, false
	}
}

func GetSeedanceVideoInputRatio(hasVideoInput bool) float64 {
	if hasVideoInput {
		return seedanceHasVideoInputRatio
	}
	return seedanceNoVideoInputRatio
}
