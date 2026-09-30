package relay

import (
	"github.com/QuantumNous/new-api/relay/channel/sub2api"
	taskdoubao "github.com/QuantumNous/new-api/relay/channel/task/doubao"
	taskgrsai "github.com/QuantumNous/new-api/relay/channel/task/grsai"
	"strconv"
	"testing"

	appcommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
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

func TestNativeTaskProvidersRemainAvailableAfterPluginMerge(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	platform, adaptor := getTaskAdaptorForRequest(c, constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGrsai)))
	require.Equal(t, constant.TaskPlatform("64"), platform)
	require.IsType(t, &taskgrsai.TaskAdaptor{}, adaptor)
	require.IsType(t, &sub2api.Adaptor{}, GetAdaptor(constant.APITypeSub2API))
	appcommon.SetContextKey(c, constant.ContextKeyOriginalModel, "seedance-custom")
	appcommon.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{
		SeedanceUpscale: map[string]dto.SeedanceUpscaleModelConfig{"seedance-custom": {}},
	})
	platform, adaptor = getTaskAdaptorForRequest(c, "doubao")
	require.Equal(t, constant.TaskPlatformDoubaoNative, platform)
	require.IsType(t, &taskdoubao.TaskAdaptor{}, adaptor)
}
