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

func DramaProvision(c *gin.Context) {
	var req DramaProvisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.Email = strings.TrimSpace(req.Email)
	req.Group = strings.TrimSpace(req.Group)
	req.TokenName = strings.TrimSpace(req.TokenName)
	req.TokenGroup = strings.TrimSpace(req.TokenGroup)
	if req.Group == "" {
		req.Group = "default"
	}
	if req.TokenGroup == "" {
		req.TokenGroup = req.Group
	}
	if req.TokenName == "" {
		req.TokenName = "drama-default"
	}
	if req.InitialQuota < 0 || req.TokenQuota < 0 {
		common.ApiError(c, fmt.Errorf("quota cannot be negative"))
		return
	}

	var user model.User
	createdUser := false
	createdToken := false
	grantedUserQuota := 0
	grantedTokenQuota := 0

	tx := model.DB.Begin()
	if tx.Error != nil {
		common.ApiError(c, tx.Error)
		return
	}
	defer tx.Rollback()

	err := tx.Where("username = ?", req.Username).First(&user).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiError(c, err)
			return
		}
		password, err := common.Password2Hash(common.GetRandomString(16))
		if err != nil {
			common.ApiError(c, err)
			return
		}
		user = model.User{
			Username:    req.Username,
			Password:    password,
			DisplayName: firstNonEmpty(req.DisplayName, req.Username),
			Email:       req.Email,
			Role:        common.RoleCommonUser,
			Status:      common.UserStatusEnabled,
			Group:       req.Group,
			Quota:       req.InitialQuota,
			AffCode:     common.GetRandomString(4),
		}
		if err := tx.Create(&user).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		createdUser = true
		grantedUserQuota = req.InitialQuota
	} else {
		updates := map[string]interface{}{}
		if user.DisplayName == "" && req.DisplayName != "" {
			updates["display_name"] = req.DisplayName
		}
		if user.Email == "" && req.Email != "" {
			updates["email"] = req.Email
		}
		if len(updates) > 0 {
			if err := tx.Model(&model.User{}).Where("id = ?", user.Id).Updates(updates).Error; err != nil {
				common.ApiError(c, err)
				return
			}
			if err := tx.Where("id = ?", user.Id).First(&user).Error; err != nil {
				common.ApiError(c, err)
				return
			}
		}
	}

	var token model.Token
	err = tx.Where("user_id = ? AND name = ?", user.Id, req.TokenName).First(&token).Error
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

	if createdUser && grantedUserQuota > 0 {
		model.RecordLog(user.Id, model.LogTypeSystem, fmt.Sprintf("drama provision granted %s", logger.LogQuota(grantedUserQuota)))
	}

	common.ApiSuccess(c, gin.H{
		"user": gin.H{
			"id":           user.Id,
			"username":     user.Username,
			"display_name": user.DisplayName,
			"email":        user.Email,
			"group":        user.Group,
			"quota":        user.Quota,
			"used_quota":   user.UsedQuota,
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
		"created_user":        createdUser,
		"created_token":       createdToken,
		"granted_user_quota":  grantedUserQuota,
		"granted_token_quota": grantedTokenQuota,
	})
}

func DramaUserQuota(c *gin.Context) {
	user, ok := getDramaUserByParam(c)
	if !ok {
		return
	}
	common.ApiSuccess(c, gin.H{
		"user_id":         user.Id,
		"username":        user.Username,
		"group":           user.Group,
		"quota":           user.Quota,
		"used_quota":      user.UsedQuota,
		"total_granted":   user.Quota + user.UsedQuota,
		"total_available": user.Quota,
		"total_used":      user.UsedQuota,
	})
}

func DramaUserQuotaLogs(c *gin.Context) {
	user, ok := getDramaUserByParam(c)
	if !ok {
		return
	}
	pageInfo := common.GetPageQuery(c)
	allowedTypes := []int{model.LogTypeTopup, model.LogTypeConsume, model.LogTypeManage, model.LogTypeSystem, model.LogTypeRefund}
	var logs []*model.Log
	var total int64
	tx := model.LOG_DB.Model(&model.Log{}).Where("user_id = ? AND type IN ?", user.Id, allowedTypes)
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
		})
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

func getDramaUserByParam(c *gin.Context) (*model.User, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid user id"})
		return nil, false
	}
	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	return user, true
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
