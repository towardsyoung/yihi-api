package controller

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
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

func TestDramaMediaConfiguredImagePricesWithPluginChannel(t *testing.T) {
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	previous := *settings
	prices, err := common.Marshal(ratio_setting.GetModelPriceCopy())
	require.NoError(t, err)
	t.Cleanup(func() {
		*settings = previous
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(prices)))
	})
	*settings = billing_setting.BillingSetting{
		BillingMode: map[string]string{
			"gpt-image-2.5":          billing_setting.BillingModeTieredExpr,
			"gpt-image-2.5-flare":    billing_setting.BillingModeTieredExpr,
			"gpt-image-2.5-sunburst": billing_setting.BillingModeTieredExpr,
		},
		BillingExpr: map[string]string{
			"gpt-image-2.5":          `tier("image", fixed(0.031)) * image_count`,
			"gpt-image-2.5-flare":    `tier("request", fixed(0.031))`,
			"gpt-image-2.5-sunburst": `tier("request", fixed(0.031))`,
		},
		PluginBillingExpr: map[string]string{},
	}
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"nano-banana-2.1":0.062}`))
	channel := &model.Channel{Id: 12, Setting: common.GetPointer(`{"task_plugin_key":"image-provider"}`)}
	for _, tc := range []struct {
		model string
		count int
		usd   float64
	}{
		{"gpt-image-2.5", 3, .093},
		{"gpt-image-2.5-flare", 3, .031},
		{"gpt-image-2.5-sunburst", 3, .031},
		{"nano-banana-2.1", 1, .062},
	} {
		for _, resolution := range []string{"1K", "2K", "4K"} {
			t.Run(tc.model+"/"+resolution, func(t *testing.T) {
				quota, _, err := quoteDramaMediaChannel(dramaMediaItem{Type: "image", Model: tc.model, Count: tc.count, Resolution: resolution, Size: "2048x2048"}, channel, .7)
				require.NoError(t, err)
				require.Equal(t, common.QuotaRound(tc.usd*common.QuotaPerUnit*.7), quota)
			})
		}
	}
	item := dramaMediaItem{Type: "image", Model: "gpt-image-2.5-flare", Count: 1, Size: "1024x1024"}
	_, oldVersion, err := quoteDramaMediaChannel(item, channel, 1)
	require.NoError(t, err)
	settings.BillingExpr[item.Model] = `tier("request", fixed(0.045))`
	quota, newVersion, err := quoteDramaMediaChannel(item, channel, 1)
	require.NoError(t, err)
	require.Equal(t, common.QuotaRound(.045*common.QuotaPerUnit), quota)
	require.NotEqual(t, oldVersion, newVersion)
	settings.BillingExpr[item.Model] = `tier("request", fixed(0.031)) + c`
	_, _, err = quoteDramaMediaChannel(item, channel, 1)
	require.Error(t, err)
	settings.BillingExpr[item.Model] = `tier("request", fixed(0.031)) * (header("Content-Type") == "application/json" ? 2 : 1)`
	_, _, err = quoteDramaMediaChannel(item, channel, 1)
	require.ErrorContains(t, err, "headers")
	settings.BillingExpr[item.Model] = `tier("request", fixed(0.031))`
	key := billing_setting.PluginBillingExprKey("image-provider", item.Model)
	settings.PluginBillingExpr[key] = `tier("request", fixed(0.07))`
	quota, _, err = quoteDramaMediaChannel(item, channel, 1)
	require.NoError(t, err)
	require.Equal(t, common.QuotaRound(.07*common.QuotaPerUnit), quota)
	settings.PluginBillingExpr[key] = `tier("usage", u("outputs") * 0.07)`
	_, _, err = quoteDramaMediaChannel(item, channel, 1)
	require.Error(t, err, "usage pricing must not fall back to a fixed model price")
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

func TestDramaMediaGrsaiH3Quote(t *testing.T) {
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	previous := *settings
	prices, err := common.Marshal(ratio_setting.GetModelPriceCopy())
	require.NoError(t, err)
	t.Cleanup(func() {
		*settings = previous
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(prices)))
	})
	const name = "minimax-h3"
	const expression = `tier("video", u("seconds") * (u("resolution") == "480p" ? 0.1 : u("resolution") == "768p" ? 0.14 : 0.3) * (u("video_input") == "none" ? 1 : 1.65))`
	*settings = billing_setting.BillingSetting{
		BillingMode:       map[string]string{name: billing_setting.BillingModeTieredExpr},
		BillingExpr:       map[string]string{name: expression},
		PluginBillingExpr: map[string]string{"hailuo::minimax-h3": `tier("other-provider", 99)`},
	}
	channel := &model.Channel{Id: 64, Type: constant.ChannelTypeGrsai, Setting: common.GetPointer(`{"task_plugin_key":"hailuo"}`)}
	for _, tc := range []struct {
		resolution string
		duration   float64
		unit       float64
	}{
		{"480p", 1, .1}, {"480p", 15, .1}, {"768p", 15, .14}, {"1080p", 10, .3}, {"768P", 5, .14},
	} {
		t.Run(fmt.Sprintf("%s/%g", tc.resolution, tc.duration), func(t *testing.T) {
			quota, _, err := quoteDramaMediaChannel(dramaMediaItem{Type: "video", Model: name, Resolution: tc.resolution, Duration: tc.duration, AspectRatio: "9:16"}, channel, .7)
			require.NoError(t, err)
			require.Equal(t, common.QuotaRound(tc.duration*tc.unit*common.QuotaPerUnit*.7), quota)
		})
	}
	item := dramaMediaItem{Type: "video", Model: name, Resolution: "768p", Duration: 5, AspectRatio: "16:9"}
	for _, invalid := range []dramaMediaItem{
		{Type: "video", Model: name, Resolution: "1080p", Duration: 11},
		{Type: "video", Model: name, Resolution: "480p", Duration: 16},
		{Type: "video", Model: name, Resolution: "768p", Duration: .5},
		{Type: "video", Model: name, Resolution: "2K", Duration: 5},
		{Type: "video", Model: name, Resolution: "768p", Duration: 5, AspectRatio: "1:1"},
		{Type: "video", Model: name, Resolution: "768p", Duration: 5, HasReferenceVideo: true},
	} {
		_, _, err := quoteDramaMediaChannel(invalid, channel, 1)
		require.Error(t, err)
	}
	_, _, err = quoteDramaMediaChannel(item, &model.Channel{Type: constant.ChannelTypeMiniMax}, 1)
	require.ErrorContains(t, err, "meter is unavailable", "Hailuo must not use the Grsai contract")
	alias := "h3-grsai-alias"
	settings.BillingMode[alias] = billing_setting.BillingModeTieredExpr
	settings.BillingExpr[alias] = expression
	aliasChannel := &model.Channel{Type: constant.ChannelTypeGrsai, ModelMapping: common.GetPointer(`{"h3-grsai-alias":"minimax-h3"}`)}
	item.Model = alias
	_, _, err = quoteDramaMediaChannel(item, aliasChannel, 1)
	require.NoError(t, err)
	item.Model = name
	settings.BillingMode[name] = billing_setting.BillingModeRatio
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"minimax-h3":0.0000017}`))
	quota, _, err := quoteDramaMediaChannel(item, channel, 1.37)
	require.NoError(t, err)
	base, err := common.QuotaFromFloatStrict(.0000017 * common.QuotaPerUnit * 1.37)
	require.NoError(t, err)
	require.Equal(t, base*5*2, quota, "legacy billing truncates before duration/resolution multipliers")
	settings.BillingMode[name] = billing_setting.BillingModeTieredExpr
	settings.BillingExpr[name] = `tier("wrong", u("input_video_seconds"))`
	_, _, err = quoteDramaMediaChannel(item, channel, 1)
	require.ErrorContains(t, err, "unknown usage")
}

