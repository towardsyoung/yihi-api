package doubao

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
)

type upscaleSubmitRequest struct {
	VideoURL   string `json:"video_url"`
	Resolution string `json:"resolution"`
	Scene      string `json:"scene"`
}

type upscaleSubmitResponse struct {
	Success   bool   `json:"success"`
	TaskID    string `json:"task_id"`
	RequestID string `json:"request_id"`
	Message   string `json:"message,omitempty"`
}

type upscaleTaskResponse struct {
	Success  bool   `json:"success"`
	TaskID   string `json:"task_id"`
	TaskType string `json:"task_type"`
	Status   string `json:"status"`
	Result   struct {
		Duration   float64 `json:"duration"`
		FPS        int     `json:"fps"`
		Resolution string  `json:"resolution"`
		VideoURL   string  `json:"video_url"`
	} `json:"result"`
	Message   string `json:"message,omitempty"`
	RequestID string `json:"request_id"`
}

func isSeedanceUpscaleTaskID(taskID string) bool {
	return strings.HasPrefix(taskID, seedanceUpscaleTaskPrefix)
}

func (a *TaskAdaptor) fetchUpscaleTask(key, taskID, proxy string) (*http.Response, error) {
	uri := fmt.Sprintf("%s/api/v1/tasks/%s", seedanceUpscaleBaseURL, taskID)
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

func (a *TaskAdaptor) submitUpscaleTask(key, proxy, videoURL, targetResolution string) (string, []byte, error) {
	payload := upscaleSubmitRequest{
		VideoURL:   videoURL,
		Resolution: targetResolution,
		Scene:      seedanceUpscaleScene,
	}
	data, err := common.Marshal(payload)
	if err != nil {
		return "", nil, err
	}

	req, err := http.NewRequest(http.MethodPost, seedanceUpscaleBaseURL+"/api/v1/tools/enhance-video", bytes.NewReader(data))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return "", nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", body, fmt.Errorf("upscale submit status %d: %s", resp.StatusCode, string(body))
	}

	var submitResp upscaleSubmitResponse
	if err := common.Unmarshal(body, &submitResp); err != nil {
		return "", body, fmt.Errorf("unmarshal upscale submit response failed: %w", err)
	}
	if !submitResp.Success || submitResp.TaskID == "" {
		if submitResp.Message != "" {
			return "", body, fmt.Errorf("upscale submit failed: %s", submitResp.Message)
		}
		return "", body, fmt.Errorf("upscale submit failed: %s", string(body))
	}
	return submitResp.TaskID, body, nil
}

func parseUpscaleTaskResult(respBody []byte) (*relaycommon.TaskInfo, bool, error) {
	var res upscaleTaskResponse
	if err := common.Unmarshal(respBody, &res); err != nil {
		return nil, false, nil
	}
	if !isSeedanceUpscaleTaskType(res.TaskType) && !isSeedanceUpscaleTaskID(res.TaskID) {
		return nil, false, nil
	}

	taskResult := relaycommon.TaskInfo{
		Code:   0,
		TaskID: res.TaskID,
	}
	switch strings.ToLower(res.Status) {
	case "completed", "succeeded", "success":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = res.Result.VideoURL
	case "failed", "fail", "canceled", "cancelled":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = res.Message
		if taskResult.Reason == "" {
			taskResult.Reason = "video upscale failed"
		}
	default:
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = seedanceUpscaleProgress
	}
	return &taskResult, true, nil
}

func isSeedanceUpscaleTaskType(taskType string) bool {
	switch strings.ToLower(strings.TrimSpace(taskType)) {
	case "enhance-video", "enhance-video-generative":
		return true
	default:
		return false
	}
}

