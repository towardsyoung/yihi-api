package relay

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGrsaiH3UsagePricing(t *testing.T) {
	const name = "minimax-h3"
	const expression = `u("resolution") == "480p"
		? tier("480p", u("seconds") * (0.1 / 6.7) * 1.8 * (u("video_input") == "video" ? 1.65 : 1))
		: u("resolution") == "768p"
		? tier("768p", u("seconds") * (0.14 / 6.7) * 1.8 * (u("video_input") == "video" ? 1.65 : 1))
		: tier("1080p", u("seconds") * (0.3 / 6.7) * 1.8 * (u("video_input") == "video" ? 1.65 : 1))`
	saveBillingConfig(t)
	require.NoError(t, model.ValidateModelPricing(name, model.PricingValues{
		"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": expression,
	}))
	require.ErrorContains(t, model.ValidateModelPricing(name, model.PricingValues{
		"billing_setting.billing_expr": `u("missing")`,
	}), "not declared")
	require.Error(t, model.ValidateModelPricing("ordinary-model", model.PricingValues{
		"billing_setting.billing_expr": expression,
	}))
	expressions, err := common.Marshal(map[string]string{name: expression})
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"minimax-h3":"tiered_expr"}`,
		"billing_setting.billing_expr": string(expressions),
	}))
	service.InitHttpClient()
	for _, tc := range []struct {
		resolution string
		duration   int
		unitPrice  float64
		valid      bool
	}{
		{"480p", 15, 0.1, true}, {"768p", 15, 0.14, true}, {"1080p", 10, 0.3, true},
		{"1080p", 11, 0, false}, {"768p", 16, 0, false}, {"480p", -1, 0, false},
	} {
		t.Run(fmt.Sprintf("%s-%d", tc.resolution, tc.duration), func(t *testing.T) {
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				called = true
				assert.Equal(t, "/v1/api/generate", req.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"grsai-test-job","status":"running"}`))
			}))
			defer server.Close()
			c, info := newTaskSubmitContext(t, name, "")
			body := fmt.Sprintf(`{"model":"minimax-h3","prompt":"landscape","duration":%d,"metadata":{"resolution":%q,"aspect_ratio":"16:9"}}`, tc.duration, tc.resolution)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeGrsai)
			c.Set("group", "default")
			info.UserGroup, info.UsingGroup, info.OriginModelName = "default", "default", name
			info.Billing = &imageReservation{limit: 1 << 30}
			result, taskErr := RelayTaskSubmit(c, info)
			if !tc.valid {
				require.NotNil(t, taskErr)
				assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
				assert.False(t, called)
				return
			}
			require.Nil(t, taskErr, "%+v", taskErr)
			require.NotNil(t, result)
			assert.True(t, called)
			wantCost := float64(tc.duration) * tc.unitPrice / 6.7 * 1.8
			assert.Equal(t, common.QuotaRound(wantCost*common.QuotaPerUnit), result.Quota)
			require.NotNil(t, info.TieredBillingSnapshot)
			assert.Equal(t, map[string]any{"seconds": float64(tc.duration), "resolution": tc.resolution, "video_input": "none"}, info.TieredBillingSnapshot.UsageFacts)
			assert.Empty(t, info.PriceData.OtherRatios())
			settled, _, err := service.EvaluateTaskCompletionUsage(info.TieredBillingSnapshot, nil)
			require.NoError(t, err)
			assert.Equal(t, result.Quota, settled.ActualQuotaAfterGroup)
		})
	}
}

