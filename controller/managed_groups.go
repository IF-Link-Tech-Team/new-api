package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func groupResult(c *gin.Context, value any, err error) {
	if errors.Is(err, authz.ErrGroupAccess) || errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Group permission denied"))
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, value)
}

func GetManagedGroups(c *gin.Context) {
	groups, err := service.ManagedGroups(c.GetInt("id"))
	if err != nil {
		groupResult(c, nil, err)
		return
	}
	var request model.GroupJoinRequest
	query := model.DB.Where("user_id = ?", c.GetInt("id")).First(&request)
	if query.Error != nil && !errors.Is(query.Error, gorm.ErrRecordNotFound) {
		groupResult(c, nil, query.Error)
		return
	}
	common.ApiSuccess(c, gin.H{"groups": groups, "permissions": authz.GroupPermissionCatalog, "join_request": request})
}

func GetOwnGroupWallet(c *gin.Context) {
	member, wallet, err := service.GetOwnGroupWallet(c.GetInt("id"))
	groupResult(c, gin.H{"member": member, "wallet": wallet}, err)
}

func GetManagedGroupWallet(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	view, err := service.GetGroupWallet(c.GetInt("id"), c.Param("group"), min(1000000, max(1, page)))
	groupResult(c, view, err)
}

func SetManagedGroupWallet(c *gin.Context) {
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if c.ShouldBindJSON(&input) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	err := service.SetGroupWalletEnabled(c.GetInt("id"), c.Param("group"), input.Enabled)
	if err == nil {
		recordManageAudit(c, "group.wallet.configure", map[string]any{"group": c.Param("group"), "enabled": input.Enabled})
	}
	groupResult(c, nil, err)
}

func SetManagedGroupWalletMode(c *gin.Context) {
	var input struct {
		Mode string `json:"mode"`
	}
	if c.ShouldBindJSON(&input) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	err := service.SetGroupWalletMode(c.GetInt("id"), c.Param("group"), input.Mode)
	if err == nil {
		recordManageAudit(c, "group.wallet.type", map[string]any{"group": c.Param("group"), "mode": input.Mode})
	}
	groupResult(c, nil, err)
}

func FundManagedGroupWallet(c *gin.Context) {
	var input struct {
		Delta       int64  `json:"delta"`
		Reason      string `json:"reason"`
		OperationID string `json:"operation_id"`
	}
	if c.ShouldBindJSON(&input) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	err := service.FundGroupWallet(c.GetInt("id"), c.Param("group"), input.Delta, input.Reason, input.OperationID)
	if err == nil {
		recordManageAudit(c, "group.wallet.fund", map[string]any{"group": c.Param("group"), "quota": input.Delta, "reason": input.Reason})
	}
	groupResult(c, nil, err)
}

func AllocateManagedGroupWallet(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	var input struct {
		Delta       int64  `json:"delta"`
		Reason      string `json:"reason"`
		OperationID string `json:"operation_id"`
	}
	if err != nil || id <= 0 || c.ShouldBindJSON(&input) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	err = service.AllocateGroupWallet(c.GetInt("id"), c.Param("group"), id, input.Delta, input.Reason, input.OperationID)
	if err == nil {
		recordManageAuditFor(c, id, "group.wallet.allocate", map[string]any{"group": c.Param("group"), "quota": input.Delta, "reason": input.Reason})
	}
	groupResult(c, nil, err)
}

func RequestManagedGroupJoin(c *gin.Context) {
	var input struct {
		Group string `json:"group"`
	}
	if c.ShouldBindJSON(&input) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	err := service.RequestGroupMembership(c.GetInt("id"), input.Group)
	if err == nil {
		recordManageAuditFor(c, c.GetInt("id"), "group.join_request", map[string]any{"group": input.Group})
	}
	groupResult(c, nil, err)
}

func GetManagedGroupMembers(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	value, err := service.ListGroupMembers(c.GetInt("id"), c.Param("group"), min(1000000, max(1, page)))
	groupResult(c, value, err)
}

func ReviewManagedGroupJoin(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	var input struct {
		Approve bool `json:"approve"`
	}
	if err != nil || id <= 0 || c.ShouldBindJSON(&input) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	err = service.ReviewGroupMembership(c.GetInt("id"), id, c.Param("group"), input.Approve)
	if err == nil {
		recordManageAuditFor(c, id, "group.review_join", map[string]any{"group": c.Param("group"), "approve": input.Approve})
	}
	groupResult(c, nil, err)
}