func (a *TaskAdaptor) HandlePostProcessSuccess(ctx context.Context, task *model.Task, taskResult *relaycommon.TaskInfo, _ string, key string, proxy string) (bool, error) {
	upscale := task.PrivateData.Upscale
	if upscale == nil || !upscale.Enabled || upscale.Stage != seedanceUpscaleStageSeedance {
		return false, nil
	}
	if strings.TrimSpace(taskResult.Url) == "" {
		markUpscaleFailure(task, "seedance task succeeded without video url")
		return true, nil
	}

	upscale.SourceVideoURL = taskResult.Url
	upscale.LastError = ""
	taskID, _, err := a.submitUpscaleTask(resolveUpscaleAPIKey(upscale, key), proxy, upscale.SourceVideoURL, upscale.TargetResolution)
	if err != nil {
		handleUpscaleRetry(task, err.Error())
		return true, nil
	}

	upscale.Stage = seedanceUpscaleStageUpscale
	upscale.UpscaleTaskID = taskID
	task.PrivateData.UpstreamTaskID = taskID
	task.PrivateData.ResultURL = ""
	task.Status = model.TaskStatusInProgress
	task.Progress = seedanceUpscaleProgress
	task.FailReason = ""
	return true, nil
}

func (a *TaskAdaptor) HandlePostProcessFailure(ctx context.Context, task *model.Task, taskResult *relaycommon.TaskInfo, _ string, key string, proxy string) (bool, error) {
	upscale := task.PrivateData.Upscale
	if upscale == nil || !upscale.Enabled || upscale.Stage != seedanceUpscaleStageUpscale {
		return false, nil
	}
	reason := strings.TrimSpace(taskResult.Reason)
	if reason == "" {
		reason = "video upscale failed"
	}
	if strings.TrimSpace(upscale.SourceVideoURL) == "" {
		markUpscaleFailure(task, reason)
		return true, nil
	}

	upscale.RetryCount++
	upscale.LastError = reason
	if upscale.RetryCount > normalizeUpscaleMaxRetries(upscale.MaxRetries) {
		markUpscaleFailure(task, fmt.Sprintf("%s after retries", reason))
		return true, nil
	}

	taskID, _, err := a.submitUpscaleTask(resolveUpscaleAPIKey(upscale, key), proxy, upscale.SourceVideoURL, upscale.TargetResolution)
	if err != nil {
		upscale.LastError = err.Error()
		if upscale.RetryCount >= normalizeUpscaleMaxRetries(upscale.MaxRetries) {
			markUpscaleFailure(task, fmt.Sprintf("%s after retries", err.Error()))
			return true, nil
		}
		task.Status = model.TaskStatusInProgress
		task.Progress = seedanceUpscaleProgress
		return true, nil
	}

	upscale.UpscaleTaskID = taskID
	task.PrivateData.UpstreamTaskID = taskID
	task.Status = model.TaskStatusInProgress
	task.Progress = seedanceUpscaleProgress
	task.FailReason = ""
	return true, nil
}

func handleUpscaleRetry(task *model.Task, reason string) {
	upscale := task.PrivateData.Upscale
	if upscale == nil {
		markUpscaleFailure(task, reason)
		return
	}
	upscale.RetryCount++
	upscale.LastError = reason
	if upscale.RetryCount >= normalizeUpscaleMaxRetries(upscale.MaxRetries) {
		markUpscaleFailure(task, fmt.Sprintf("%s after retries", reason))
		return
	}
	task.Status = model.TaskStatusInProgress
	task.Progress = seedanceUpscaleProgress
	task.FailReason = ""
}

func markUpscaleFailure(task *model.Task, reason string) {
	if reason == "" {
		reason = "video upscale failed"
	}
	task.Status = model.TaskStatusFailure
	task.Progress = "100%"
	task.FailReason = reason
}

func normalizeUpscaleMaxRetries(maxRetries int) int {
	if maxRetries <= 0 {
		return seedanceUpscaleDefaultMaxRetries
	}
	return maxRetries
}

func resolveUpscaleAPIKey(upscale *model.VideoUpscaleContext, fallback string) string {
	if upscale != nil && strings.TrimSpace(upscale.APIKey) != "" {
		return strings.TrimSpace(upscale.APIKey)
	}
	return fallback
}