func TestOpenAIH3UsagePricing(t *testing.T) {
	saveBillingConfig(t)
	prices, err := common.Marshal(ratio_setting.GetModelPriceCopy())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(prices))) })
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"minimax-h3":0.01}`))
	source, err := builtinplugins.Source("sora")
	require.NoError(t, err)
	previousRegistry := pluginruntime.DefaultRegistry
	pluginruntime.DefaultRegistry = pluginruntime.NewRegistry()
	t.Cleanup(func() { pluginruntime.DefaultRegistry = previousRegistry })
	_, err = pluginruntime.DefaultRegistry.Register(source, pluginruntime.Options{})
	require.NoError(t, err)
	const name = "minimax-h3"
	const expression = `tier("h3", u("seconds") * (u("resolution") == "480p" ? 0.1 : u("resolution") == "768p" ? 0.14 : 0.3) * (u("video_input") == "none" ? 1 : 1.65))`
	expressions, err := common.Marshal(map[string]string{name: expression})
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"minimax-h3":"tiered_expr"}`,
		"billing_setting.billing_expr": string(expressions),
	}))
	settings := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	service.InitHttpClient()
	for _, tc := range []struct {
		resolution string
		seconds    int
		unit       float64
		valid      bool
		field      string
		multipart  bool
		legacy     bool
	}{
		{"480p", 1, .1, true, "duration", false, false}, {"768p", 1, .14, true, "duration", false, false}, {"768p", 1, .14, true, "seconds", false, false},
		{"1080p", 10, .3, true, "duration", false, false}, {"1080p", 11, 0, false, "duration", false, false}, {"768p", 16, 0, false, "duration", false, false},
		{"768p", 5, .14, true, "seconds", true, false}, {"480p", 5, .1, true, "duration", true, false},
		{"1080p", 11, 0, false, "seconds", true, false}, {"768p", 16, 0, false, "duration", true, false},
		{"768p", 5, .01, true, "seconds", true, true}, {"768p", 16, 0, false, "seconds", true, true},
	} {
		t.Run(fmt.Sprintf("%s/%d/%s/multipart=%t/legacy=%t", tc.resolution, tc.seconds, tc.field, tc.multipart, tc.legacy), func(t *testing.T) {
			settings.BillingMode[name] = billing_setting.BillingModeTieredExpr
			if tc.legacy {
				settings.BillingMode[name] = billing_setting.BillingModeRatio
			}
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				assert.Equal(t, "/v1/videos", r.URL.Path)
				if tc.multipart {
					if !assert.NoError(t, r.ParseMultipartForm(1<<20)) {
						return
					}
					defer r.MultipartForm.RemoveAll()
					assert.Equal(t, strconv.Itoa(tc.seconds), r.FormValue(tc.field))
					assert.JSONEq(t, fmt.Sprintf(`{"resolution":%q,"aspect_ratio":"16:9"}`, tc.resolution), r.FormValue("metadata"))
					file, _, err := r.FormFile("input_reference")
					if !assert.NoError(t, err) {
						return
					}
					defer file.Close()
					content, err := io.ReadAll(file)
					assert.NoError(t, err)
					assert.Equal(t, "reference-image", string(content))
				} else {
					var body map[string]any
					if !assert.NoError(t, common.DecodeJson(r.Body, &body)) {
						return
					}
					assert.Equal(t, float64(tc.seconds), body[tc.field])
					assert.Equal(t, tc.resolution, body["metadata"].(map[string]any)["resolution"])
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"h3-compatible-task","status":"running"}`))
			}))
			defer server.Close()
			c, info := newTaskSubmitContext(t, name, "")
			body := map[string]any{"model": name, "prompt": "landscape", "duration": tc.seconds, "metadata": map[string]any{"resolution": tc.resolution, "aspect_ratio": "16:9"}}
			delete(body, "duration")
			body[tc.field] = tc.seconds
			c.Set("task_request", body)
			if tc.multipart {
				var input bytes.Buffer
				writer := multipart.NewWriter(&input)
				require.NoError(t, writer.WriteField("model", name))
				require.NoError(t, writer.WriteField("prompt", "landscape"))
				require.NoError(t, writer.WriteField(tc.field, strconv.Itoa(tc.seconds)))
				require.NoError(t, writer.WriteField("metadata", fmt.Sprintf(`{"resolution":%q,"aspect_ratio":"16:9"}`, tc.resolution)))
				file, err := writer.CreateFormFile("input_reference", "reference.png")
				require.NoError(t, err)
				_, err = io.WriteString(file, "reference-image")
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(input.Bytes()))
				c.Request.Header.Set("Content-Type", writer.FormDataContentType())
			}
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			info.UserGroup, info.UsingGroup, info.OriginModelName = "default", "default", name
			info.Billing = &imageReservation{limit: 1 << 30}
			result, taskErr := RelayTaskSubmit(c, info)
			if !tc.valid {
				require.NotNil(t, taskErr)
				assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
				assert.False(t, called)
				return
			}
			require.Nil(t, taskErr, "%+v", taskErr)
			require.NotNil(t, result)
			assert.True(t, called)
			assert.Equal(t, common.QuotaRound(float64(tc.seconds)*tc.unit*common.QuotaPerUnit), result.Quota)
			if tc.legacy {
				assert.Nil(t, info.TieredBillingSnapshot)
				assert.Equal(t, map[string]float64{"seconds": float64(tc.seconds)}, info.PriceData.OtherRatios())
				return
			}
			require.NotNil(t, info.TieredBillingSnapshot)
			assert.Equal(t, map[string]any{"seconds": float64(tc.seconds), "resolution": tc.resolution, "video_input": "none"}, info.TieredBillingSnapshot.UsageFacts)
			settled, _, err := service.EvaluateTaskCompletionUsage(info.TieredBillingSnapshot, nil)
			require.NoError(t, err)
			assert.Equal(t, result.Quota, settled.ActualQuotaAfterGroup)
		})
	}
}

func setupRelayChannelDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	previousCache := common.MemoryCacheEnabled
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.Channel{}))
	model.DB = database
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.MemoryCacheEnabled = previousCache
		require.NoError(t, sqlDB.Close())
	})
	return database
}

func TestApplyChannelPinPreservesOriginTasksAndRetryMode(t *testing.T) {
	database := setupRelayChannelDB(t)
	channel := &model.Channel{Name: "origin-channel", Key: "sk-test", Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeDoubaoVideo}
	require.NoError(t, database.Create(channel).Error)
	originTask := &model.Task{
		TaskID: "task-lock", ChannelId: channel.Id, Action: "text_to_video", Status: model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{UpstreamTaskID: "upstream-task-lock"},
		Data:        []byte(`{"id":"upstream-task-lock"}`),
	}

	for _, tc := range []struct {
		name     string
		tokenPin bool
		apply    func(*gin.Context, *relaycommon.RelayInfo) *dto.TaskError
	}{
		{name: "origin affinity", apply: ApplyOriginTaskAffinity},
		{name: "same channel retry", apply: ApplyChannelPin},
		{name: "token pin suppresses channel lock", tokenPin: true, apply: ApplyChannelPin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			common.SetContextKey(c, constant.ContextKeyOriginTasks, []*model.Task{originTask})
			constraints := service.GetChannelConstraints(c)
			constraints.AddPin(dto.ChannelPin{ChannelId: channel.Id, Source: dto.PinSourceOriginTask, Rank: dto.PinRankOriginTask, RetryMode: dto.PinRetrySameChannel})
			if tc.tokenPin {
				constraints.AddPin(dto.ChannelPin{ChannelId: channel.Id, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt})
			}
			info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
			require.Nil(t, tc.apply(c, info))
			if tc.tokenPin {
				assert.Nil(t, info.LockedChannel)
			} else {
				locked, ok := info.LockedChannel.(*model.Channel)
				require.True(t, ok)
				require.NotNil(t, locked)
				assert.Equal(t, channel.Id, locked.Id)
			}
			require.Len(t, info.OriginTasks, 1)
			assert.Equal(t, "task-lock", info.OriginTasks[0].TaskID)
			assert.Equal(t, "upstream-task-lock", info.OriginTasks[0].UpstreamTaskID)
			assert.Equal(t, "text_to_video", info.OriginTasks[0].Action)
			assert.Equal(t, string(model.TaskStatusSuccess), info.OriginTasks[0].Status)
			assert.Equal(t, []byte(originTask.Data), info.OriginTasks[0].Data)
		})
	}
}

func TestTaskModel2DtoNormalizesLegacyAction(t *testing.T) {
	task := &model.Task{Action: "firstTailGenerate"}

	dtoTask := TaskModel2Dto(task)

	assert.Equal(t, constant.TaskActionFirstTailToVideo, dtoTask.Action)
	assert.Equal(t, "firstTailGenerate", task.Action)
}

const mappingOrderSubmitPlugin = `
export const meta = {apiVersion:1,key:"maporder",name:"Map Order",version:"1.0.0",author:{name:"Test"},models:["declared-model"],fetchMode:"per_task"};
export function buildSubmitRequest(ctx) {
  return {url: ctx.baseUrl+"/submit", method:"POST", body:{upstreamModel: ctx.upstreamModel, model: ctx.model}, action:"text_to_video"};
}
export function parseSubmitResponse(){return {taskId:"1"};}
export function buildQueryRequest(){return {url:"https://provider.example"};}
export function parseTaskResult(){return {status:"SUCCESS"};}
`

const mappingOrderRewritePlugin = `
export const meta = {apiVersion:1,key:"maporder-rw",name:"Map Order RW",version:"1.0.0",author:{name:"Test"},models:["declared-model"],fetchMode:"per_task"};
export function buildSubmitRequest(ctx) {
  return {url: ctx.baseUrl+"/submit", method:"POST", body:{upstreamModel: ctx.upstreamModel}, rewriteModel:"rewritten"};
}
export function parseSubmitResponse(){return {taskId:"1"};}
export function buildQueryRequest(){return {url:"https://provider.example"};}
export function parseTaskResult(){return {status:"SUCCESS"};}
`

func pinMappingOrderPlugin(t *testing.T, c *gin.Context, source string) {
	t.Helper()
	plugin, err := pluginruntime.NewRegistry().Register(source, pluginruntime.Options{})
	require.NoError(t, err)
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
}

func newTaskSubmitContext(t *testing.T, originalModel, mapping string) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, originalModel)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://provider.example")
	if mapping != "" {
		c.Set("model_mapping", mapping)
	}
	c.Set("task_request", map[string]any{"prompt": "p"})
	return c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
}

func TestRelayTaskSubmitMapsBeforeValidateWhenOriginSet(t *testing.T) {
	const mapping = `{"alias-model":"mid-model","mid-model":"declared-model"}`

	c, info := newTaskSubmitContext(t, "alias-model", mapping)
	pinMappingOrderPlugin(t, c, mappingOrderSubmitPlugin)
	info.OriginModelName = "alias-model"

	_, taskErr := RelayTaskSubmit(c, info)
	require.NotNil(t, taskErr)
	assert.Equal(t, "model_price_error", taskErr.Code)
	assert.Equal(t, "alias-model", info.OriginModelName)
	assert.Equal(t, "declared-model", info.UpstreamModelName)
	assert.True(t, info.IsModelMapped)
}

func TestRelayTaskSubmitDeclaredNameWithoutMappingIsUnchanged(t *testing.T) {
	c, info := newTaskSubmitContext(t, "declared-model", "")
	pinMappingOrderPlugin(t, c, mappingOrderSubmitPlugin)
	info.OriginModelName = "declared-model"

	_, taskErr := RelayTaskSubmit(c, info)
	require.NotNil(t, taskErr)
	assert.Equal(t, "model_price_error", taskErr.Code)
	assert.Equal(t, "declared-model", info.OriginModelName)
	assert.Equal(t, "declared-model", info.UpstreamModelName)
	assert.False(t, info.IsModelMapped)
}

func TestRelayTaskSubmitDoesNotApplyMappingTwice(t *testing.T) {
	c, info := newTaskSubmitContext(t, "alias-model", `{"alias-model":"declared-model"}`)
	pinMappingOrderPlugin(t, c, mappingOrderRewritePlugin)
	info.OriginModelName = "alias-model"

	_, taskErr := RelayTaskSubmit(c, info)
	require.NotNil(t, taskErr)
	assert.Equal(t, "model_price_error", taskErr.Code)
	assert.Equal(t, "rewritten", info.UpstreamModelName, "late mapping would overwrite rewriteModel with the chain tail")
	assert.Equal(t, "alias-model", info.OriginModelName)
}

func TestRelayTaskSubmitEmptyOriginKeepsLateMapping(t *testing.T) {
	plugin, err := pluginruntime.NewRegistry().Register(mappingOrderSubmitPlugin, pluginruntime.Options{})
	require.NoError(t, err)
	synthesized := service.CoverTaskActionToModelName(constant.TaskPlatform(plugin.Meta.Key), "text_to_video")
	c, info := newTaskSubmitContext(t, "pre-validate-upstream",
		`{"pre-validate-upstream":"should-not-apply-early","`+synthesized+`":"legacy-tail"}`)
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
	info.OriginModelName = ""

	_, taskErr := RelayTaskSubmit(c, info)
	require.NotNil(t, taskErr)
	assert.Equal(t, "model_price_error", taskErr.Code)
	assert.Equal(t, synthesized, info.OriginModelName)
	assert.Equal(t, "legacy-tail", info.UpstreamModelName)
	assert.True(t, info.IsModelMapped)
}

const billingFallbackPlugin = `
export const meta = {apiVersion:1,key:"bill-fallback",name:"Bill Fallback",version:"1.0.0",author:{name:"Test"},models:["declared-model"],fetchMode:"per_task"};
export function buildSubmitRequest(ctx) {
  return {url: ctx.baseUrl+"/submit", method:"POST", body:{upstreamModel: ctx.upstreamModel, model: ctx.model}, action:"text_to_video"};
}
export function parseSubmitResponse(){return {taskId:"1"};}
export function buildQueryRequest(){return {url:"https://provider.example"};}
export function parseTaskResult(){return {status:"SUCCESS"};}
`

func saveBillingConfig(t *testing.T) {
	t.Helper()
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
}

func TestRelayTaskSubmitAliasBillingIdentityAndExprFallback(t *testing.T) {
	const mapping = `{"alias-model":"declared-model"}`
	const aliasExpr = `tier("alias", 2)`
	const tailExpr = `tier("tail", 3)`
	const legacyExpr = `tier("legacy", u("old_units") * 2)`

	tests := []struct {
		name       string
		modes      map[string]string
		exprs      map[string]string
		wantTiered bool
		wantExpr   string
		source     string
	}{
		{
			name:       "alias own tiered wins",
			modes:      map[string]string{"alias-model": "tiered_expr", "declared-model": "tiered_expr"},
			exprs:      map[string]string{"alias-model": aliasExpr, "declared-model": tailExpr},
			wantTiered: true,
			wantExpr:   aliasExpr,
		},
		{
			name:       "fallback uses tail expr",
			modes:      map[string]string{"declared-model": "tiered_expr"},
			exprs:      map[string]string{"declared-model": tailExpr},
			wantTiered: true,
			wantExpr:   tailExpr,
		},
		{
			name:       "stored expression keeps running after schema narrows",
			modes:      map[string]string{"declared-model": "tiered_expr"},
			exprs:      map[string]string{"declared-model": legacyExpr},
			wantTiered: true,
			wantExpr:   legacyExpr,
			source: strings.Replace(billingFallbackPlugin, `fetchMode:"per_task"`, `fetchMode:"per_task", usageSchema:{old_units:{type:"number",unit:"count"}}, usageProfiles:[{models:["declared-model"],schema:{seconds:{type:"number",unit:"second"}}}]`, 1) + `
export function extractUsage(){return {old_units:2};}
`,
		},
		{
			name:       "neither tiered uses ordinary pricing",
			wantTiered: false,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			saveBillingConfig(t)
			if len(testCase.modes) > 0 {
				modeJSON, marshalErr := common.Marshal(testCase.modes)
				require.NoError(t, marshalErr)
				exprJSON, marshalErr := common.Marshal(testCase.exprs)
				require.NoError(t, marshalErr)
				require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
					"billing_setting.billing_mode": string(modeJSON),
					"billing_setting.billing_expr": string(exprJSON),
				}))
				if testCase.wantExpr == aliasExpr {
					require.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode("alias-model"))
				} else {
					require.Equal(t, billing_setting.BillingModeRatio, billing_setting.GetBillingMode("alias-model"))
					require.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode("declared-model"))
				}
			}

			c, info := newTaskSubmitContext(t, "alias-model", mapping)
			c.Set("group", "default")
			info.UserGroup = "default"
			info.UsingGroup = "default"
			source := testCase.source
			if source == "" {
				source = billingFallbackPlugin
			}
			pinMappingOrderPlugin(t, c, source)
			info.OriginModelName = "alias-model"

			_, taskErr := RelayTaskSubmit(c, info)
			require.NotNil(t, taskErr)
			assert.Equal(t, "alias-model", info.OriginModelName)
			assert.Equal(t, "declared-model", info.UpstreamModelName)
			assert.True(t, info.IsModelMapped)

			task := model.InitTask(constant.TaskPlatform("bill-fallback"), info)
			assert.Equal(t, "alias-model", task.Properties.OriginModelName)
			assert.Equal(t, "declared-model", task.Properties.UpstreamModelName)

			if testCase.wantTiered {
				require.NotNil(t, info.TieredBillingSnapshot, "submission error: %+v", taskErr)
				assert.Equal(t, "alias-model", info.TieredBillingSnapshot.ModelName)
				assert.Equal(t, testCase.wantExpr, info.TieredBillingSnapshot.ExprString)
				assert.Equal(t, billingexpr.ExprHashString(testCase.wantExpr), info.TieredBillingSnapshot.ExprHash)
				assert.NotEqual(t, "model_price_error", taskErr.Code)
				if testCase.wantExpr == legacyExpr {
					assert.Equal(t, 4*common.QuotaPerUnit, info.TieredBillingSnapshot.EstimatedQuotaBeforeGroup)
				}
			} else {
				assert.Nil(t, info.TieredBillingSnapshot)
				assert.Equal(t, "model_price_error", taskErr.Code)
			}
		})
	}
}

func TestSharedTaskBillingExpressionSelectionAndFrozenSettlement(t *testing.T) {
	const baseExpr = `tier("base", u("seconds") * 2)`
	const alphaExpr = `tier("alpha", u("seconds") * 3)`
	const betaExpr = `tier("beta", u("credits") * 5)`
	const aliasExpr = `tier("alias", u("credits") * 7)`
	for _, tc := range []struct {
		name, plugin, model, mapping, modelExpr, mode, wantExpr string
		variants                                                map[string]string
		wantPriceError, profiled                                bool
	}{
		{name: "executing plugin override", plugin: "billing-beta", model: "declared-model", modelExpr: baseExpr, mode: "tiered_expr", variants: map[string]string{"billing-alpha::declared-model": alphaExpr, "billing-beta::declared-model": betaExpr}, wantExpr: betaExpr},
		{name: "override ignores model mode", plugin: "billing-beta", model: "declared-model", modelExpr: baseExpr, mode: "ratio", variants: map[string]string{"billing-beta::declared-model": betaExpr}, wantExpr: betaExpr},
		{name: "model expression fallback", plugin: "billing-alpha", model: "declared-model", modelExpr: baseExpr, mode: "tiered_expr", wantExpr: baseExpr},
		{name: "alias override precedes mapped override", plugin: "billing-beta", model: "alias-model", mapping: `{"alias-model":"declared-model"}`, modelExpr: baseExpr, mode: "tiered_expr", variants: map[string]string{"billing-beta::declared-model": betaExpr, "billing-beta::alias-model": aliasExpr}, wantExpr: aliasExpr},
		{name: "mapped override precedes model fallback", plugin: "billing-beta", model: "alias-model", mapping: `{"alias-model":"declared-model"}`, modelExpr: baseExpr, mode: "tiered_expr", variants: map[string]string{"billing-beta::declared-model": betaExpr}, wantExpr: betaExpr},
		{name: "unconfigured plugin cannot use another schema", plugin: "billing-beta", model: "declared-model", modelExpr: baseExpr, mode: "tiered_expr", wantPriceError: true},
		{name: "missing usage in skipped branch remains incompatible", plugin: "billing-beta", model: "declared-model", modelExpr: `true ? tier("free", 0) : tier("missing", u("seconds"))`, mode: "tiered_expr", wantPriceError: true},
		{name: "fixed pricing is still rejected", plugin: "billing-beta", model: "declared-model", variants: map[string]string{"billing-beta::declared-model": `tier("fixed", fixed(1))`}, wantPriceError: true},
		{name: "endpoint mapping keeps the declared profile", plugin: "billing-beta", model: "declared-model", mapping: `{"declared-model":"ep-endpoint"}`, modelExpr: baseExpr, mode: "tiered_expr", variants: map[string]string{"billing-beta::declared-model": betaExpr}, wantExpr: betaExpr, profiled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saveBillingConfig(t)
			registry := pluginruntime.NewRegistry()
			for _, spec := range []struct{ key, field, unit string }{{"billing-alpha", "seconds", "second"}, {"billing-beta", "credits", "credit"}} {
				source := strings.ReplaceAll(billingFallbackPlugin, "bill-fallback", spec.key)
				schema := `usageSchema:{` + spec.field + `:{type:"number",unit:"` + spec.unit + `"}}`
				if tc.profiled && spec.key == "billing-beta" {
					// The endpoint ID is undeclared, so only the declared model's profile carries this schema.
					schema = `usageSchema:{seconds:{type:"number",unit:"second"}},usageProfiles:[{models:["declared-model"],schema:{` + spec.field + `:{type:"number",unit:"` + spec.unit + `"}}}]`
				}
				source = strings.Replace(source, `fetchMode:"per_task"`, `fetchMode:"per_task",`+schema, 1)
				source += `export function extractUsage(){return {` + spec.field + `:2};}`
				_, err := registry.Register(source, pluginruntime.Options{})
				require.NoError(t, err)
			}
			variants := tc.variants
			if variants == nil {
				variants = map[string]string{}
			}
			rawVariants, err := common.Marshal(variants)
			require.NoError(t, err)
			modes, err := common.Marshal(map[string]string{"declared-model": tc.mode})
			require.NoError(t, err)
			expressions, err := common.Marshal(map[string]string{"declared-model": tc.modelExpr})
			require.NoError(t, err)
			require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
				billing_setting.PluginBillingExprOption: string(rawVariants), "billing_setting.billing_mode": string(modes), "billing_setting.billing_expr": string(expressions),
			}))
			c, info := newTaskSubmitContext(t, tc.model, tc.mapping)
			c.Set("group", "default")
			c.Set("task_plugin_key", tc.plugin)
			info.UserGroup = "default"
			info.UsingGroup = "default"
			info.OriginModelName = tc.model
			plugin, ok := registry.Generation().Get(tc.plugin)
			require.True(t, ok)
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Generation: registry.Generation(), Plugin: plugin})
			_, taskErr := RelayTaskSubmit(c, info)
			require.NotNil(t, taskErr) // This fixture stops at reservation, before upstream submission.
			if tc.wantPriceError {
				assert.Equal(t, "model_price_error", taskErr.Code)
				assert.Nil(t, info.TieredBillingSnapshot)
				return
			}
			require.NotNil(t, info.TieredBillingSnapshot, "submission error: %+v", taskErr)
			assert.Equal(t, tc.wantExpr, info.TieredBillingSnapshot.ExprString)
			assert.NotEqual(t, "model_price_error", taskErr.Code)
			require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{billing_setting.PluginBillingExprOption: `{}`, "billing_setting.billing_expr": `{}`}))
			field := "seconds"
			if tc.plugin == "billing-beta" {
				field = "credits"
			}
			result, usage, err := service.EvaluateTaskCompletionUsage(info.TieredBillingSnapshot, map[string]any{field: float64(4)})
			require.NoError(t, err)
			assert.Equal(t, float64(4), usage[field])
			assert.Equal(t, float64(2), info.TieredBillingSnapshot.UsageFacts[field])
			assert.Equal(t, 2*info.TieredBillingSnapshot.EstimatedQuotaAfterGroup, result.ActualQuotaAfterGroup)
			assert.Equal(t, tc.wantExpr, info.TieredBillingSnapshot.ExprString)
		})
	}
}

// Issue #7478: task APIs answer 201 Created or 202 Accepted on submission.
// Every 2xx must reach parseSubmitResponse; only other statuses are upstream
// failures that keep the upstream status and body.
func TestRelayTaskSubmitAcceptsAnySuccessfulUpstreamStatus(t *testing.T) {
	service.InitHttpClient()
	const source = `
export const meta = {apiVersion:1,key:"status-echo",name:"Status Echo",version:"1.0.0",author:{name:"Test"},models:["declared-model"],fetchMode:"per_task"};
export function buildSubmitRequest(ctx) { return {url: ctx.baseUrl+"/submit", method:"POST", body:{model: ctx.model}, action:"text_to_video"}; }
export function parseSubmitResponse(ctx, response) { return {taskId: response.body.id, taskData: {status: response.statusCode}}; }
export function buildQueryRequest(ctx) { return {url: ctx.baseUrl+"/query"}; }
export function parseTaskResult() { return {status:"SUCCESS"}; }
`
	for _, tc := range []struct {
		status   int
		wantCode string
	}{
		{status: http.StatusOK},
		{status: http.StatusCreated},
		{status: http.StatusAccepted},
		{status: http.StatusBadRequest, wantCode: "fail_to_fetch_task"},
		{status: http.StatusBadGateway, wantCode: "fail_to_fetch_task"},
	} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			saveBillingConfig(t)
			require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
				"billing_setting.billing_mode": `{"declared-model":"tiered_expr"}`,
				"billing_setting.billing_expr": `{"declared-model":"tier(\"flat\", 3)"}`,
			}))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"id":"job-42","message":"upstream body"}`))
			}))
			defer server.Close()

			c, info := newTaskSubmitContext(t, "declared-model", "")
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			c.Set("group", "default")
			info.UserGroup, info.UsingGroup = "default", "default"
			info.OriginModelName = "declared-model"
			// An existing reservation skips pre-consume so the fixture reaches the upstream call.
			info.Billing = &imageReservation{limit: 1 << 30}
			pinMappingOrderPlugin(t, c, source)

			result, taskErr := RelayTaskSubmit(c, info)
			if tc.wantCode != "" {
				require.NotNil(t, taskErr)
				assert.Equal(t, tc.wantCode, taskErr.Code)
				assert.Equal(t, tc.status, taskErr.StatusCode)
				assert.Contains(t, taskErr.Message, "upstream body")
				assert.Nil(t, result)
				return
			}
			require.Nil(t, taskErr, "submission error: %+v", taskErr)
			require.NotNil(t, result)
			assert.Equal(t, "job-42", result.UpstreamTaskID)
			assert.JSONEq(t, `{"status":`+strconv.Itoa(tc.status)+`}`, string(result.TaskData))
		})
	}
}

