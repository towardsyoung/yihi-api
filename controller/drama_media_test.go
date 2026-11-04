package controller

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDramaMediaGenericPluginMeter(t *testing.T) {
	previousRegistry := jsplugin.DefaultRegistry
	jsplugin.DefaultRegistry = jsplugin.NewRegistry()
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	previousSettings := *settings
	prices, err := common.Marshal(ratio_setting.GetModelPriceCopy())
	require.NoError(t, err)
	t.Cleanup(func() {
		jsplugin.DefaultRegistry = previousRegistry
		*settings = previousSettings
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(prices)))
	})
	// Neither the provider nor these model names appear in the quote host.
	_, err = jsplugin.DefaultRegistry.RegisterFactory(`
export const meta = {apiVersion:1,key:"quote-fixture",name:"Quote",version:"1.0.0",author:{name:"test"},
 models:["new-video","new-image"], fetchMode:"per_task", upstreams:["new_api"],
 usageSchema:{seconds:{type:"number",unit:"second"}, resolution:{enum:["480p","768p","1080p"]}, output:{type:"number",unit:"count"}, audio:{type:"boolean"}, mode:{enum:["std","fast"]}},
 usageProfiles:[{models:["new-image"],schema:{images:{type:"number",unit:"count"}}}],
 protocols:["openai_video","openai_image"]};
export function buildSubmitRequest(ctx) {return {url:ctx.baseUrl+"/jobs",method:"POST",body:ctx.requestBody};}
export function parseSubmitResponse() {return {taskId:"upstream"};}
export function buildQueryRequest() {return {url:"https://unused.invalid/jobs/1",method:"GET"};}
export function parseTaskResult() {return {status:"IN_PROGRESS"};}
export function listArtifacts() {return [];}
export function buildContentRequest() {return {url:"https://unused.invalid/content",method:"GET"};}
export const protocols = {
 openai_video:{decodeRequest(ctx){if(ctx.method!=="POST"||ctx.operation!=="create"||!ctx.upstreamModel)throw new Error("missing video decode context");return {kind:"submit",model:ctx.model,action:"text_to_video",requestBody:ctx.body.value};},render(){return {}; }},
 openai_image:{decodeRequest(ctx){if(ctx.method!=="POST"||ctx.operation!=="generate")throw new Error("missing image decode context");return {kind:"submit",model:ctx.model,action:"text_to_image",requestBody:ctx.body.value};},render(){return {data:[]}; }}
};
export function extractUsage(ctx) {
 if(ctx.upstreamModel==="new-image") return {images:ctx.requestBody.n};
 return ctx.usagePurpose==="billing_ratios" ? {seconds:Number(ctx.requestBody.seconds)} :
 {seconds:Number(ctx.requestBody.seconds),resolution:ctx.requestBody.resolution,audio:ctx.requestBody.generate_audio===true,mode:ctx.requestBody.mode||"std"};
}`, jsplugin.Options{Key: "quote-fixture"})
	require.NoError(t, err)
	*settings = billing_setting.BillingSetting{
		BillingMode:       map[string]string{"new-video": billing_setting.BillingModeTieredExpr, "new-image": billing_setting.BillingModeTieredExpr},
		BillingExpr:       map[string]string{"new-video": `tier("video", u("seconds") * (u("resolution") == "768p" ? 0.2 : 0.1))`, "new-image": `tier("image", u("images") * 0.03)`},
		PluginBillingExpr: map[string]string{},
	}
	ch := &model.Channel{Id: 91, Type: constant.ChannelTypeTaskPlugin, Setting: common.GetPointer(`{"task_plugin_key":"quote-fixture"}`)}
	video := dramaMediaItem{Model: "new-video", Type: "video", Duration: 5, Resolution: "768p", AspectRatio: "16:9"}
	quota, version, err := quoteDramaMediaChannel(video, ch, .7)
	require.NoError(t, err)
	require.Equal(t, common.QuotaRound(common.QuotaPerUnit*.7), quota)
	quota, _, err = quoteDramaMediaChannel(dramaMediaItem{Model: "new-image", Type: "image", Count: 3, Size: "1024x1024"}, ch, .7)
	require.NoError(t, err)
	require.Equal(t, common.QuotaRound(.09*common.QuotaPerUnit*.7), quota)

	settings.PluginBillingExpr[billing_setting.PluginBillingExprKey("quote-fixture", video.Model)] = `tier("override", u("seconds") * 0.3)`
	quota, overrideVersion, err := quoteDramaMediaChannel(video, ch, .7)
	require.NoError(t, err)
	require.Equal(t, common.QuotaRound(1.5*common.QuotaPerUnit*.7), quota)
	require.NotEqual(t, version, overrideVersion)
	delete(settings.PluginBillingExpr, billing_setting.PluginBillingExprKey("quote-fixture", video.Model))

	// New provider parameters and explicit false survive the full quote path.
	settings.BillingExpr[video.Model] = `tier("options", u("seconds") * (u("mode") == "fast" && u("audio") == false ? 0.4 : 0.1))`
	video.Audio = common.GetPointer(false)
	video.Options = map[string]any{"mode": "fast"}
	quota, _, err = quoteDramaMediaChannel(video, ch, .7)
	require.NoError(t, err)
	require.Equal(t, common.QuotaRound(2*common.QuotaPerUnit*.7), quota)
	video.Audio, video.Options = nil, nil
	settings.BillingExpr[video.Model] = `tier("video", u("seconds") * (u("resolution") == "768p" ? 0.2 : 0.1))`
	extended := &model.Channel{Id: 92, Type: constant.ChannelTypeNewAPI, Setting: common.GetPointer(`{"task_extend_plugin_keys":["quote-fixture"]}`)}
	quota, _, err = quoteDramaMediaChannel(video, extended, .7)
	require.NoError(t, err)
	require.Equal(t, common.QuotaRound(common.QuotaPerUnit*.7), quota)

	// Alias pricing uses the final mapped provider model/profile.
	ch.ModelMapping = common.GetPointer(`{"video-alias":"new-video"}`)
	alias := video
	alias.Model = "video-alias"
	quota, _, err = quoteDramaMediaChannel(alias, ch, .7)
	require.NoError(t, err)
	require.Equal(t, common.QuotaRound(common.QuotaPerUnit*.7), quota)
	ch.ModelMapping = nil

	for _, expr := range []string{`tier("bad", u("missing") * 0.1)`, `tier("bad", p * 0.1)`, `tier("bad", u("seconds") * (param("fast") == true ? 2 : 1))`} {
		settings.BillingExpr[video.Model] = expr
		_, _, err = quoteDramaMediaChannel(video, ch, 1)
		require.Error(t, err)
	}
	settings.BillingExpr[video.Model] = `tier("unknown", u("output") * 0.1)`
	_, _, err = quoteDramaMediaChannel(video, ch, 1)
	require.ErrorContains(t, err, "cannot estimate required usage: output")
	settings.BillingExpr[video.Model] = `tier("video", u("seconds") * 0.1)`
	invalid := video
	invalid.Resolution = "4k"
	_, _, err = quoteDramaMediaChannel(invalid, ch, 1)
	require.Error(t, err, "enum validation must match submission")
	invalid = video
	invalid.Duration = 3601
	_, _, err = quoteDramaMediaChannel(invalid, ch, 1)
	require.Error(t, err)
	invalid = video
	invalid.HasReferenceVideo = true
	_, _, err = quoteDramaMediaChannel(invalid, ch, 1)
	require.ErrorContains(t, err, "buildQuoteRequest")

	// Legacy mode must call the ratio hook, not multiply numeric usage facts
	// indiscriminately. Its truncation order matches task submission.
	settings.BillingMode = map[string]string{}
	settings.BillingExpr = map[string]string{}
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"new-video":0.010001}`))
	quota, _, err = quoteDramaMediaChannel(video, ch, .7)
	require.NoError(t, err)
	base := common.QuotaFromFloat(.010001 * common.QuotaPerUnit * .7)
	require.Equal(t, common.QuotaFromFloat(float64(base)*5), quota)

}

func TestDramaMediaSoraQuoteSize(t *testing.T) {
	source, err := os.ReadFile("../plugins/tasks/sora/plugin.js")
	require.NoError(t, err)
	previousRegistry := jsplugin.DefaultRegistry
	jsplugin.DefaultRegistry = jsplugin.NewRegistry()
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	previousSettings := *settings
	t.Cleanup(func() { jsplugin.DefaultRegistry = previousRegistry; *settings = previousSettings })
	_, err = jsplugin.DefaultRegistry.RegisterFactory(string(source), jsplugin.Options{Key: "sora"})
	require.NoError(t, err)
	*settings = billing_setting.BillingSetting{
		BillingMode: map[string]string{"sora-2-pro": billing_setting.BillingModeTieredExpr},
		BillingExpr: map[string]string{"sora-2-pro": `tier("video", u("seconds") * (u("size") == "1792x1024" ? 0.4 : 0.1))`},
	}
	ch := &model.Channel{Id: 1, Type: constant.ChannelTypeTaskPlugin, Setting: common.GetPointer(`{"task_plugin_key":"sora"}`)}
	for _, tc := range []struct {
		size string
		cost float64
	}{
		{size: "1792x1024", cost: 1.6},
		{size: "1280x720", cost: 0.4},
		{size: "", cost: 0.4},
	} {
		t.Run(tc.size, func(t *testing.T) {
			quota, _, err := quoteDramaMediaChannel(dramaMediaItem{Model: "sora-2-pro", Type: "video", Duration: 4, Size: tc.size}, ch, 1)
			require.NoError(t, err)
			assert.Equal(t, common.QuotaRound(tc.cost*common.QuotaPerUnit), quota)
		})
	}
}

func TestDramaMediaIncompleteChannelsAndRange(t *testing.T) {
	db := setupDramaExternalTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	token := seedDramaToken(t, db)
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	previous := *settings
	t.Cleanup(func() { *settings = previous })
	*settings = billing_setting.BillingSetting{
		BillingMode:       map[string]string{"quoted-image": billing_setting.BillingModeTieredExpr},
		BillingExpr:       map[string]string{"quoted-image": `tier("image", fixed(0.2)) * image_count`},
		PluginBillingExpr: map[string]string{billing_setting.PluginBillingExprKey("other-price", "quoted-image"): `tier("image", fixed(0.4)) * image_count`},
	}
	channels := []model.Channel{
		{Name: "priced", Key: "never-sent", Status: common.ChannelStatusEnabled},
		{Name: "incomplete", Key: "never-sent", Status: common.ChannelStatusEnabled, ParamOverride: common.GetPointer(`{"seconds":10}`), Setting: common.GetPointer(`{"task_plugin_key":"other-price"}`)},
	}
	for i := range channels {
		require.NoError(t, db.Create(&channels[i]).Error)
		require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "quoted-image", ChannelId: channels[i].Id, Enabled: true}).Error)
	}
	call := func() map[string]any {
		t.Helper()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(token.Id)}}
		c.Request = httptest.NewRequest(http.MethodPost, "/quote", strings.NewReader(`{"items":[{"model":"quoted-image","type":"image","count":1}]}`))
		c.Request.Header.Set("Content-Type", "application/json")
		DramaMediaQuote(c)
		require.Equal(t, http.StatusOK, recorder.Code)
		var response map[string]any
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		require.Equal(t, true, response["success"])
		return response["data"].(map[string]any)["items"].([]any)[0].(map[string]any)
	}
	partial := call()
	require.Equal(t, "unavailable", partial["status"])
	require.Equal(t, false, partial["complete"])
	require.NotContains(t, partial, "rawQuota")
	require.NotContains(t, partial, "minQuota")
	require.Len(t, partial["channelErrors"], 1)
	require.Equal(t, float64(1), partial["partialQuote"].(map[string]any)["channelCount"])
	require.NoError(t, db.Model(&channels[1]).Update("param_override", nil).Error)
	complete := call()
	require.Equal(t, "range", complete["status"])
	require.Equal(t, true, complete["complete"])
	require.NotContains(t, complete, "channelErrors")
	require.NotContains(t, complete, "partialQuote")
	require.Equal(t, complete["minQuota"].(float64)*2, complete["maxQuota"])
	require.NotEqual(t, partial["pricingVersion"], complete["pricingVersion"])
	var unchanged model.Token
	require.NoError(t, db.First(&unchanged, token.Id).Error)
	require.Equal(t, token.RemainQuota, unchanged.RemainQuota)
	require.Equal(t, token.UsedQuota, unchanged.UsedQuota)
}

func TestDramaMediaYunqiaoQuoteMatchesSubmission(t *testing.T) {
	source, err := os.ReadFile("../plugins/tasks/yunqiao/plugin.js")
	require.NoError(t, err)
	previous := jsplugin.DefaultRegistry
	jsplugin.DefaultRegistry = jsplugin.NewRegistry()
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	previousSettings := *settings
	t.Cleanup(func() { jsplugin.DefaultRegistry = previous; *settings = previousSettings })
	plugin, err := jsplugin.DefaultRegistry.RegisterFactory(string(source), jsplugin.Options{Key: "yunqiao"})
	require.NoError(t, err)
	*settings = billing_setting.BillingSetting{
		BillingMode: map[string]string{"minimax-h3": billing_setting.BillingModeTieredExpr},
		BillingExpr: map[string]string{"minimax-h3": `tier("H3", u("seconds") * (u("resolution") == "768p" ? 0.14 : 0.3) * (u("video_input") == "video" ? 1.65 : 1))`},
	}
	ch := &model.Channel{Id: 37, Type: constant.ChannelTypeTaskPlugin, Setting: common.GetPointer(`{"task_plugin_key":"yunqiao"}`)}
	for _, resolution := range []string{"480p", "768p", "1080p"} {
		t.Run(resolution, func(t *testing.T) {
			item := dramaMediaItem{Model: "minimax-h3", Type: "video", Duration: 5, Resolution: resolution, AspectRatio: "16:9"}
			quota, _, err := quoteDramaMediaChannel(item, ch, .7)
			require.NoError(t, err)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/videos", nil)
			c.Set("task_request", map[string]any{"model": "minimax-h3", "prompt": "actual prompt", "seconds": 5, "metadata": map[string]any{"resolution": resolution, "aspect_ratio": "16:9"}})
			info := &relaycommon.RelayInfo{OriginModelName: "minimax-h3", ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeTaskPlugin, UpstreamModelName: "minimax-h3", ChannelBaseUrl: "https://unused.invalid", ApiKey: "test-key"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
			adaptor := taskplugin.New(plugin)
			adaptor.Init(info)
			_, err = adaptor.BuildRequestBody(c, info)
			require.NoError(t, err)
			facts, err := adaptor.ExtractUsageFactsValidated(c, info)
			require.NoError(t, err)
			require.Equal(t, resolution, facts["resolution"])
			require.Equal(t, "none", facts["video_input"])
			cost, _, err := billingexpr.RunExprWithRequest(settings.BillingExpr[item.Model], billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: facts})
			require.NoError(t, err)
			require.Equal(t, common.QuotaRound(cost*common.QuotaPerUnit*.7), quota)
		})
	}
	_, _, err = quoteDramaMediaChannel(dramaMediaItem{Model: "minimax-h3", Type: "video", Duration: 5, Resolution: "4k"}, ch, 1)
	require.Error(t, err)
	_, _, err = quoteDramaMediaChannel(dramaMediaItem{Model: "minimax-h3", Type: "video", Duration: 5, Resolution: "768p", HasReferenceVideo: true}, ch, 1)
	require.ErrorContains(t, err, "reference video")
}

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
	require.ErrorContains(t, err, "duration must be 6", "Hailuo must validate its own contract, not the Grsai contract")
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
