package model

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"gorm.io/gorm"
)

// migrateGrsaiChannelType reserves upstream's type 59 for Sub2API. This fork
// used 59 for Grsai before the merge; the option makes the remap run once,
// including on an empty database, so subsequently created Sub2API rows stay 59.
func migrateGrsaiChannelType(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		marker := Option{Key: "migration.grsai_channel_type_64"}
		if err := tx.Where(&Option{Key: marker.Key}).FirstOrCreate(&marker).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where(&Option{Key: marker.Key}).First(&marker).Error; err != nil {
			return err
		}
		if marker.Value == "done" {
			return nil
		}
		var channels []Channel
		if err := tx.Where("type = ?", 59).Find(&channels).Error; err != nil {
			return err
		}
		for _, channel := range channels {
			updates := map[string]any{"type": constant.ChannelTypeGrsai}
			if channel.OtherSettings != "" {
				var settings map[string]any
				if err := common.UnmarshalJsonStr(channel.OtherSettings, &settings); err != nil {
					return err
				}
				if settings["task_platform"] == "59" {
					settings["task_platform"] = strconv.Itoa(constant.ChannelTypeGrsai)
					data, err := common.Marshal(settings)
					if err != nil {
						return err
					}
					updates["settings"] = string(data)
				}
			}
			if err := tx.Model(&Channel{}).Where("id = ?", channel.Id).Updates(updates).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&Task{}).Where("platform = ?", "59").Update("platform", strconv.Itoa(constant.ChannelTypeGrsai)).Error; err != nil {
			return err
		}
		return tx.Model(&Option{}).Where(&Option{Key: marker.Key}).Update("value", "done").Error
	})
}
