package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetPrivateUserAliases(c *gin.Context) {
	aliases, err := model.GetUserAliases(c.GetInt("id"))
	groupResult(c, aliases, err)
}

func SetPrivateUserAlias(c *gin.Context) {
	userID, err := strconv.Atoi(c.Param("id"))
	var input struct {
		Alias *string `json:"alias"`
		Group string  `json:"group"`
	}
	if err != nil || userID <= 0 || c.ShouldBindJSON(&input) != nil || input.Alias == nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	// Alias text is private: do not copy it into globally visible audit logs.
	groupResult(c, nil, service.SetPrivateUserAlias(c.GetInt("id"), userID, *input.Alias, input.Group))
}
