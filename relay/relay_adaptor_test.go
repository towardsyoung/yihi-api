package relay

import (
	"testing"

	appcommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetTaskPlatformUsesChannelOtherSettingsOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Set("channel_type", constant.ChannelTypeDoubaoVideo)
	appcommon.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{
		TaskPlatform: string(constant.TaskPlatformCloneFS),
	})

	require.Equal(t, constant.TaskPlatformCloneFS, GetTaskPlatform(c))
}

func TestGetTaskPlatformWithInfoUsesRelayInfoOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Set("channel_type", constant.ChannelTypeDoubaoVideo)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{
				TaskPlatform: string(constant.TaskPlatformCloneFS),
			},
		},
	}

	require.Equal(t, constant.TaskPlatformCloneFS, GetTaskPlatformWithInfo(c, info))
}
