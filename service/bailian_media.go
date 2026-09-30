package service

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func IsBailianLipSyncTask(task *model.Task) bool {
	return task != nil && (task.Properties.UpstreamModelName == "pixverse/pixverse-lipsync" || task.Properties.UpstreamModelName == "qwen-audio-3.1-tts-next")
}

// Recover a known provider task by querying it, without creating paid work.
func RefreshUncertainBailianTask(ctx context.Context, task *model.Task) error {
	if !IsBailianLipSyncTask(task) || task.Status != model.TaskStatusUnknown || task.PrivateData.UpstreamTaskID == "" {
		return nil
	}
	if GetTaskAdaptorFunc == nil {
		return fmt.Errorf("task query unavailable")
	}
	ch, err := model.CacheGetChannel(task.ChannelId)
	if err != nil {
		return err
	}
	adaptor := GetTaskAdaptorFunc(constant.TaskPlatform(task.Platform))
	if adaptor == nil {
		return fmt.Errorf("task query unavailable")
	}
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: ch.Type, ChannelId: ch.Id, ChannelBaseUrl: ch.GetBaseURL(), ApiKey: ch.Key}})
	return updateVideoSingleTask(ctx, adaptor, ch, task.TaskID, map[string]*model.Task{task.TaskID: task})
}

// MeasureBailianMediaDuration verifies billable duration independently of client
// fields. Downloads carry no API credentials and use the protected fetch client.
func MeasureBailianMediaDuration(ctx context.Context, rawURL, format string, maxBytes int64, maxSeconds float64) (float64, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return 0, fmt.Errorf("media URL must use HTTPS without credentials")
	}
	if err := ValidateSSRFProtectedFetchURL(rawURL); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	client := GetSSRFProtectedHTTPClient()
	if client == nil {
		return 0, fmt.Errorf("media verification client unavailable")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("cannot read media for duration verification")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength > maxBytes {
		return 0, fmt.Errorf("media response is invalid or oversized")
	}
	file, err := os.CreateTemp("", "bailian-meter-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	size, err := io.Copy(file, io.LimitReader(resp.Body, maxBytes+1))
	if err != nil || size == 0 || size > maxBytes {
		return 0, fmt.Errorf("media is empty, unreadable or oversized")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	if format == "" {
		format = path.Ext(parsed.Path)
	}
	duration, err := common.GetAudioDuration(ctx, file, format)
	if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || duration > maxSeconds {
		return 0, fmt.Errorf("media duration is invalid or exceeds %.0f seconds", maxSeconds)
	}
	return math.Ceil(duration), nil
}
