package billing_setting

import "github.com/QuantumNous/new-api/pkg/jsplugin"

// NativeTaskUsageSchema describes usage supplied by Go task adaptors that do
// not have a JavaScript plugin manifest.
func NativeTaskUsageSchema(model string) map[string]jsplugin.UsageFieldSchema {
	if model != "minimax-h3" {
		return nil
	}
	return map[string]jsplugin.UsageFieldSchema{
		"seconds": {
			Type: "number", Unit: "second",
			Description: jsplugin.LocalizedText{"en": "Video generation unit price", "zh": "视频生成单价"},
		},
		"resolution": {
			Enum:        []string{"480p", "768p", "1080p"},
			Description: jsplugin.LocalizedText{"en": "Output video resolution", "zh": "输出视频分辨率"},
		},
		// GrsAI's generate request accepts images and audios, but no reference
		// video. Keep this fact for expressions shared with other video models.
		"video_input": {
			Enum:        []string{"none"},
			Description: jsplugin.LocalizedText{"en": "Reference video input", "zh": "参考视频输入"},
			EnumLabels:  map[string]jsplugin.LocalizedText{"none": {"en": "No reference video", "zh": "无参考视频"}},
		},
	}
}
