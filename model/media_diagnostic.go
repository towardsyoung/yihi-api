package model

import (
	"time"

	"gorm.io/gorm/clause"
)

const MediaDiagnosticRetention = 7 * 24 * time.Hour

// MediaDiagnostic contains only sanitized payloads, independently of task data.
// Plain text avoids dialect-specific JSON encoding in the supported databases.
// These records always live in the primary database.
type MediaDiagnostic struct {
	ID        string `json:"id" gorm:"type:varchar(64);primaryKey"`
	RequestID string `json:"request_id" gorm:"type:varchar(191);index"`
	TaskID    string `json:"task_id,omitempty" gorm:"type:varchar(191);index"`
	ChannelID int    `json:"channel_id"`
	UserID    int    `json:"user_id"`
	Stage     string `json:"stage" gorm:"type:varchar(32)"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at" gorm:"index"`
	Payload   string `json:"payload" gorm:"type:text"`
}

func SaveMediaDiagnostic(entry *MediaDiagnostic) error {
	return DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"payload", "created_at"})}).Create(entry).Error
}

func GetMediaDiagnostics(requestID, taskID string) ([]MediaDiagnostic, error) {
	entries := make([]MediaDiagnostic, 0)
	query := DB.Where("expires_at > ?", time.Now().Unix())
	if taskID != "" && requestID != "" {
		query = query.Where("task_id = ? OR request_id = ?", taskID, requestID)
	} else if taskID != "" {
		query = query.Where("task_id = ?", taskID)
	} else if requestID != "" {
		query = query.Where("request_id = ?", requestID)
	} else {
		return entries, nil
	}
	err := query.Order("created_at ASC, id ASC").Limit(100).Find(&entries).Error
	return entries, err
}

func DeleteExpiredMediaDiagnostics(now int64) error {
	// Batches bound lock time and transaction size on busy installations.
	for {
		var ids []string
		if err := DB.Model(&MediaDiagnostic{}).Where("expires_at <= ?", now).Limit(500).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := DB.Where("id IN ?", ids).Delete(&MediaDiagnostic{}).Error; err != nil {
			return err
		}
	}
}
