package controller

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// GetMediaDiagnostics is a separate root-only read; task/list DTOs never carry
// payloads. Views are audited without copying their content into audit logs.
func GetMediaDiagnostics(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if c.GetInt("role") < common.RoleRootUser {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "Root access required"})
		return
	}
	requestID, taskID := strings.TrimSpace(c.Query("request_id")), strings.TrimSpace(c.Query("task_id"))
	if requestID == "" && taskID == "" || len(requestID) > 191 || len(taskID) > 191 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "A valid request_id or task_id is required"})
		return
	}
	if taskID != "" {
		var task model.Task
		if err := model.DB.Select("private_data").Where("task_id = ?", taskID).First(&task).Error; err == nil && task.PrivateData.Execution != nil {
			requestID = task.PrivateData.Execution.RequestID
		}
	}
	entries, err := model.GetMediaDiagnostics(requestID, taskID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load media diagnostics"})
		return
	}
	targetHash := fmt.Sprintf("%x", sha256.Sum256([]byte(requestID+"\n"+taskID)))
	model.RecordAuditLog(c, model.AuditLog{
		UserId: c.GetInt("id"), ActorRole: c.GetInt("role"), Category: model.AuditCategoryOperation,
		Action: "media_diagnostics.read", Success: true, Content: "Viewed sanitized media diagnostics",
		Other: model.AuditOther{RootInfo: model.AuditFields{"diagnostic_target_hash": targetHash, "record_count": len(entries)}},
	})
	common.ApiSuccess(c, entries)
}