func TestSeedanceOwnExpressionSubmission(t *testing.T) {
	service.InitHttpClient()
	source, err := builtinplugins.Source("doubao")
	require.NoError(t, err)
	registry := pluginruntime.NewRegistry()
	plugin, err := registry.Register(source, pluginruntime.Options{})
	require.NoError(t, err)
	const original = "doubao-seedance-2-5-260628"
	const cheap = "doubao-seedance-2-5-cheap"
	for _, tc := range []struct {
		name, client string
		ownPrice     bool
		wantCost     float64
	}{
		{"original price", original, true, 1.5},
		{"cheap own price", cheap, true, 1.2},
		{"cheap cannot inherit original price", cheap, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saveBillingConfig(t)
			modes := map[string]string{original: "tiered_expr"}
			expressions := map[string]string{original: `tier("720p", u("seconds") * 0.15)`}
			if tc.client == cheap && tc.ownPrice {
				modes[cheap] = "tiered_expr"
				expressions[cheap] = `tier("720p", u("seconds") * 0.12)`
			}
			modeJSON, err := common.Marshal(modes)
			require.NoError(t, err)
			exprJSON, err := common.Marshal(expressions)
			require.NoError(t, err)
			require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"billing_setting.billing_mode": string(modeJSON), "billing_setting.billing_expr": string(exprJSON), "billing_setting.plugin_billing_expr": "{}"}))
			var received map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				require.NoError(t, common.DecodeJson(req.Body, &received))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"seedance-test-job"}`))
			}))
			defer server.Close()
			mapping := ""
			settings := kitdto.ChannelOtherSettings{}
			if tc.client == cheap {
				settings.SeedanceUpscale = map[string]kitdto.SeedanceUpscaleModelConfig{cheap: {Enabled: true, MapModel: original, Groups: []string{"default"}, Rules: map[string]kitdto.SeedanceUpscaleRuleConfig{"720p": {SeedanceResolution: "480p", UpscaleResolution: "720p"}}, Upscale: kitdto.SeedanceUpscaleProviderConfig{APIKey: "test-only"}}}
				mapping = `{"` + cheap + `":"` + original + `"}`
			}
			c, info := newTaskSubmitContext(t, tc.client, mapping)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, settings)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeDoubaoVideo)
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin, Generation: registry.Generation()})
			c.Set("task_request", map[string]any{"model": tc.client, "prompt": "landscape", "seconds": 10, "metadata": map[string]any{"resolution": "720p"}})
			c.Set("group", "default")
			info.UserGroup, info.UsingGroup, info.OriginModelName = "default", "default", tc.client
			info.Billing = &imageReservation{limit: 1 << 30}
			result, taskErr := RelayTaskSubmit(c, info)
			if !tc.ownPrice {
				require.NotNil(t, taskErr)
				assert.Equal(t, "model_price_error", taskErr.Code)
				assert.Nil(t, received, "missing product price must fail before calling upstream")
				return
			}
			require.Nil(t, taskErr, "%+v", taskErr)
			require.NotNil(t, result)
			assert.Equal(t, common.QuotaRound(tc.wantCost*common.QuotaPerUnit), result.Quota)
			assert.Equal(t, original, received["model"])
			assert.Equal(t, float64(10), received["duration"])
			expectedResolution := "720p"
			if tc.client == cheap {
				expectedResolution = "480p"
				require.NotNil(t, result.PrivateDataPatch.Upscale)
				assert.Equal(t, "720p", result.PrivateDataPatch.Upscale.TargetResolution)
			}
			assert.Equal(t, expectedResolution, received["resolution"])
			require.NotNil(t, info.TieredBillingSnapshot)
			assert.Equal(t, tc.client, info.TieredBillingSnapshot.ModelName)
			assert.Equal(t, "720p", info.TieredBillingSnapshot.UsageFacts["resolution"])
			assert.Empty(t, info.PriceData.OtherRatios())
			// Later price edits must not change the already submitted job's charge.
			require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"billing_setting.billing_expr": "{}"}))
			settled, _, err := service.EvaluateTaskCompletionUsage(info.TieredBillingSnapshot, nil)
			require.NoError(t, err)
			assert.Equal(t, result.Quota, settled.ActualQuotaAfterGroup)
		})
	}
}
