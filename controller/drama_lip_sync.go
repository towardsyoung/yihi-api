package controller

import (
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/gin-gonic/gin"
)

// Quotes share the same expression resolver and group multiplier as task billing.
// They are informational; the relay still checks and reserves authoritative quota.
func DramaLipSyncQuote(c *gin.Context) {
	token, ok := getDramaTokenByParam(c)
	if !ok {
		return
	}
	var req struct {
		Model   string  `json:"model"`
		Seconds float64 `json:"seconds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	maximum := 120.0
	if req.Model == "qwen-audio-3.1-tts-next" {
		maximum = 240
	} else if req.Model != "pixverse/pixverse-lipsync" {
		common.ApiError(c, fmt.Errorf("unsupported model"))
		return
	}
	if math.IsNaN(req.Seconds) || math.IsInf(req.Seconds, 0) || req.Seconds <= 0 || req.Seconds > maximum {
		common.ApiError(c, fmt.Errorf("invalid seconds"))
		return
	}
	expr, configured := billing_setting.ResolveTaskBillingExpr("alibaba", req.Model, req.Model)
	if !configured {
		common.ApiSuccess(c, gin.H{"configured": false, "message": "请在中转站配置该服务的按秒价格"})
		return
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
		common.ApiError(c, fmt.Errorf("请为配音和对口型 Token 指定固定计费分组"))
		return
	}
	info := &relaycommon.RelayInfo{UserId: token.UserId, UserGroup: user.Group, UsingGroup: group}
	ratio := helper.HandleGroupRatio(c, info).GroupRatio
	seconds := math.Ceil(req.Seconds)
	if req.Model == "qwen-audio-3.1-tts-next" {
		seconds = 240
	}
	unit, _, err := billingexpr.RunExprWithRequest(expr, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: map[string]any{"seconds": 1.0}})
	if err != nil || unit <= 0 || math.IsNaN(unit) || math.IsInf(unit, 0) {
		common.ApiError(c, fmt.Errorf("该服务需要配置有效的按秒价格"))
		return
	}
	cost, _, err := billingexpr.RunExprWithRequest(expr, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: map[string]any{"seconds": seconds}})
	if err != nil || cost <= 0 {
		common.ApiError(c, fmt.Errorf("invalid usage price"))
		return
	}
	quota, err := common.QuotaFromFloatStrict(math.Round(cost * common.QuotaPerUnit * ratio))
	if err != nil || quota <= 0 {
		common.ApiError(c, fmt.Errorf("invalid or oversized quote"))
		return
	}
	common.ApiSuccess(c, gin.H{"configured": true, "seconds": seconds, "quota": quota,
		"unit_quota": unit * common.QuotaPerUnit * ratio, "available_quota": token.RemainQuota,
		"sufficient": token.UnlimitedQuota || token.RemainQuota >= quota, "unlimited": token.UnlimitedQuota})
}

// Local users can share a gateway account. Match the token as well as the user
// before returning a task's supplier URLs or consumption details.
func DramaLipSyncTask(c *gin.Context) {
	token, ok := getDramaTokenByParam(c)
	if !ok {
		return
	}
	clientID := strings.TrimSpace(c.Param("client_task_id"))
	if clientID == "" || len(clientID) > 191 {
		common.ApiError(c, fmt.Errorf("invalid client task id"))
		return
	}
	task, exists, err := model.GetByClientTaskId(token.UserId, clientID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !exists || task.PrivateData.TokenId != token.Id {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "task_not_exist"})
		return
	}
	if task.Properties.UpstreamModelName != "pixverse/pixverse-lipsync" && task.Properties.UpstreamModelName != "qwen-audio-3.1-tts-next" {
		common.ApiError(c, fmt.Errorf("unsupported task"))
		return
	}
	if err := service.RefreshUncertainBailianTask(c.Request.Context(), task); err != nil {
		common.ApiError(c, fmt.Errorf("原任务查询失败，请稍后刷新"))
		return
	}
	var body map[string]any
	if len(task.Data) > 0 {
		_ = common.Unmarshal(task.Data, &body)
	}
	statuses := map[model.TaskStatus]string{model.TaskStatusSuccess: "SUCCEEDED", model.TaskStatusFailure: "FAILED", model.TaskStatusInProgress: "RUNNING", model.TaskStatusUnknown: "UNKNOWN"}
	status := statuses[task.Status]
	if status == "" {
		status = "PENDING"
	}
	common.ApiSuccess(c, gin.H{"task_id": task.TaskID, "task_status": status, "output": body["output"], "usage": body["usage"], "quota": task.Quota, "message": task.FailReason})
}
