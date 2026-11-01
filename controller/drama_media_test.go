package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
	"math"
	"os"
	"testing"
)

func TestDramaMediaQuoteBilling(t *testing.T) {
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	oldModes, oldExpr := settings.BillingMode, settings.BillingExpr
	t.Cleanup(func() { settings.BillingMode = oldModes; settings.BillingExpr = oldExpr })
	video := "doubao-seedance-2-0-260128"
	settings.BillingMode = map[string]string{video: billing_setting.BillingModeTieredExpr, "test-image": billing_setting.BillingModeTieredExpr, "test-token": billing_setting.BillingModeTieredExpr}
	settings.BillingExpr = map[string]string{video: `tier("video", u("seconds") * (u("video_input") == "video" ? 0.2 : 0.1))`, "test-image": `tier("image", fixed(0.04)) * image_count`, "test-token": `tier("token", p * 2 + c * 8)`}
	for _, tc := range []struct {
		reference bool
		usd       float64
	}{{false, .5}, {true, 1}} {
		item := dramaMediaItem{Model: video, Type: "video", Duration: 5, Resolution: "720p", HasReferenceVideo: tc.reference}
		quota, _, err := quoteDramaMediaChannel(item, &model.Channel{Type: constant.ChannelTypeDoubaoVideo}, 1.5)
		require.NoError(t, err)
		require.Equal(t, common.QuotaRound(tc.usd*common.QuotaPerUnit*1.5), quota)
	}
	quota, _, err := quoteDramaMediaChannel(dramaMediaItem{Model: "test-image", Type: "image", Count: 3}, &model.Channel{}, 1)
	require.NoError(t, err)
	require.Equal(t, common.QuotaRound(.12*common.QuotaPerUnit), quota)
	_, _, err = quoteDramaMediaChannel(dramaMediaItem{Model: "test-token", Type: "image"}, &model.Channel{}, 1)
	require.ErrorContains(t, err, "actual output usage")
	for _, duration := range []float64{-1, math.Inf(1), math.NaN(), .5, 3601} {
		require.Error(t, validateDramaMediaItem(dramaMediaItem{Model: video, Type: "video", Duration: duration}))
	}
}

func TestDramaMediaSeedreamRealMeter(t *testing.T) {
	source, err := os.ReadFile("../plugins/tasks/doubao/plugin.js")
	require.NoError(t, err)
	previous := jsplugin.DefaultRegistry
	jsplugin.DefaultRegistry = jsplugin.NewRegistry()
	t.Cleanup(func() { jsplugin.DefaultRegistry = previous })
	_, err = jsplugin.DefaultRegistry.Register(string(source), jsplugin.Options{Key: "doubao", Version: "quote-test"})
	require.NoError(t, err)
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	oldModes, oldExpr := settings.BillingMode, settings.BillingExpr
	t.Cleanup(func() { settings.BillingMode = oldModes; settings.BillingExpr = oldExpr })
	name := "doubao-seedream-5-0-pro-260628"
	settings.BillingMode = map[string]string{name: billing_setting.BillingModeTieredExpr}
	settings.BillingExpr = map[string]string{name: `tier("image", u("images_above_1_5k") * 0.08 + u("images_up_to_1_5k") * 0.04 + u("input_images") * 0.01)`}
	count := 1
	channel := &model.Channel{Setting: common.GetPointer(`{"task_plugin_key":"doubao"}`)}
	quota, _, err := quoteDramaMediaChannel(dramaMediaItem{Model: name, Type: "image", Count: 1, Size: "2048x2048", ReferenceImageCount: &count}, channel, 1)
	require.NoError(t, err)
	require.Equal(t, common.QuotaRound(.09*common.QuotaPerUnit), quota)
	_, _, err = quoteDramaMediaChannel(dramaMediaItem{Model: name, Type: "image", Count: 1, Size: "2048x2048"}, channel, 1)
	require.ErrorContains(t, err, "reference image count")
}
