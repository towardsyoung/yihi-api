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
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

type dramaMediaItem struct {
	Model               string  `json:"model"`
	Type                string  `json:"type"`
	Resolution          string  `json:"resolution"`
	Duration            float64 `json:"duration"`
	Count               int     `json:"count"`
	AspectRatio         string  `json:"aspectRatio"`
	Quality             string  `json:"quality"`
	Size                string  `json:"size"`
	HasReferenceVideo   bool    `json:"hasReferenceVideo"`
	ReferenceImageCount *int    `json:"referenceImageCount"`
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
				complete = false
				continue
			}
			q, version, e := quoteDramaMediaChannel(item, ch, ratio)
			if e != nil {
				result["reason"] = e.Error()
				complete = false
				continue
			}
			quotas = append(quotas, q)
			fingerprints = append(fingerprints, version)
		}
		slices.Sort(fingerprints)
		version := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprint(group, ratio, common.QuotaPerUnit, fingerprints))))
		result["pricingVersion"] = version
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
	if len(ch.GetParamOverride()) > 0 {
		return 0, "", fmt.Errorf("channel parameter overrides require an execution meter")
	}
	mapped := item.Model
	var mapping map[string]string
	if err := common.UnmarshalJsonStr(ch.GetModelMapping(), &mapping); err == nil && mapping[item.Model] != "" {
		mapped = mapping[item.Model]
	}
	plugin := ch.GetSetting().TaskPluginKey
	loaded, loadedOK := jsplugin.DefaultRegistry.Get(plugin)
	if plugin == "" {
		loaded, loadedOK = jsplugin.DefaultRegistry.GetByChannelType(ch.Type)
		if loadedOK {
			plugin = loaded.Meta.Key
		}
	}
	if item.Type == "image" {
		quota, version, handled, err := quoteDramaImageRequestPrice(item, ch.Id, mapped, plugin, ratio)
		if handled {
			return quota, version, err
		}
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
			facts, err := loaded.Engine.Call(ctx, "extractUsage", map[string]any{"model": item.Model, "upstreamModel": mapped, "usagePurpose": "facts", "requestBody": map[string]any{"model": mapped, "seconds": item.Duration, "metadata": map[string]any{"resolution": item.Resolution, "content": content}}})
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
