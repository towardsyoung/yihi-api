package clonefs

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
	"github.com/QuantumNous/new-api/logger"
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

type mediaURL struct {
	URL string `json:"url,omitempty"`
}

type contentItem struct {
	Type        string    `json:"type,omitempty"`
	Text        string    `json:"text,omitempty"`
	ImageURL    *mediaURL `json:"image_url,omitempty"`
	VideoURL    *mediaURL `json:"video_url,omitempty"`
	AudioURL    *mediaURL `json:"audio_url,omitempty"`
	Role        string    `json:"role,omitempty"`
	SubjectType string    `json:"subject_type,omitempty"`
}

type requestPayload struct {
	Model         string         `json:"model"`
	Prompt        string         `json:"prompt"`
	Content       []contentItem  `json:"content,omitempty"`
	Ratio         string         `json:"ratio,omitempty"`
	Duration      *dto.IntValue  `json:"duration,omitempty"`
	Resolution    string         `json:"resolution,omitempty"`
	Watermark     bool           `json:"watermark"`
	GenerateAudio *dto.BoolValue `json:"generate_audio,omitempty"`
}

type requestMetadata struct {
	Content       []contentItem  `json:"content,omitempty"`
	Resolution    string         `json:"resolution,omitempty"`
	Ratio         string         `json:"ratio,omitempty"`
	GenerateAudio *dto.BoolValue `json:"generate_audio,omitempty"`
	Duration      *dto.IntValue  `json:"duration,omitempty"`
}

type submitResponse struct {
	ID      string `json:"id"`
	VideoID string `json:"video_id"`
	TaskID  string `json:"task_id"`
	Data    struct {
		ID      string `json:"id"`
		VideoID string `json:"video_id"`
		TaskID  string `json:"task_id"`
	} `json:"data"`
}

type taskResponse struct {
	ID       string `json:"id"`
	VideoID  string `json:"video_id"`
	TaskID   string `json:"task_id"`
	Model    string `json:"model"`
	Status   string `json:"status"`
	State    string `json:"state"`
	VideoURL string `json:"video_url"`
	Content  struct {
		VideoURL string `json:"video_url"`
	} `json:"content"`
	Output struct {
		VideoURL string `json:"video_url"`
	} `json:"output"`
	Data struct {
		Status   string `json:"status"`
		State    string `json:"state"`
		VideoURL string `json:"video_url"`
		Error    string `json:"error"`
		Message  string `json:"message"`
	} `json:"data"`
	Message string        `json:"message"`
	Detail  string        `json:"detail"`
	Error   responseError `json:"error"`
}

type responseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *responseError) UnmarshalJSON(data []byte) error {
	var message string
	if err := common.Unmarshal(data, &message); err == nil {
		e.Message = message
		return nil
	}
	type alias responseError
	var parsed alias
	if err := common.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*e = responseError(parsed)
	return nil
}

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = strings.TrimRight(info.ChannelBaseUrl, "/")
	a.apiKey = info.ApiKey
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
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
	if !isFixedPriceModel(modelName) {
		return nil
	}
	if _, ok := ratio_setting.GetModelPrice(modelName, false); !ok {
		return service.TaskErrorWrapperLocal(fmt.Errorf("clonefs fixed-price model %s must configure ModelPrice", modelName), "model_price_error", http.StatusBadRequest)
	}
	body, err := a.convertToRequestPayload(&req, modelName)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if _, err := billingFactors(body); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_clonefs_billing_params", http.StatusBadRequest)
	}
	return nil
}

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	modelName := info.OriginModelName
	if modelName == "" {
		modelName = req.Model
	}
	if !isFixedPriceModel(modelName) {
		return nil
	}
	body, err := a.convertToRequestPayload(&req, modelName)
	if err != nil {
		return nil
	}
	factors, err := billingFactors(body)
	if err != nil {
		return nil
	}
	return factors
}

func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s/v1/videos", strings.TrimRight(a.baseURL, "/")), nil
}

func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}

	body, err := a.convertToRequestPayload(&req, info.OriginModelName)
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}
	if info.IsModelMapped {
		body.Model = info.UpstreamModelName
	} else {
		info.UpstreamModelName = body.Model
	}
	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	logger.LogDebug(c, fmt.Sprintf("clonefs video request body: %s", data))
	return bytes.NewReader(data), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	_ = resp.Body.Close()

	var cloneResp submitResponse
	if err := common.Unmarshal(responseBody, &cloneResp); err != nil {
		return "", nil, service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
	}
	taskID = cloneResp.taskID()
	if taskID == "" {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName
	c.JSON(http.StatusOK, ov)
	return taskID, responseBody, nil
}

