package model

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestMigrateGrsaiChannelType(t *testing.T) {
	for _, engine := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var dialector gorm.Dialector
			switch engine {
			case "sqlite":
				dialector = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				dialector = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				dialector = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(dialector, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("grsaimig_%d_", time.Now().UnixNano())}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			defer sqlDB.Close()
			defer func() { require.NoError(t, db.Migrator().DropTable(&Task{}, &Channel{}, &Option{})) }()
			oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
			common.SetDatabaseTypes(common.DatabaseType(engine), oldLog)
			defer common.SetDatabaseTypes(oldMain, oldLog)
			for _, populated := range []bool{true, false} {
				require.NoError(t, db.Migrator().DropTable(&Task{}, &Channel{}, &Option{}))
				require.NoError(t, db.AutoMigrate(&Channel{}, &Task{}, &Option{}))
				if populated {
					require.NoError(t, db.Create(&Channel{Id: 91, Type: 59, Models: "minimax-h3", OtherSettings: `{"task_platform":"59","custom":"preserved"}`}).Error)
					require.NoError(t, db.Create(&Task{TaskID: "legacy-grsai", ChannelId: 91, Platform: "59", Status: TaskStatusInProgress}).Error)
				}
				require.NoError(t, migrateGrsaiChannelType(db))
				if populated {
					var channel Channel
					require.NoError(t, db.First(&channel, 91).Error)
					assert.Equal(t, constant.ChannelTypeGrsai, channel.Type)
					assert.JSONEq(t, `{"task_platform":"64","custom":"preserved"}`, channel.OtherSettings)
					var task Task
					require.NoError(t, db.Where("task_id = ?", "legacy-grsai").First(&task).Error)
					assert.Equal(t, constant.TaskPlatform("64"), task.Platform)
					assert.Equal(t, TaskStatus(TaskStatusInProgress), task.Status)
				}
				require.NoError(t, db.Create(&Channel{Id: 92, Type: constant.ChannelTypeSub2API, Models: "gpt-4"}).Error)
				require.NoError(t, db.Create(&Task{TaskID: "sub2api-task", ChannelId: 92, Platform: "59", Status: TaskStatusInProgress}).Error)
				require.NoError(t, db.AutoMigrate(&Channel{}, &Task{}, &Option{}))
				require.NoError(t, migrateGrsaiChannelType(db))
				var channel Channel
				require.NoError(t, db.First(&channel, 92).Error)
				assert.Equal(t, constant.ChannelTypeSub2API, channel.Type)
				var task Task
				require.NoError(t, db.Where("task_id = ?", "sub2api-task").First(&task).Error)
				assert.Equal(t, constant.TaskPlatform("59"), task.Platform)
			}
		})
	}
}
