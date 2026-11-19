package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

func registerManagedGroupRoutes(api *gin.RouterGroup) {
	// Deliberately separate from global admin endpoints. A group grant never
	// promotes a user or changes their access to any existing management route.
	groups := api.Group("/managed-groups", middleware.UserAuth(), middleware.DisableCache())
	groups.GET("", controller.GetManagedGroups)
	groups.GET("/wallet", controller.GetOwnGroupWallet)
	groups.GET("/:group/wallet", controller.GetManagedGroupWallet)
	groups.PATCH("/:group/wallet", middleware.CriticalRateLimit(), controller.SetManagedGroupWallet)
	groups.POST("/:group/wallet/funds", middleware.CriticalRateLimit(), controller.FundManagedGroupWallet)
	groups.POST("/:group/wallet/allocations/:id", middleware.CriticalRateLimit(), controller.AllocateManagedGroupWallet)
	groups.POST("/join", middleware.CriticalRateLimit(), controller.RequestManagedGroupJoin)
	groups.GET("/:group/members", controller.GetManagedGroupMembers)
	groups.POST("/:group/requests/:id", controller.ReviewManagedGroupJoin)
	groups.DELETE("/:group/members/:id", controller.RemoveManagedGroupMember)
	groups.GET("/:group/usage", controller.GetManagedGroupUsage)
	groups.GET("/:group/models", controller.GetManagedGroupModels)
	groups.PUT("/:group/channels/:id/models", middleware.CriticalRateLimit(), controller.SetManagedGroupModels)
	groups.PUT("/:group/channels/:id/prices", middleware.CriticalRateLimit(), controller.SetManagedGroupPrice)
	groups.GET("/:group/channels", controller.GetManagedGroupChannels)
	groups.POST("/:group/channels", middleware.CriticalRateLimit(), controller.CreateManagedGroupChannel)
	groups.PATCH("/:group/channels/:id", controller.SetManagedGroupChannel)
	admin := api.Group("/group-admin", middleware.RootAuth(), middleware.DisableCache())
	admin.GET("/channels", controller.GetGroupAdminCatalog)
	admin.PATCH("/:group/wallet", middleware.CriticalRateLimit(), controller.SetManagedGroupWallet)
	admin.PUT("/:group/wallet/type", middleware.CriticalRateLimit(), controller.SetManagedGroupWalletMode)
	admin.POST("/:group/wallet/funds", middleware.CriticalRateLimit(), controller.FundManagedGroupWallet)
	admin.GET("/:group/grants/:id", controller.GetGroupGrants)
	admin.PUT("/:group/grants/:id", middleware.CriticalRateLimit(), controller.SetGroupGrants)
	admin.PUT("/:group/channels/:id", controller.OfferManagedGroupChannel)
	admin.POST("/:group/channels/:id/approve", controller.ApproveManagedGroupChannel)
}