func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}
	uri := fmt.Sprintf("%s/v1/videos/%s", strings.TrimRight(baseUrl, "/"), taskID)
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

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	resTask := taskResponse{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{Code: 0}
	status := resTask.normalizedStatus()
	switch status {
	case "pending", "queued":
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "10%"
	case "processing", "running", "in_progress":
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "50%"
	case "completed", "succeeded", "success":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = resTask.videoURL()
	case "failed", "failure", "error":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = resTask.errorMessage()
	case "cancelled", "canceled", "expired", "timeout":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = resTask.errorMessage()
		if taskResult.Reason == "" {
			taskResult.Reason = status
		}
	default:
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}
	return &taskResult, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var cloneResp taskResponse
	if err := common.Unmarshal(originTask.Data, &cloneResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal clonefs task data failed")
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.TaskID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	openAIVideo.SetMetadata("url", cloneResp.videoURL())
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt
	openAIVideo.Model = originTask.Properties.OriginModelName
	if cloneResp.isFailure() {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: cloneResp.errorMessage(),
			Code:    cloneResp.Error.Code,
		}
	}
	return common.Marshal(openAIVideo)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq, modelName string) (*requestPayload, error) {
	if modelName == "" {
		modelName = req.Model
	}
	metadata := requestMetadata{}
	if err := taskcommon.UnmarshalMetadata(req.Metadata, &metadata); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}

	body := &requestPayload{
		Model:         modelName,
		Prompt:        strings.TrimSpace(req.Prompt),
		Content:       append([]contentItem{}, metadata.Content...),
		Ratio:         metadata.Ratio,
		Duration:      metadata.Duration,
		Resolution:    metadata.Resolution,
		Watermark:     false,
		GenerateAudio: metadata.GenerateAudio,
	}

	for _, imgURL := range req.Images {
		body.Content = append(body.Content, contentItem{
			Type:     "image_url",
			ImageURL: &mediaURL{URL: imgURL},
		})
	}
	if sec, _ := strconv.Atoi(req.Seconds); sec > 0 {
		body.Duration = lo.ToPtr(dto.IntValue(sec))
	} else if req.Duration > 0 {
		body.Duration = lo.ToPtr(dto.IntValue(req.Duration))
	}
	if resolution, ok := aliasResolution(modelName); ok {
		body.Resolution = resolution
	}
	if body.Resolution == "" {
		body.Resolution = "720p"
	}

	body.Content = normalizeContent(body.Content)
	if body.Prompt != "" {
		body.Content = append([]contentItem{{Type: "text", Text: body.Prompt}}, body.Content...)
	}
	return body, nil
}

func normalizeContent(content []contentItem) []contentItem {
	out := make([]contentItem, 0, len(content))
	for i, item := range content {
		switch item.Type {
		case "text":
			continue
		case "image_url":
			if item.Role == "" {
				if i == 0 {
					item.Role = "first_frame"
				} else {
					item.Role = "reference_image"
				}
			}
			if item.SubjectType == "" {
				item.SubjectType = "person"
			}
		case "audio_url":
			if item.Role == "" {
				item.Role = "reference_audio"
			}
		}
		out = append(out, item)
	}
	return out
}

func billingFactors(body *requestPayload) (map[string]float64, error) {
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
	_, resolutionRatio, ok := normalizeResolution(body.Resolution)
	if !ok {
		return nil, fmt.Errorf("seedance resolution must be 480p or 720p")
	}
	return map[string]float64{
		"seconds":     float64(seconds),
		"resolution":  resolutionRatio,
		"video_input": videoInputRatio(hasVideoInput(body.Content)),
	}, nil
}

func hasVideoInput(content []contentItem) bool {
	for _, item := range content {
		if item.Type == "video_url" || item.VideoURL != nil {
			return true
		}
	}
	return false
}

func (r submitResponse) taskID() string {
	return firstNonEmpty(r.VideoID, r.ID, r.TaskID, r.Data.VideoID, r.Data.ID, r.Data.TaskID)
}

func (r taskResponse) normalizedStatus() string {
	status := firstNonEmpty(r.Status, r.State, r.Data.Status, r.Data.State)
	return strings.ToLower(strings.TrimSpace(status))
}

func (r taskResponse) videoURL() string {
	return firstNonEmpty(r.VideoURL, r.Content.VideoURL, r.Data.VideoURL, r.Output.VideoURL)
}

func (r taskResponse) errorMessage() string {
	return firstNonEmpty(r.Error.Message, r.Message, r.Detail, r.Data.Error, r.Data.Message)
}

func (r taskResponse) isFailure() bool {
	status := r.normalizedStatus()
	return status == "failed" || status == "failure" || status == "error"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