func RemoveManagedGroupMember(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	err = service.RemoveGroupMember(c.GetInt("id"), id, c.Param("group"))
	if err == nil {
		recordManageAuditFor(c, id, "group.remove_member", map[string]any{"group": c.Param("group")})
	}
	groupResult(c, nil, err)
}

func GetManagedGroupChannels(c *gin.Context) {
	value, err := service.ListGroupChannels(c.GetInt("id"), c.Param("group"))
	groupResult(c, value, err)
}

func CreateManagedGroupChannel(c *gin.Context) {
	var input service.NewGroupChannel
	if c.ShouldBindJSON(&input) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	id, err := service.CreateGroupChannel(c.GetInt("id"), c.Param("group"), input)
	if err == nil {
		model.InitChannelCache()
		recordManageAudit(c, "group.create_channel", map[string]any{"group": c.Param("group"), "channel_id": id})
	}
	groupResult(c, gin.H{"id": id}, err)
}

func SetManagedGroupChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if err != nil || id <= 0 || c.ShouldBindJSON(&input) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	err = service.SetGroupChannelEnabled(c.GetInt("id"), c.Param("group"), id, input.Enabled)
	if err == nil {
		model.InitChannelCache()
		recordManageAudit(c, "group.set_channel", map[string]any{"group": c.Param("group"), "channel_id": id, "enabled": input.Enabled})
	}
	groupResult(c, nil, err)
}

func GetGroupAdminCatalog(c *gin.Context) {
	channels, err := service.GroupChannelCatalog(c.Query("group"))
	groupResult(c, channels, err)
}

func GetGroupGrants(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	rows, err := service.GroupGrantRows(id, c.Param("group"))
	groupResult(c, rows, err)
}

func SetGroupGrants(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	var input struct {
		Permissions []string `json:"permissions"`
	}
	if err != nil || id <= 0 || c.ShouldBindJSON(&input) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if requireAdminUserProof(c, service.VerificationScopeAdminUserUpdate, service.AdminUserContext{UserID: id}) == nil {
		return
	}
	err = service.SetGroupGrants(c.GetInt("id"), id, c.Param("group"), input.Permissions)
	if err == nil {
		recordManageAuditFor(c, id, "group.set_grants", map[string]any{"group": c.Param("group"), "permissions": input.Permissions})
	}
	groupResult(c, nil, err)
}

func OfferManagedGroupChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	var input struct {
		Allowed bool `json:"allowed"`
	}
	if err != nil || id <= 0 || c.ShouldBindJSON(&input) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	err = service.OfferGroupChannel(c.GetInt("id"), c.Param("group"), id, input.Allowed)
	if err == nil {
		model.InitChannelCache()
		recordManageAudit(c, "group.offer_channel", map[string]any{"group": c.Param("group"), "channel_id": id, "allowed": input.Allowed})
	}
	groupResult(c, nil, err)
}

func ApproveManagedGroupChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	err = service.ApproveGroupChannel(c.GetInt("id"), c.Param("group"), id)
	if err == nil {
		recordManageAudit(c, "group.approve_channel", map[string]any{"group": c.Param("group"), "channel_id": id})
	}
	groupResult(c, nil, err)
}

func GetManagedGroupModels(c *gin.Context) {
	rows, err := service.GetGroupModelSettings(c.GetInt("id"), c.Param("group"))
	groupResult(c, rows, err)
}

func SetManagedGroupModels(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	var input struct {
		Models         string `json:"models"`
		ExpectedModels string `json:"expected_models"`
	}
	if err != nil || id <= 0 || c.ShouldBindJSON(&input) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	err = service.SetGroupChannelModels(c.GetInt("id"), c.Param("group"), id, input.ExpectedModels, input.Models)
	if err == nil {
		model.InitChannelCache()
		recordManageAudit(c, "group.channel.models", map[string]any{"group": c.Param("group"), "channel_id": id, "models": input.Models})
	}
	groupResult(c, nil, err)
}

func SetManagedGroupPrice(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	var input service.GroupModelRateInput
	if err != nil || id <= 0 || c.ShouldBindJSON(&input) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	err = service.SaveGroupModelRate(c.GetInt("id"), c.Param("group"), id, input)
	if err == nil {
		recordManageAudit(c, "group.model.price", map[string]any{"group": c.Param("group"), "channel_id": id, "rate": input})
	}
	groupResult(c, nil, err)
}

func GetManagedGroupUsage(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	rows, err := service.GetGroupUsage(c.GetInt("id"), c.Param("group"), min(1000000, max(1, page)))
	groupResult(c, rows, err)
}
