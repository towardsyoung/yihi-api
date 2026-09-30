package grsai

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const modelName = "minimax-h3"

type requestMetadata struct {
	Resolution  string   `json:"resolution"`
	AspectRatio string   `json:"aspect_ratio"`
	Audios      []string `json:"audios"`
}

type generateRequest struct {
	Model       string   `json:"model"`
	Prompt      string   `json:"prompt"`
	AspectRatio string   `json:"aspectRatio"`
	Resolution  string   `json:"resolution"`
	Duration    int      `json:"duration"`
	Images      []string `json:"images"`
	Audios      []string `json:"audios"`
	ReplyType   string   `json:"replyType"`
}

type taskResponse struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Error   string `json:"error"`
	Results []struct {
		URL string `json:"url"`
	} `json:"results"`
}

type TaskAdaptor struct {
	baseURL string
	apiKey  string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.baseURL = strings.TrimRight(info.ChannelBaseUrl, "/")
	a.apiKey = info.ApiKey
}

func buildGenerateRequest(req *relaycommon.TaskSubmitReq, upstreamModel string) (*generateRequest, error) {
	if upstreamModel != modelName {
		return nil, fmt.Errorf("unsupported Grsai video model: %s", upstreamModel)
	}
	meta := requestMetadata{}
	if err := req.UnmarshalMetadata(&meta); err != nil {
		return nil, err
	}
	resolution := strings.ToLower(meta.Resolution)
	if resolution != "480p" && resolution != "768p" && resolution != "1080p" {
		return nil, fmt.Errorf("MiniMax H3 supports 480p, 768p, or 1080p")
	}
	maxDuration := 15
	if resolution == "1080p" {
		maxDuration = 10
	}
	if req.Duration < 1 || req.Duration > maxDuration {
		return nil, fmt.Errorf("MiniMax H3 %s duration must be 1-%d seconds", resolution, maxDuration)
	}
	if meta.AspectRatio != "16:9" && meta.AspectRatio != "9:16" {
		return nil, fmt.Errorf("MiniMax H3 aspect ratio must be 16:9 or 9:16")
	}
	if len(req.Images) > 9 || len(meta.Audios) > 3 {
		return nil, fmt.Errorf("MiniMax H3 supports at most 9 images and 3 audios")
	}
	aspectRatio := "landscape"
	if meta.AspectRatio == "9:16" {
		aspectRatio = "portrait"
	}
	return &generateRequest{
		Model: modelName, Prompt: req.Prompt, AspectRatio: aspectRatio,
		Resolution: resolution, Duration: req.Duration, Images: req.Images,
		Audios: meta.Audios, ReplyType: "async",
	}, nil
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
	if taskErr := relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionImageToVideo); taskErr != nil {
		return taskErr
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if req.Duration == 0 && req.Seconds != "" {
		req.Duration, err = strconv.Atoi(req.Seconds)
		if err != nil {
			return service.TaskErrorWrapperLocal(fmt.Errorf("seconds must be an integer"), "invalid_request", http.StatusBadRequest)
		}
	}
	if len(req.Images) == 0 && strings.TrimSpace(req.InputReference) != "" {
		req.Images = []string{strings.TrimSpace(req.InputReference)}
	}
	_, err = buildGenerateRequest(&req, modelName)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	c.Set("task_request", req)
	return nil
}

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, _ *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	body, err := buildGenerateRequest(&req, modelName)
	if err != nil {
		return nil
	}
	resolutionRatio := map[string]float64{"480p": 1, "768p": 2, "1080p": 3}[body.Resolution]
	return map[string]float64{"seconds": float64(body.Duration), "resolution": resolutionRatio}
}

func (a *TaskAdaptor) AdjustBillingOnSubmit(_ *relaycommon.RelayInfo, _ []byte) map[string]float64 {
	return nil
}

func (a *TaskAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return 0
}

func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return grsaiBaseURL(a.baseURL) + "/api/generate", nil
}

func grsaiBaseURL(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL
	}
	return baseURL + "/v1"
}

func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimPrefix(a.apiKey, "Bearer "))
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}
	body, err := buildGenerateRequest(&req, info.UpstreamModelName)
	if err != nil {
		return nil, err
	}
	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, body)
}

func (a *TaskAdaptor) ParseResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*channel.TaskSubmitResponse, *taskdto.TaskError) {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	_ = resp.Body.Close()
	task := taskResponse{}
	if err := common.Unmarshal(data, &task); err != nil {
		return nil, service.TaskErrorWrapper(err, "invalid_response", http.StatusBadGateway)
	}
	if task.Status == "failed" || task.Status == "violation" || task.ID == "" {
		return nil, service.TaskErrorWrapperLocal(fmt.Errorf("Grsai H3 submit failed: %s", task.Error), "upstream_error", http.StatusBadGateway)
	}
	return &channel.TaskSubmitResponse{UpstreamTaskID: task.ID, TaskData: data}, nil
}

func (a *TaskAdaptor) FetchTask(baseURL, key string, task *model.Task, proxy string) (*http.Response, error) {
	taskID := task.GetUpstreamTaskID()
	if taskID == "" {
		return nil, fmt.Errorf("missing task ID")
	}
	endpoint := grsaiBaseURL(baseURL) + "/api/result?id=" + url.QueryEscape(taskID)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimPrefix(key, "Bearer "))
	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}

func (a *TaskAdaptor) ParseTaskResult(_ *model.Task, _ *http.Response, data []byte) (*relaycommon.TaskInfo, error) {
	task := taskResponse{}
	if err := common.Unmarshal(data, &task); err != nil {
		return nil, err
	}
	result := &relaycommon.TaskInfo{Code: 0}
	switch task.Status {
	case "running":
		result.Status, result.Progress = model.TaskStatusInProgress, "50%"
	case "succeeded":
		if len(task.Results) == 0 || task.Results[0].URL == "" {
			result.Status, result.Progress, result.Reason = model.TaskStatusFailure, "100%", "Grsai H3 returned no video URL"
		} else {
			result.Status, result.Progress, result.Url = model.TaskStatusSuccess, "100%", task.Results[0].URL
		}
	case "failed", "violation":
		result.Status, result.Progress, result.Reason = model.TaskStatusFailure, "100%", task.Error
	default:
		return nil, fmt.Errorf("unknown Grsai H3 task status: %s", task.Status)
	}
	return result, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	video := task.ToOpenAIVideo()
	if task.Status == model.TaskStatusFailure {
		video.Error = &dto.OpenAIVideoError{Message: task.FailReason, Code: "generation_failed"}
	}
	return common.Marshal(video)
}

func (a *TaskAdaptor) GetModelList() []string { return []string{modelName} }
func (a *TaskAdaptor) GetChannelName() string { return "Grsai" }
