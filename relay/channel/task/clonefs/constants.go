package clonefs

import "strings"

var ModelList = []string{
	"seedance-2.0-480p",
	"seedance-2.0-720p",
	"seedance-2.0-fast-480p",
	"seedance-2.0-fast-720p",
	"doubao-seedance-2-0-260128",
	"doubao-seedance-2-0-fast-260128",
}

var ChannelName = "clonefs"

const (
	resolution480pRatio = 1.0
	resolution720pRatio = 2.2
	noVideoInputRatio   = 1.0
	hasVideoInputRatio  = 1.65
)

var fixedPriceModels = map[string]bool{
	"seedance-2.0-480p":               true,
	"seedance-2.0-720p":               true,
	"seedance-2.0-fast-480p":          true,
	"seedance-2.0-fast-720p":          true,
	"doubao-seedance-2-0-260128":      true,
	"doubao-seedance-2-0-fast-260128": true,
}

func isFixedPriceModel(modelName string) bool {
	return fixedPriceModels[strings.ToLower(strings.TrimSpace(modelName))]
}

func normalizeResolution(resolution string) (string, float64, bool) {
	normalized := strings.ToLower(strings.TrimSpace(resolution))
	switch normalized {
	case "480p":
		return normalized, resolution480pRatio, true
	case "720p":
		return normalized, resolution720pRatio, true
	default:
		return normalized, 0, false
	}
}

func videoInputRatio(hasVideoInput bool) float64 {
	if hasVideoInput {
		return hasVideoInputRatio
	}
	return noVideoInputRatio
}

func aliasResolution(modelName string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(modelName))
	switch {
	case strings.HasSuffix(normalized, "-480p"), strings.HasSuffix(normalized, "_480p"):
		return "480p", true
	case strings.HasSuffix(normalized, "-720p"), strings.HasSuffix(normalized, "_720p"):
		return "720p", true
	default:
		return "", false
	}
}
