package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay/channel/task/grsai"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

type dramaMediaItem struct {
	Model               string         `json:"model"`
	Type                string         `json:"type"`
	Resolution          string         `json:"resolution"`
	Duration            float64        `json:"duration"`
	Count               int            `json:"count"`
	AspectRatio         string         `json:"aspectRatio"`
	Quality             string         `json:"quality"`
	Size                string         `json:"size"`
	HasReferenceVideo   bool           `json:"hasReferenceVideo"`
	ReferenceImageCount *int           `json:"referenceImageCount"`
	Audio               *bool          `json:"audio,omitempty"`
	Options             map[string]any `json:"options,omitempty"`
}

// Quotes are read-only. Missing provider meters or unknown output tokens are
// reported explicitly, rather than substituting a reservation for a price.
func DramaMediaQuote(c *gin.Context) {
	token, ok := getDramaTokenByParam(c)
	if !ok {
		return
	}
	var req struct {
		Items []dramaMediaItem `json:"items"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Items) == 0 || len(req.Items) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "items must contain 1 to 128 quotes"})
		return
	}
	for _, item := range req.Items {
		if err := validateDramaMediaItem(item); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
			return
		}
	}
	user, err := model.GetUserById(token.UserId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	group := token.Group
	if group == "" {
		group = user.Group
	}
	if group == "auto" {
		common.ApiError(c, fmt.Errorf("media quotes require a fixed token group"))
		return
	}
	info := &relaycommon.RelayInfo{UserId: token.UserId, UserGroup: user.Group, UsingGroup: group}
	ratio := helper.HandleGroupRatio(c, info).GroupRatio
	abilities, err := model.GetAllEnableAbilityWithChannels()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	now := time.Now().UnixMilli()
	results := make([]gin.H, 0, len(req.Items))
	versions := []string{}
	for _, item := range req.Items {
		result := gin.H{"status": "unavailable", "reason": "no supported billing meter or available channel", "effectiveParameters": item, "estimatedAt": now, "unit": "request"}
		var quotas []int
		var fingerprints []string
		channelErrors := make([]gin.H, 0)
		complete := true
		for _, ability := range abilities {
			if ability.Group != group || ability.Model != item.Model {
				continue
			}
			if token.ModelLimitsEnabled && !token.GetModelLimitsMap()[item.Model] {
				continue
			}
			ch, e := model.GetChannelById(ability.ChannelId, false)
			if e != nil {
				channelErrors = append(channelErrors, gin.H{"channelId": ability.ChannelId, "reason": "channel is unavailable"})
				fingerprints = append(fingerprints, fmt.Sprint(ability.ChannelId, ":unavailable"))
				complete = false
				continue
			}
			q, version, e := quoteDramaMediaChannel(item, ch, ratio)
			if e != nil {
				result["reason"] = e.Error()
				channelErrors = append(channelErrors, gin.H{"channelId": ch.Id, "reason": e.Error()})
				fingerprints = append(fingerprints, fmt.Sprint(ch.Id, ":", e.Error()))
				complete = false
				continue
			}
			quotas = append(quotas, q)
			fingerprints = append(fingerprints, version)
		}
		slices.Sort(fingerprints)
		version := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprint(group, ratio, common.QuotaPerUnit, fingerprints))))
		result["pricingVersion"] = version
		result["complete"] = complete && len(quotas) > 0
		if len(channelErrors) > 0 {
			result["channelErrors"] = channelErrors
		}
		if len(quotas) > 0 && !complete {
			// Known prices are diagnostic only; clients must not display these
			// as a complete quote for a request that can use an unpriced channel.
			result["partialQuote"] = gin.H{"minQuota": slices.Min(quotas), "maxQuota": slices.Max(quotas), "channelCount": len(quotas)}
		}
		versions = append(versions, version)
		if len(quotas) > 0 && complete {
			lo, hi := slices.Min(quotas), slices.Max(quotas)
			delete(result, "reason")
			result["status"] = "exact_parameters"
			result["rawQuota"] = lo
			if lo != hi {
				result["status"] = "range"
				delete(result, "rawQuota")
				result["minQuota"] = lo
				result["maxQuota"] = hi
			}
		}
		results = append(results, result)
	}
	common.ApiSuccess(c, gin.H{"items": results, "estimatedAt": now, "pricingVersion": fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(versions, ","))))})
}

func validateDramaMediaItem(item dramaMediaItem) error {
	if item.Model == "" || len(item.Model) > 255 || (item.Type != "image" && item.Type != "video") {
		return fmt.Errorf("invalid media model or type")
	}
	if item.Count < 0 || item.Count > dto.MaxImageN || (item.ReferenceImageCount != nil && (*item.ReferenceImageCount < 0 || *item.ReferenceImageCount > dto.MaxImageN)) {
		return fmt.Errorf("invalid image count")
	}
	if math.IsNaN(item.Duration) || math.IsInf(item.Duration, 0) || item.Duration < 0 || item.Duration > relaycommon.MaxTaskDurationSeconds {
		return fmt.Errorf("invalid duration")
	}
	if item.Type == "video" && (item.Duration <= 0 || math.Trunc(item.Duration) != item.Duration) {
		return fmt.Errorf("video duration must be positive whole seconds")
	}
	return nil
}

func quoteDramaMediaChannel(item dramaMediaItem, ch *model.Channel, ratio float64) (int, string, error) {
	if err := validateDramaMediaItem(item); err != nil {
		return 0, "", err
	}
	if len(ch.GetParamOverride()) > 0 {
		return 0, "", fmt.Errorf("channel parameter overrides require an execution meter")
	}
	mapped := item.Model
	var mapping map[string]string
	if err := common.UnmarshalJsonStr(ch.GetModelMapping(), &mapping); err == nil && mapping[item.Model] != "" {
		mapped = mapping[item.Model]
	}
	// Grsai is a native adaptor; a registry plugin for the same model must not
	// replace its resolution or duration contract.
	if item.Type == "video" && ch.Type == constant.ChannelTypeGrsai {
		return quoteDramaH3Video(item, ch.Id, mapped, "", ratio)
	}
	plugin := ch.GetSetting().TaskPluginKey
	loaded, loadedOK := jsplugin.DefaultRegistry.Get(plugin)
	if plugin == "" {
		loaded, loadedOK = jsplugin.DefaultRegistry.GetByChannelType(ch.Type)
		if loadedOK {
			plugin = loaded.Meta.Key
		}
	}
	if ch.Type == constant.ChannelTypeNewAPI {
		// Match the host protocol distributor's first bound candidate order.
		path := "/v1/videos"
		if item.Type == "image" {
			path = "/v1/images/generations"
		}
		loaded, loadedOK = nil, false
		for _, candidate := range jsplugin.DefaultRegistry.Generation().LookupEndpointCandidates("POST", path, item.Model) {
			if ch.GetSetting().BindsTaskPlugin(candidate.Plugin.Meta.Key) {
				loaded, loadedOK, plugin = candidate.Plugin, true, candidate.Plugin.Meta.Key
				break
			}
		}
		if !loadedOK && len(ch.GetSetting().TaskPluginBindings()) > 0 {
			return 0, "", fmt.Errorf("no bound provider supports this media model and protocol")
		}
	}
	if item.Type == "video" && ch.Type == constant.ChannelTypeOpenAI && mapped == "minimax-h3" && loadedOK && plugin == "sora" {
		platform := ch.GetOtherSettings().TaskPlatform
		if platform == "" || platform == "sora" || platform == "1" || platform == "55" {
			return quoteDramaH3Video(item, ch.Id, mapped, plugin, ratio)
		}
	}
	if item.Type == "image" {
		quota, version, handled, err := quoteDramaImageRequestPrice(item, ch.Id, mapped, plugin, ratio)
		if handled {
			return quota, version, err
		}
	}
	// Existing Doubao meters below remain a compatibility path for older
	// plugins. Other plugins use their decoder/usage hooks, without a model or
	// provider allowlist. A plugin can map canonical parameters explicitly.
	if loadedOK && (plugin != "doubao" || loaded.Engine.HasExport("buildQuoteRequest")) {
		return quoteDramaPluginMedia(item, ch, mapped, loaded, ratio)
	}
	var expression string
	var configured bool
	input := billingexpr.RequestInput{}
	if item.Type == "video" {
		if (plugin != "doubao" && ch.Type != constant.ChannelTypeDoubaoVideo) || !strings.HasPrefix(item.Model, "doubao-seedance-2-") {
			return 0, "", fmt.Errorf("video provider meter is unavailable")
		}
		if !loadedOK && item.Resolution != "480p" && item.Resolution != "720p" && item.Resolution != "1080p" {
			return 0, "", fmt.Errorf("resolution requires a provider-specific meter")
		}
		expression, configured = billing_setting.ResolveTaskBillingExpr(plugin, item.Model, "")
		videoInput := "none"
		if item.HasReferenceVideo {
			videoInput = "video"
		}
		input.Usage = map[string]any{"seconds": item.Duration, "resolution": item.Resolution, "video_input": videoInput}
		if loadedOK {
			content := []any{}
			if item.HasReferenceVideo {
				content = append(content, map[string]any{"type": "video_url", "role": "reference_video", "video_url": map[string]any{"url": "https://quote.invalid/reference"}})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			body := map[string]any{"model": mapped, "seconds": item.Duration, "metadata": map[string]any{"resolution": item.Resolution, "content": content}}
			if item.Audio != nil {
				body["generate_audio"] = *item.Audio
			}
			facts, err := loaded.Engine.Call(ctx, "extractUsage", map[string]any{"model": item.Model, "upstreamModel": mapped, "usagePurpose": "facts", "requestBody": body})
			usage, ok := facts.(map[string]any)
			if err != nil || !ok {
				return 0, "", fmt.Errorf("video provider rejected quote parameters")
			}
			input.Usage = usage
		}
		for key := range billingexpr.UsedUsageKeys(expression) {
			if _, exists := input.Usage[key]; !exists {
				return 0, "", fmt.Errorf("billing requires unknown usage: %s", key)
			}
		}
	} else {
		if plugin == "doubao" && loadedOK && strings.Contains(mapped, "seedream") {
			if item.Count > 1 {
				return 0, "", fmt.Errorf("quote image requests separately")
			}
			body := map[string]any{"model": mapped, "prompt": "quote"}
			if item.Size != "" {
				body["size"] = item.Size
			}
			if item.ReferenceImageCount != nil && *item.ReferenceImageCount > 0 {
				refs := make([]string, *item.ReferenceImageCount)
				for i := range refs {
					refs[i] = "https://quote.invalid/reference"
				}
				body["image"] = refs
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			value, err := loaded.Engine.Call(ctx, "extractUsage", map[string]any{"model": item.Model, "upstreamModel": mapped, "requestBody": body, "usagePurpose": "facts", "action": "text_to_image"})
			if err != nil {
				return 0, "", fmt.Errorf("provider usage extraction failed: %w", err)
			}
			facts, ok := value.(map[string]any)
			if !ok {
				return 0, "", fmt.Errorf("provider returned no usage facts")
			}
			input.Usage = facts
			expression, configured = billing_setting.ResolveTaskBillingExpr(plugin, item.Model, mapped)
			for key := range billingexpr.UsedUsageKeys(expression) {
				if key == "input_images" && item.ReferenceImageCount == nil {
					return 0, "", fmt.Errorf("reference image count is required for this price")
				}
				if _, ok := facts[key]; !ok {
					return 0, "", fmt.Errorf("unknown image usage: %s", key)
				}
			}
		} else if plugin != "" {
			return 0, "", fmt.Errorf("image task provider meter is unavailable")
		} else {
			return 0, "", fmt.Errorf("image price depends on actual output usage")
		}
	}
	if !configured {
		return 0, "", fmt.Errorf("model billing is not configured")
	}
	if regexp.MustCompile(`\bheader\s*\(`).MatchString(expression) {
		return 0, "", fmt.Errorf("billing requires request headers not available to quote")
	}
	for key := range billingexpr.UsedVars(expression) {
		if slices.Contains([]string{"p", "c", "len", "cr", "cc", "cc1h", "img", "img_cr", "img_o", "ai", "ao"}, key) {
			return 0, "", fmt.Errorf("billing depends on unavailable token usage")
		}
	}
	cost, _, err := billingexpr.RunExprWithRequest(expression, billingexpr.TokenParams{}, input)
	if err != nil || cost < 0 {
		return 0, "", fmt.Errorf("cannot evaluate model price")
	}
	quota, err := common.QuotaFromFloatStrict(math.Round(cost * common.QuotaPerUnit * ratio))
	return quota, fmt.Sprint(ch.Id, mapped, expression), err
}

func quoteDramaPluginMedia(item dramaMediaItem, ch *model.Channel, mapped string, plugin *jsplugin.LoadedPlugin, ratio float64) (int, string, error) {
	expression, configured := billing_setting.ResolveTaskBillingExpr(plugin.Meta.Key, item.Model, mapped)
	useExpression := configured || billing_setting.GetBillingMode(item.Model) == billing_setting.BillingModeTieredExpr
	if useExpression && !configured {
		return 0, "", fmt.Errorf("model billing is not configured for provider %s", plugin.Meta.Key)
	}
	if useExpression {
		if billingexpr.UsesFixedPricing(expression) {
			return 0, "", fmt.Errorf("fixed pricing is not supported for task usage expressions")
		}
		schema, _ := plugin.Meta.UsageForModels(mapped, item.Model)
		if !billing_setting.TaskExprCompatible(expression, schema) {
			return 0, "", fmt.Errorf("provider %s does not declare the usage fields required by its price", plugin.Meta.Key)
		}
		// Task submission evaluates usage facts only. Request/token-dependent
		// formulas cannot be faithfully quoted from the canonical parameters.
		if regexp.MustCompile(`\b(header|param|req)\s*\(`).MatchString(expression) {
			return 0, "", fmt.Errorf("price depends on request or output usage unavailable to quote")
		}
		for key := range billingexpr.UsedVars(expression) {
			if slices.Contains([]string{"p", "c", "len", "cr", "cc", "cc1h", "img", "img_cr", "img_o", "ai", "ao", "image_count"}, key) {
				return 0, "", fmt.Errorf("price depends on request or output usage unavailable to quote")
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var intent any
	var err error
	if plugin.Engine.HasExport("buildQuoteRequest") {
		intent, err = plugin.Engine.Call(ctx, "buildQuoteRequest", map[string]any{"model": item.Model, "upstreamModel": mapped, "parameters": item})
	} else {
		// Standard host protocol input. Providers with a different wire shape
		// implement buildQuoteRequest and still reuse their actual extractUsage.
		count := max(item.Count, 1)
		body := make(map[string]any, len(item.Options)+6)
		for key, value := range item.Options {
			body[key] = value
		}
		body["model"], body["prompt"] = item.Model, "quote"
		metadata := map[string]any{}
		if provided, ok := body["metadata"].(map[string]any); ok {
			for key, value := range provided {
				metadata[key] = value
			}
		}
		if item.Resolution != "" {
			metadata["resolution"] = item.Resolution
		}
		if item.AspectRatio != "" {
			metadata["aspect_ratio"] = item.AspectRatio
		}
		if len(metadata) > 0 {
			body["metadata"] = metadata
		}
		if item.Audio != nil {
			body["generate_audio"] = *item.Audio
		}
		if item.Size != "" {
			body["size"] = item.Size
		}
		protocol := "openai_image"
		if item.Type == "video" {
			protocol = "openai_video"
			body["seconds"] = item.Duration
			if item.Resolution != "" {
				body["resolution"] = item.Resolution
			}
			if item.AspectRatio != "" {
				body["ratio"] = item.AspectRatio
			}
		} else {
			body["n"] = count
			if item.Quality != "" {
				body["quality"] = item.Quality
			}
		}
		// Placeholder references are never fetched. Reject when a decoder
		// cannot represent the requested reference kind/count.
		if item.HasReferenceVideo || (item.ReferenceImageCount != nil && *item.ReferenceImageCount > 0) {
			return 0, "", fmt.Errorf("provider %s requires buildQuoteRequest for reference media quotes", plugin.Meta.Key)
		}
		claimed := false
		for _, claim := range plugin.Meta.Protocols {
			if claim.Name == protocol && (len(claim.Models) == 0 || slices.ContainsFunc(claim.Models, func(model string) bool {
				return strings.EqualFold(model, item.Model) || strings.EqualFold(model, mapped)
			})) {
				claimed = true
				break
			}
		}
		if !claimed {
			return 0, "", fmt.Errorf("provider %s requires buildQuoteRequest for %s quotes", plugin.Meta.Key, item.Type)
		}
		path, operation := "/v1/videos", "create"
		if item.Type == "image" {
			path, operation = "/v1/images/generations", "generate"
		}
		decodeContext := jsplugin.ProtocolRequestContext{
			RouteRequestContext: jsplugin.RouteRequestContext{Path: path, Method: http.MethodPost, Body: map[string]any{"kind": "json", "value": body}},
			Protocol:            protocol, Operation: operation, Model: item.Model, UpstreamModel: mapped,
		}
		intent, err = plugin.Engine.CallPath(ctx, "protocols", []string{protocol, "decodeRequest"}, decodeContext.JSValue())
	}
	if err != nil {
		return 0, "", fmt.Errorf("provider %s rejected quote parameters: %w", plugin.Meta.Key, err)
	}
	resolved, ok := intent.(map[string]any)
	if !ok {
		return 0, "", fmt.Errorf("provider %s cannot quote these parameters", plugin.Meta.Key)
	}
	if !plugin.Engine.HasExport("buildQuoteRequest") && (resolved["kind"] != "submit" || resolved["model"] != item.Model) {
		return 0, "", fmt.Errorf("provider %s did not decode a submission for this model", plugin.Meta.Key)
	}
	body, ok := resolved["requestBody"].(map[string]any)
	if !ok {
		return 0, "", fmt.Errorf("provider %s returned no quote request", plugin.Meta.Key)
	}
	action, _ := resolved["action"].(string)
	purpose := "billing_ratios"
	if useExpression {
		purpose = "facts"
	}
	facts, ratios, err := taskplugin.New(plugin).ExtractQuoteUsage(ctx, body, item.Model, mapped, action, purpose)
	if err != nil {
		return 0, "", err
	}
	version := fmt.Sprint(ch.Id, mapped, plugin.Meta.Key, plugin.Meta.Version, expression)
	if useExpression {
		for key := range billingexpr.UsedUsageKeys(expression) {
			if _, exists := facts[key]; !exists {
				return 0, "", fmt.Errorf("provider %s cannot estimate required usage: %s", plugin.Meta.Key, key)
			}
		}
		cost, _, runErr := billingexpr.RunExprWithRequest(expression, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: facts})
		if runErr != nil || cost < 0 {
			return 0, "", fmt.Errorf("cannot evaluate provider price")
		}
		quota, quotaErr := billingexpr.QuotaRoundStrict(cost * common.QuotaPerUnit * ratio)
		return quota, version, quotaErr
	}
	price, exists := ratio_setting.GetModelPrice(item.Model, false)
	if !exists {
		price, exists = ratio_setting.GetDefaultModelPriceMap()[item.Model]
		if !exists {
			return 0, "", fmt.Errorf("model billing is not configured")
		}
	}
	base, err := common.QuotaFromFloatStrict(price * common.QuotaPerUnit * ratio)
	if err != nil {
		return 0, "", err
	}
	priceData := types.PriceData{}
	if !common.StringsContains(constant.TaskPricePatches, item.Model) {
		for key, value := range ratios {
			priceData.AddOtherRatio(key, value)
		}
	}
	quota, err := common.QuotaFromFloatStrict(priceData.ApplyOtherRatiosToFloat(float64(base)))
	return quota, fmt.Sprint(version, price), err
}

func quoteDramaH3Video(item dramaMediaItem, channelID int, mapped, plugin string, ratio float64) (int, string, error) {
	if err := validateDramaMediaItem(item); err != nil {
		return 0, "", err
	}
	if item.HasReferenceVideo {
		return 0, "", fmt.Errorf("MiniMax H3 does not support reference video")
	}
	aspectRatio := item.AspectRatio
	if aspectRatio == "" {
		aspectRatio = "16:9"
	}
	c := &gin.Context{}
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model: mapped, Prompt: "quote", Duration: int(item.Duration),
		Metadata: map[string]any{"resolution": item.Resolution, "aspect_ratio": aspectRatio},
	})
	info := &relaycommon.RelayInfo{OriginModelName: item.Model, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: mapped}}
	adaptor := &grsai.TaskAdaptor{}
	facts, err := adaptor.ExtractUsageFactsValidated(c, info)
	if err != nil {
		return 0, "", err
	}
	expression, configured := billing_setting.ResolveTaskBillingExpr(plugin, item.Model, mapped)
	if configured || billing_setting.GetBillingMode(item.Model) == billing_setting.BillingModeTieredExpr {
		if !configured {
			return 0, "", fmt.Errorf("model billing is not configured")
		}
		if billingexpr.UsesFixedPricing(expression) {
			return 0, "", fmt.Errorf("fixed pricing is not supported for task usage expressions")
		}
		for key := range billingexpr.UsedUsageKeys(expression) {
			if _, ok := facts[key]; !ok {
				return 0, "", fmt.Errorf("billing requires unknown usage: %s", key)
			}
		}
		cost, _, err := billingexpr.RunExprWithRequest(expression, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: facts})
		if err != nil || cost < 0 {
			return 0, "", fmt.Errorf("cannot evaluate model price")
		}
		quota, err := billingexpr.QuotaRoundStrict(cost * common.QuotaPerUnit * ratio)
		return quota, fmt.Sprint(channelID, mapped, expression), err
	}
	price, ok := ratio_setting.GetModelPrice(item.Model, false)
	if !ok {
		price, ok = ratio_setting.GetDefaultModelPriceMap()[item.Model]
		if !ok {
			return 0, "", fmt.Errorf("model billing is not configured")
		}
	}
	// Legacy task billing truncates the base quota before applying adaptor ratios.
	base, err := common.QuotaFromFloatStrict(price * common.QuotaPerUnit * ratio)
	if err != nil {
		return 0, "", err
	}
	if common.StringsContains(constant.TaskPricePatches, item.Model) {
		return base, fmt.Sprint(channelID, mapped, price), nil
	}
	priceData := types.PriceData{}
	if plugin == "sora" {
		priceData.AddOtherRatio("seconds", item.Duration)
	} else {
		for key, value := range adaptor.EstimateBilling(c, info) {
			priceData.AddOtherRatio(key, value)
		}
	}
	quota, err := common.QuotaFromFloatStrict(priceData.ApplyOtherRatiosToFloat(float64(base)))
	return quota, fmt.Sprint(channelID, mapped, price), err
}

// Request/image fixed prices do not require a task output-usage meter. Resolve
// them before rejecting an unimplemented plugin meter, preserving overrides.
func quoteDramaImageRequestPrice(item dramaMediaItem, channelID int, mapped, plugin string, ratio float64) (int, string, bool, error) {
	expression, configured := billing_setting.ResolveTaskBillingExpr(plugin, item.Model, mapped)
	if !configured {
		if billing_setting.GetBillingMode(item.Model) == billing_setting.BillingModeTieredExpr {
			return 0, "", false, nil
		}
		// These official models use the native images/chat fixed-price path.
		// Other task plugins can apply additional usage ratios to legacy prices.
		if plugin != "" && !strings.HasPrefix(item.Model, "gpt-image-") && !strings.HasPrefix(item.Model, "nano-banana-") {
			return 0, "", false, nil
		}
		price, ok := ratio_setting.GetModelPrice(item.Model, false)
		if !ok {
			return 0, "", false, nil
		}
		count := max(item.Count, 1)
		if strings.HasPrefix(item.Model, "nano-banana-") && count != 1 {
			return 0, "", true, fmt.Errorf("quote chat image requests separately")
		}
		quota, err := common.QuotaFromFloatStrict(price * common.QuotaPerUnit * ratio * float64(count))
		return quota, fmt.Sprint(channelID, mapped, price), true, err
	}
	if !billingexpr.UsesFixedPricing(expression) {
		return 0, "", false, nil
	}
	if regexp.MustCompile(`\bheader\s*\(`).MatchString(expression) {
		return 0, "", true, fmt.Errorf("billing requires request headers not available to quote")
	}
	for key := range billingexpr.UsedVars(expression) {
		if slices.Contains([]string{"p", "c", "len", "cr", "cc", "cc1h", "img", "img_cr", "img_o", "ai", "ao"}, key) {
			return 0, "", true, fmt.Errorf("billing depends on unavailable token usage")
		}
	}
	if len(billingexpr.UsedUsageKeys(expression)) > 0 {
		return 0, "", true, fmt.Errorf("fixed image price requires a provider usage meter")
	}
	if item.Size == "" && regexp.MustCompile(`\b(req|param)\s*\(`).MatchString(expression) && !strings.HasPrefix(item.Model, "nano-banana-") {
		return 0, "", true, fmt.Errorf("effective image size is required for conditional pricing")
	}
	count := max(item.Count, 1)
	body, err := common.Marshal(map[string]any{"model": item.Model, "n": count, "size": item.Size, "quality": item.Quality, "extra_body": map[string]any{"google": map[string]any{"image_config": map[string]any{"image_size": item.Resolution, "aspect_ratio": item.AspectRatio}}}})
	if err != nil {
		return 0, "", true, err
	}
	cost, _, err := billingexpr.RunExprWithRequest(expression, billingexpr.TokenParams{}, billingexpr.RequestInput{Body: body, ImageCount: &count})
	if err != nil || cost < 0 {
		return 0, "", true, fmt.Errorf("cannot evaluate model price")
	}
	quota, err := billingexpr.QuotaRoundStrict(cost / 1_000_000 * common.QuotaPerUnit * ratio)
	return quota, fmt.Sprint(channelID, mapped, expression), true, err
}
