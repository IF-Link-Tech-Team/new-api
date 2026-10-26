package helper

import (
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

func ModelMappedHelper(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) error {
	if info.ChannelMeta == nil {
		info.ChannelMeta = &relaycommon.ChannelMeta{}
	}

	mapped, err := model.ResolveMappedModelName(c.GetString("model_mapping"), info.OriginModelName)
	if err != nil {
		return err
	}
	info.IsModelMapped = mapped != info.OriginModelName
	info.UpstreamModelName = mapped

	if request != nil {
		request.SetModelName(info.UpstreamModelName)
	}
	return nil
}
