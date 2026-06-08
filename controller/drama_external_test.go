package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupDramaExternalTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	previousDB := model.DB
	previousLOGDB := model.LOG_DB
	previousUsingSQLite := common.UsingSQLite
	previousUsingMySQL := common.UsingMySQL
	previousUsingPostgreSQL := common.UsingPostgreSQL
	previousRedisEnabled := common.RedisEnabled
	previousBatchUpdateEnabled := common.BatchUpdateEnabled

	gin.SetMode(gin.TestMode)
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false

	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Log{}))

	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLOGDB
		common.UsingSQLite = previousUsingSQLite
		common.UsingMySQL = previousUsingMySQL
		common.UsingPostgreSQL = previousUsingPostgreSQL
		common.RedisEnabled = previousRedisEnabled
		common.BatchUpdateEnabled = previousBatchUpdateEnabled

		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func seedDramaToken(t *testing.T, db *gorm.DB) *model.Token {
	t.Helper()

	user := &model.User{
		Username: "drama_user",
		Group:    "default",
		Quota:    0,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(user).Error)

	token := &model.Token{
		UserId:             user.Id,
		Name:               "drama_token",
		Key:                "test-key",
		Status:             common.TokenStatusEnabled,
		CreatedTime:        1,
		AccessedTime:       1,
		ExpiredTime:        -1,
		RemainQuota:        100,
		UsedQuota:          20,
		UnlimitedQuota:     false,
		ModelLimitsEnabled: false,
		Group:              "default",
	}
	require.NoError(t, db.Create(token).Error)
	return token
}

func TestAddDramaTokenQuotaIdempotent(t *testing.T) {
	db := setupDramaExternalTestDB(t)
	token := seedDramaToken(t, db)
	uniqueId := "event-001"
	upstreamRequestId := dramaQuotaAddUpstreamRequestId(uniqueId)

	result, err := addDramaTokenQuota(token.Id, 50, "task completed", uniqueId, upstreamRequestId)
	require.NoError(t, err)
	require.Equal(t, false, result["idempotent"])
	require.Equal(t, 150, result["quota"])
	require.Equal(t, 20, result["used_quota"])

	var updated model.Token
	require.NoError(t, db.First(&updated, token.Id).Error)
	require.Equal(t, 150, updated.RemainQuota)
	require.Equal(t, 20, updated.UsedQuota)

	var log model.Log
	require.NoError(t, db.Where("token_id = ? AND upstream_request_id = ?", token.Id, upstreamRequestId).First(&log).Error)
	require.Contains(t, log.Content, "task completed")
	require.Equal(t, 50, log.Quota)
	other, err := common.StrToMap(log.Other)
	require.NoError(t, err)
	require.Equal(t, uniqueId, other["unique_id"])

	result, err = addDramaTokenQuota(token.Id, 50, "task completed", uniqueId, upstreamRequestId)
	require.NoError(t, err)
	require.Equal(t, true, result["idempotent"])
	require.Equal(t, 150, result["quota"])

	var count int64
	require.NoError(t, db.Model(&model.Log{}).Where("token_id = ? AND upstream_request_id = ?", token.Id, upstreamRequestId).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestAddDramaTokenQuotaRejectsUniqueIdWithDifferentDelta(t *testing.T) {
	db := setupDramaExternalTestDB(t)
	token := seedDramaToken(t, db)
	uniqueId := "event-002"
	upstreamRequestId := dramaQuotaAddUpstreamRequestId(uniqueId)

	_, err := addDramaTokenQuota(token.Id, 50, "task completed", uniqueId, upstreamRequestId)
	require.NoError(t, err)

	_, err = addDramaTokenQuota(token.Id, 60, "task completed", uniqueId, upstreamRequestId)
	require.Error(t, err)

	var updated model.Token
	require.NoError(t, db.First(&updated, token.Id).Error)
	require.Equal(t, 150, updated.RemainQuota)
	require.Equal(t, 20, updated.UsedQuota)
}

func TestDramaTokenQuotaAddDefaultsEmptyReason(t *testing.T) {
	db := setupDramaExternalTestDB(t)
	token := seedDramaToken(t, db)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	body := map[string]interface{}{
		"token_id":  token.Id,
		"delta":     50,
		"unique_id": "event-003",
	}
	payload, err := common.Marshal(body)
	require.NoError(t, err)
	ctx.Request = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/external/drama/tokens/%d/quota/add", token.Id), strings.NewReader(string(payload)))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", token.Id)}}

	DramaTokenQuotaAdd(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response tokenAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)

	var log model.Log
	require.NoError(t, db.Where("token_id = ? AND upstream_request_id = ?", token.Id, dramaQuotaAddUpstreamRequestId("event-003")).First(&log).Error)
	require.Contains(t, log.Content, defaultDramaTokenQuotaAddReason)
}