func TestDramaMediaOpenAIH3Quote(t *testing.T) {
	prices, err := common.Marshal(ratio_setting.GetModelPriceCopy())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(prices))) })
	source, err := os.ReadFile("../plugins/tasks/sora/plugin.js")
	require.NoError(t, err)
	previousRegistry := jsplugin.DefaultRegistry
	jsplugin.DefaultRegistry = jsplugin.NewRegistry()
	t.Cleanup(func() { jsplugin.DefaultRegistry = previousRegistry })
	_, err = jsplugin.DefaultRegistry.Register(string(source), jsplugin.Options{})
	require.NoError(t, err)
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	previous := *settings
	t.Cleanup(func() { *settings = previous })
	const name = "minimax-h3"
	*settings = billing_setting.BillingSetting{
		BillingMode:       map[string]string{name: billing_setting.BillingModeTieredExpr},
		BillingExpr:       map[string]string{name: `tier("video", u("seconds") * (u("resolution") == "480p" ? 0.1 : u("resolution") == "768p" ? 0.14 : 0.3))`},
		PluginBillingExpr: map[string]string{},
	}
	proxy := &model.Channel{Id: 24, Type: constant.ChannelTypeOpenAI}
	native := &model.Channel{Id: 35, Type: constant.ChannelTypeGrsai}
	for _, resolution := range []string{"480p", "768p", "1080p"} {
		item := dramaMediaItem{Type: "video", Model: name, Duration: 1, Resolution: resolution, AspectRatio: "16:9"}
		proxyQuota, _, err := quoteDramaMediaChannel(item, proxy, .7)
		require.NoError(t, err)
		nativeQuota, _, err := quoteDramaMediaChannel(item, native, .7)
		require.NoError(t, err)
		require.Equal(t, nativeQuota, proxyQuota)
	}
	key := billing_setting.PluginBillingExprKey("sora", name)
	settings.PluginBillingExpr[key] = `tier("proxy", u("seconds") * 0.09)`
	item := dramaMediaItem{Type: "video", Model: name, Duration: 1, Resolution: "768p"}
	quota, _, err := quoteDramaMediaChannel(item, proxy, 1)
	require.NoError(t, err)
	require.Equal(t, common.QuotaRound(.09*common.QuotaPerUnit), quota)
	item.Duration = 11
	item.Resolution = "1080p"
	_, _, err = quoteDramaMediaChannel(item, proxy, 1)
	require.Error(t, err)
	delete(settings.PluginBillingExpr, key)
	settings.BillingMode[name] = billing_setting.BillingModeRatio
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"minimax-h3":0.01}`))
	item.Resolution, item.Duration = "768p", 5
	quota, _, err = quoteDramaMediaChannel(item, proxy, 1)
	require.NoError(t, err)
	require.Equal(t, common.QuotaRound(.01*common.QuotaPerUnit*5), quota, "OpenAI legacy price uses the driver's per-second multiplier")
}
