package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type DramaProvisionRequest struct {
	Username     string `json:"username" binding:"required"`
	DisplayName  string `json:"display_name"`
	Email        string `json:"email"`
	Group        string `json:"group"`
	InitialQuota int    `json:"initial_quota"`
	TokenName    string `json:"token_name"`
	TokenGroup   string `json:"token_group"`
	TokenQuota   int    `json:"token_quota"`
}

type DramaTokenProvisionRequest struct {
	UserId     int    `json:"user_id" binding:"required"`
	TokenName  string `json:"token_name" binding:"required"`
	TokenGroup string `json:"token_group"`
	TokenQuota int    `json:"token_quota"`
}

func DramaTokenProvision(c *gin.Context) {
	var req DramaTokenProvisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	req.TokenName = strings.TrimSpace(req.TokenName)
	req.TokenGroup = strings.TrimSpace(req.TokenGroup)
	if req.UserId <= 0 {
		common.ApiError(c, fmt.Errorf("user_id is required"))
		return
	}
	if req.TokenName == "" {
		common.ApiError(c, fmt.Errorf("token_name is required"))
		return
	}
	if req.TokenGroup == "" {
		req.TokenGroup = "default"
	}
	if req.TokenQuota < 0 {
		common.ApiError(c, fmt.Errorf("quota cannot be negative"))
		return
	}

	var user model.User
	var token model.Token
	createdToken := false
	grantedTokenQuota := 0

	tx := model.DB.Begin()
	if tx.Error != nil {
		common.ApiError(c, tx.Error)
		return
	}
	defer tx.Rollback()

	if err := tx.Where("id = ?", req.UserId).First(&user).Error; err != nil {
		common.ApiError(c, err)
		return
	}

	err := tx.Where("user_id = ? AND name = ?", user.Id, req.TokenName).First(&token).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiError(c, err)
			return
		}
		key, err := common.GenerateKey()
		if err != nil {
			common.ApiError(c, err)
			return
		}
		token = model.Token{
			UserId:             user.Id,
			Name:               req.TokenName,
			Key:                key,
			Status:             common.TokenStatusEnabled,
			CreatedTime:        common.GetTimestamp(),
			AccessedTime:       common.GetTimestamp(),
			ExpiredTime:        -1,
			RemainQuota:        req.TokenQuota,
			UnlimitedQuota:     false,
			ModelLimitsEnabled: false,
			Group:              req.TokenGroup,
		}
		if err := tx.Create(&token).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		createdToken = true
		grantedTokenQuota = req.TokenQuota
	}

	if err := tx.Commit().Error; err != nil {
		common.ApiError(c, err)
		return
	}

	if createdToken && grantedTokenQuota > 0 {
		recordDramaTokenLog(user.Id, token.Id, token.Name, token.Group, model.LogTypeSystem, grantedTokenQuota, fmt.Sprintf("drama token provision granted %s", logger.LogQuota(grantedTokenQuota)))
	}

	common.ApiSuccess(c, gin.H{
		"user": gin.H{
			"id":           user.Id,
			"username":     user.Username,
			"display_name": user.DisplayName,
			"email":        user.Email,
			"group":        user.Group,
		},
		"token": gin.H{
			"id":              token.Id,
			"key":             token.GetFullKey(),
			"name":            token.Name,
			"group":           token.Group,
			"remain_quota":    token.RemainQuota,
			"used_quota":      token.UsedQuota,
			"unlimited_quota": token.UnlimitedQuota,
			"expired_time":    token.ExpiredTime,
		},
		"created_token":       createdToken,
		"granted_token_quota": grantedTokenQuota,
	})
}

func DramaTokenQuota(c *gin.Context) {
	token, ok := getDramaTokenByParam(c)
	if !ok {
		return
	}
	common.ApiSuccess(c, gin.H{
		"user_id":         token.UserId,
		"token_id":        token.Id,
		"token_name":      token.Name,
		"group":           token.Group,
		"quota":           token.RemainQuota,
		"used_quota":      token.UsedQuota,
		"total_granted":   token.RemainQuota + token.UsedQuota,
		"total_available": token.RemainQuota,
		"total_used":      token.UsedQuota,
		"unlimited_quota": token.UnlimitedQuota,
	})
}

func DramaTokenQuotaLogs(c *gin.Context) {
	token, ok := getDramaTokenByParam(c)
	if !ok {
		return
	}
	pageInfo := common.GetPageQuery(c)
	allowedTypes := []int{model.LogTypeTopup, model.LogTypeConsume, model.LogTypeManage, model.LogTypeSystem, model.LogTypeRefund}
	var logs []*model.Log
	var total int64
	tx := model.LOG_DB.Model(&model.Log{}).Where("token_id = ? AND type IN ?", token.Id, allowedTypes)
	if err := tx.Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := tx.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&logs).Error; err != nil {
		common.ApiError(c, err)
		return
	}

	items := make([]gin.H, 0, len(logs))
	for _, log := range logs {
		rawQuota := log.Quota
		direction := "none"
		if log.Type == model.LogTypeConsume {
			direction = "decrease"
		} else if log.Quota > 0 {
			direction = "increase"
		}
		displayQuota := rawQuota
		if direction == "decrease" && displayQuota > 0 {
			displayQuota = -displayQuota
		}
		items = append(items, gin.H{
			"id":           log.Id,
			"type":         log.Type,
			"type_name":    dramaLogTypeName(log.Type),
			"quota":        displayQuota,
			"raw_quota":    rawQuota,
			"direction":    direction,
			"model_name":   log.ModelName,
			"token_name":   log.TokenName,
			"created_at":   log.CreatedAt,
			"channel":      log.ChannelId,
			"channel_name": log.ChannelName,
			"content":      log.Content,
		})
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

func getDramaTokenByParam(c *gin.Context) (*model.Token, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid token id"})
		return nil, false
	}
	token, err := model.GetTokenById(id)
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	return token, true
}

func recordDramaTokenLog(userId int, tokenId int, tokenName string, group string, logType int, quota int, content string) {
	username, _ := model.GetUsernameById(userId, false)
	log := &model.Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
		Quota:     quota,
		TokenId:   tokenId,
		TokenName: tokenName,
		Group:     group,
	}
	if err := model.LOG_DB.Create(log).Error; err != nil {
		common.SysLog("failed to record drama token log: " + err.Error())
	}
}

func dramaLogTypeName(logType int) string {
	switch logType {
	case model.LogTypeTopup:
		return "topup"
	case model.LogTypeConsume:
		return "consume"
	case model.LogTypeManage:
		return "manage"
	case model.LogTypeSystem:
		return "system"
	case model.LogTypeRefund:
		return "refund"
	default:
		return "unknown"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
