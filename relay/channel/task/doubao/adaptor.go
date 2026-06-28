package doubao

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/samber/lo"
)

// ============================
// Request / Response structures
// ============================

type ContentItem struct {
	Type     string    `json:"type,omitempty"`
	Text     string    `json:"text,omitempty"`
	ImageURL *MediaURL `json:"image_url,omitempty"`
	VideoURL *MediaURL `json:"video_url,omitempty"`
	AudioURL *MediaURL `json:"audio_url,omitempty"`
	Role     string    `json:"role,omitempty"`
}

type MediaURL struct {
	URL string `json:"url,omitempty"`
}

type requestPayload struct {
	Model                 string         `json:"model"`
	Content               []ContentItem  `json:"content,omitempty"`
	CallbackURL           string         `json:"callback_url,omitempty"`
	ReturnLastFrame       *dto.BoolValue `json:"return_last_frame,omitempty"`
	ServiceTier           string         `json:"service_tier,omitempty"`
	ExecutionExpiresAfter *dto.IntValue  `json:"execution_expires_after,omitempty"`
	GenerateAudio         *dto.BoolValue `json:"generate_audio,omitempty"`
	Draft                 *dto.BoolValue `json:"draft,omitempty"`
	Tools                 []struct {
		Type string `json:"type,omitempty"`
	} `json:"tools,omitempty"`
	Resolution  string         `json:"resolution,omitempty"`
	Ratio       string         `json:"ratio,omitempty"`
	Duration    *dto.IntValue  `json:"duration,omitempty"`
	Frames      *dto.IntValue  `json:"frames,omitempty"`
	Seed        *dto.IntValue  `json:"seed,omitempty"`
	CameraFixed *dto.BoolValue `json:"camera_fixed,omitempty"`
	Watermark   *dto.BoolValue `json:"watermark,omitempty"`
}

type responsePayload struct {
	ID string `json:"id"` // task_id
}

type responseTask struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Status  string `json:"status"`
	Content struct {
		VideoURL string `json:"video_url"`
	} `json:"content"`
	Seed            int    `json:"seed"`
	Resolution      string `json:"resolution"`
	Duration        int    `json:"duration"`
	Ratio           string `json:"ratio"`
	FramesPerSecond int    `json:"framespersecond"`
	ServiceTier     string `json:"service_tier"`
	Tools           []struct {
		Type string `json:"type"`
	} `json:"tools"`
	Usage struct {
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		ToolUsage        struct {
			WebSearch int `json:"web_search"`
		} `json:"tool_usage"`
	} `json:"usage"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

// ValidateRequestAndSetAction parses body, validates fields and sets default action.
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	// Accept only POST /v1/video/generations as "generate" action.
	if taskErr := relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate); taskErr != nil {
		return taskErr
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	modelName := req.Model
	if modelName == "" {
		modelName = info.OriginModelName
	}
	body, err := a.convertToRequestPayload(cloneTaskSubmitReqForBilling(&req))
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	priceModelName := modelName
	if plan, ok, err := resolveConfiguredSeedanceUpscalePlan(info, modelName, body.Resolution); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_seedance_upscale_config", http.StatusBadRequest)
	} else if ok {
		priceModelName = plan.BillingModelName
		applySeedanceUpscalePlan(info, plan)
		body.Resolution = plan.SourceResolution
	} else if !IsSeedanceFixedPriceModel(modelName) {
		return nil
	}
	if _, ok := ratio_setting.GetModelPrice(priceModelName, false); !ok {
		return service.TaskErrorWrapperLocal(fmt.Errorf("seedance fixed-price model %s must configure ModelPrice", priceModelName), "model_price_error", http.StatusBadRequest)
	}
	if _, err := seedanceBillingFactors(body); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_seedance_billing_params", http.StatusBadRequest)
	}
	return nil
}

// BuildRequestURL constructs the upstream URL.
func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s/api/v3/contents/generations/tasks", a.baseURL), nil
}

// BuildRequestHeader sets required headers.
func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

// EstimateBilling returns fixed-price multipliers for Seedance 2.0.
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	modelName := info.OriginModelName
	if modelName == "" {
		modelName = req.Model
	}
	body, err := a.convertToRequestPayload(cloneTaskSubmitReqForBilling(&req))
	if err != nil {
		return nil
	}
	if plan, ok, err := resolveConfiguredSeedanceUpscalePlan(info, modelName, body.Resolution); err != nil {
		return nil
	} else if ok {
		applySeedanceUpscalePlan(info, plan)
		body.Resolution = plan.SourceResolution
	} else if !IsSeedanceFixedPriceModel(modelName) {
		return nil
	}
	factors, err := seedanceBillingFactors(body)
	if err != nil {
		return nil
	}
	return factors
}

func hasVideoInContent(content []ContentItem) bool {
	for _, item := range content {
		if item.Type == "video_url" || item.VideoURL != nil {
			return true
		}
	}
	return false
}

func cloneTaskSubmitReqForBilling(req *relaycommon.TaskSubmitReq) *relaycommon.TaskSubmitReq {
	clone := *req
	if req.Metadata != nil {
		clone.Metadata = make(map[string]interface{}, len(req.Metadata))
		for k, v := range req.Metadata {
			clone.Metadata[k] = v
		}
	}
	return &clone
}

func seedanceBillingFactors(body *requestPayload) (map[string]float64, error) {
	if body.Duration == nil {
		return nil, fmt.Errorf("seedance duration is required")
	}
	seconds := int(*body.Duration)
	if seconds <= 0 {
		return nil, fmt.Errorf("seedance duration must be positive")
	}

	if body.Resolution == "" {
		return nil, fmt.Errorf("seedance resolution is required")
	}
	_, resolutionRatio, ok := NormalizeSeedanceResolution(body.Resolution)
	if !ok {
		return nil, fmt.Errorf("seedance resolution must be 480p or 720p")
	}

	return map[string]float64{
		"seconds":     float64(seconds),
		"resolution":  resolutionRatio,
		"video_input": GetSeedanceVideoInputRatio(hasVideoInContent(body.Content)),
	}, nil
}

// BuildRequestBody converts request into Doubao specific format.
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}

	body, err := a.convertToRequestPayload(&req)
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}
	modelName := info.OriginModelName
	if modelName == "" {
		modelName = body.Model
	}
	if plan, ok, err := resolveConfiguredSeedanceUpscalePlan(info, modelName, body.Resolution); err != nil {
		return nil, err
	} else if ok {
		applySeedanceUpscalePlan(info, plan)
		body.Resolution = plan.SourceResolution
		body.Model = plan.UpstreamModelName
		info.UpstreamModelName = plan.UpstreamModelName
		info.IsModelMapped = plan.UpstreamModelName != modelName
	} else if info.IsModelMapped {
		body.Model = info.UpstreamModelName
	}
	if info.UpstreamModelName == "" {
		info.UpstreamModelName = body.Model
	}
	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

type seedanceUpscalePlan struct {
	ExternalModelName string
	UpstreamModelName string
	BillingModelName  string
	APIKey            string
	SourceResolution  string
	TargetResolution  string
	MaxRetries        int
}

func resolveConfiguredSeedanceUpscalePlan(info *relaycommon.RelayInfo, modelName, resolution string) (seedanceUpscalePlan, bool, error) {
	if info == nil || info.ChannelMeta == nil || len(info.ChannelMeta.ChannelOtherSettings.SeedanceUpscale) == 0 {
		return seedanceUpscalePlan{}, false, nil
	}
	cfg, ok := info.ChannelMeta.ChannelOtherSettings.SeedanceUpscale[modelName]
	if !ok || !cfg.Enabled {
		return seedanceUpscalePlan{}, false, nil
	}
	if !seedanceUpscaleGroupAllowed(info, cfg.Groups) {
		return seedanceUpscalePlan{}, false, nil
	}
	normalized, _, ok := NormalizeSeedanceResolution(resolution)
	if !ok {
		normalized = strings.ToLower(strings.TrimSpace(resolution))
	}
	rule, ok := cfg.Rules[normalized]
	if !ok {
		return seedanceUpscalePlan{}, false, nil
	}
	upstreamModel := strings.TrimSpace(cfg.MapModel)
	if upstreamModel == "" {
		return seedanceUpscalePlan{}, false, fmt.Errorf("seedance upscale model %s must configure map_model", modelName)
	}
	if strings.TrimSpace(rule.BillingModel) == "" {
		return seedanceUpscalePlan{}, false, fmt.Errorf("seedance upscale model %s resolution %s missing billing_model", modelName, normalized)
	}
	if strings.TrimSpace(rule.SeedanceResolution) == "" || strings.TrimSpace(rule.UpscaleResolution) == "" {
		return seedanceUpscalePlan{}, false, fmt.Errorf("seedance upscale model %s resolution %s missing seedance_resolution or upscale_resolution", modelName, normalized)
	}
	if provider := strings.TrimSpace(cfg.Upscale.Provider); provider != "" && !strings.EqualFold(provider, "doubao") {
		return seedanceUpscalePlan{}, false, fmt.Errorf("unsupported seedance upscale provider %s", provider)
	}
	apiKey := strings.TrimSpace(cfg.Upscale.APIKey)
	if apiKey == "" {
		return seedanceUpscalePlan{}, false, fmt.Errorf("seedance upscale model %s must configure upscale.api_key", modelName)
	}
	maxRetries := cfg.Upscale.MaxRetries
	if maxRetries <= 0 {
		maxRetries = seedanceUpscaleDefaultMaxRetries
	}
	return seedanceUpscalePlan{
		ExternalModelName: modelName,
		UpstreamModelName: upstreamModel,
		BillingModelName:  strings.TrimSpace(rule.BillingModel),
		APIKey:            apiKey,
		SourceResolution:  strings.ToLower(strings.TrimSpace(rule.SeedanceResolution)),
		TargetResolution:  strings.ToLower(strings.TrimSpace(rule.UpscaleResolution)),
		MaxRetries:        maxRetries,
	}, true, nil
}

func seedanceUpscaleGroupAllowed(info *relaycommon.RelayInfo, groups []string) bool {
	if len(groups) == 0 {
		return true
	}
	groupSet := make(map[string]bool, len(groups))
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group != "" {
			groupSet[group] = true
		}
	}
	for _, group := range []string{info.TokenGroup, info.UsingGroup, info.UserGroup} {
		if groupSet[strings.TrimSpace(group)] {
			return true
		}
	}
	return false
}

func applySeedanceUpscalePlan(info *relaycommon.RelayInfo, plan seedanceUpscalePlan) {
	info.BillingModelName = plan.BillingModelName
	info.VideoUpscale = &relaycommon.VideoUpscaleRelayInfo{
		Enabled:          true,
		Stage:            seedanceUpscaleStageSeedance,
		BillingModelName: plan.BillingModelName,
		APIKey:           plan.APIKey,
		SourceResolution: plan.SourceResolution,
		TargetResolution: plan.TargetResolution,
		MaxRetries:       plan.MaxRetries,
	}
}

func (a *TaskAdaptor) BuildTaskPrivateDataPatch(info *relaycommon.RelayInfo, upstreamTaskID string, _ []byte) model.TaskPrivateData {
	if info == nil || info.VideoUpscale == nil || !info.VideoUpscale.Enabled {
		return model.TaskPrivateData{}
	}
	upscale := info.VideoUpscale
	return model.TaskPrivateData{
		Upscale: &model.VideoUpscaleContext{
			Enabled:          true,
			Stage:            upscale.Stage,
			BillingModelName: upscale.BillingModelName,
			APIKey:           upscale.APIKey,
			SeedanceTaskID:   upstreamTaskID,
			SourceResolution: upscale.SourceResolution,
			TargetResolution: upscale.TargetResolution,
			MaxRetries:       upscale.MaxRetries,
		},
	}
}

// DoRequest delegates to common helper.
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse handles upstream response, returns taskID etc.
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	// Parse Doubao response
	var dResp responsePayload
	if err := common.Unmarshal(responseBody, &dResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	if dResp.ID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName

	c.JSON(http.StatusOK, ov)
	return dResp.ID, responseBody, nil
}

// FetchTask fetch task status
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}
	if isSeedanceUpscaleTaskID(taskID) {
		return a.fetchUpscaleTask(key, taskID, proxy)
	}

	uri := fmt.Sprintf("%s/api/v3/contents/generations/tasks/%s", baseUrl, taskID)

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq) (*requestPayload, error) {
	r := requestPayload{
		Model:   req.Model,
		Content: []ContentItem{},
	}

	// Add images if present
	if req.HasImage() {
		for _, imgURL := range req.Images {
			r.Content = append(r.Content, ContentItem{
				Type: "image_url",
				ImageURL: &MediaURL{
					URL: imgURL,
				},
			})
		}
	}

	metadata := req.Metadata
	if err := taskcommon.UnmarshalMetadata(metadata, &r); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}

	if sec, _ := strconv.Atoi(req.Seconds); sec > 0 {
		r.Duration = lo.ToPtr(dto.IntValue(sec))
	} else if req.Duration > 0 {
		r.Duration = lo.ToPtr(dto.IntValue(req.Duration))
	}

	r.Content = lo.Reject(r.Content, func(c ContentItem, _ int) bool { return c.Type == "text" })
	r.Content = append(r.Content, ContentItem{
		Type: "text",
		Text: req.Prompt,
	})

	return &r, nil
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	if taskResult, ok, err := parseUpscaleTaskResult(respBody); ok || err != nil {
		return taskResult, err
	}

	resTask := responseTask{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}

	// Map Doubao status to internal status
	switch resTask.Status {
	case "pending", "queued":
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "10%"
	case "processing", "running":
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "50%"
	case "succeeded":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = resTask.Content.VideoURL
		// 解析 usage 信息用于按倍率计费
		taskResult.CompletionTokens = resTask.Usage.CompletionTokens
		taskResult.TotalTokens = resTask.Usage.TotalTokens
	case "failed":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = resTask.Error.Message
	default:
		// Unknown status, treat as processing
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}

	return &taskResult, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var dResp responseTask
	if err := common.Unmarshal(originTask.Data, &dResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal doubao task data failed")
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.TaskID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	openAIVideo.SetMetadata("url", dResp.Content.VideoURL)
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt
	openAIVideo.Model = originTask.Properties.OriginModelName

	if dResp.Status == "failed" {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: dResp.Error.Message,
			Code:    dResp.Error.Code,
		}
	}

	return common.Marshal(openAIVideo)
}
