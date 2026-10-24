package controller

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProductPriceBookAdministrativeAPI(t *testing.T) {
	admin, _ := setupAccessTokenAudit(t)
	db := model.DB
	require.NoError(t, db.AutoMigrate(&model.SubscriptionPlan{}, &model.PriceBookPrice{}))
	payment := operation_setting.GetPaymentSetting()
	previousPayment := *payment
	t.Cleanup(func() { *payment = previousPayment })
	payment.ComplianceConfirmed = true
	payment.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	// No product template or legacy price table exists in this database.
	plan := model.SubscriptionPlan{Id: 41, Title: "Administrator configured product", ProductType: "", PriceVersion: "existing-v1"}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&plan).Error)
	otherPlan := plan
	otherPlan.Id = 42
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&otherPlan).Error)
	read, _ := createScopedAccessToken(t, admin.Id, 0, "billing:read")
	write, _ := createScopedAccessToken(t, admin.Id, 0, "billing:write")
	router := gin.New()
	router.Use(middleware.RequestId(), middleware.AccessTokenAudit())
	group := router.Group("/api/subscription/admin", middleware.AdminAuth())
	group.GET("/plans/:id/prices", AdminListProductPriceBookVersions)
	group.POST("/plans/:id/prices", AdminCreateProductPriceBookVersion)
	price := model.PriceBookPrice{
		Id: 987, CreatedAt: 1, UpdatedAt: 1,
		Model: "operator-model", PriceVersion: "operator-v2", BillingMultiplier: 0.73, Enabled: true,
		OfficialReferencePrice:  `{"input":1,"cached_input":0.2,"cache_creation":1,"output":4}`,
		UserSellingPrice:        `{"input":1,"cached_input":0.2,"cache_creation":1,"output":4}`,
		UpstreamSettlementPrice: `{"input":0.5,"cached_input":0.1,"cache_creation":0.5,"output":2}`,
		BillingExpression:       `tier("base", p + cr * 0.2 + cc + c * 4)`,
		AllowedChannels:         `[8,9]`, MaxUpstreamCost: `{"input":0.5,"cached_input":0.1,"cache_creation":0.5,"output":2}`,
	}
	body, err := common.Marshal(map[string]any{"price": price})
	require.NoError(t, err)
	path := "/api/subscription/admin/plans/41/prices"
	response := accessTokenRequest(router, http.MethodPost, path, read, "", string(body))
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = accessTokenRequest(router, http.MethodPost, path, write, "", string(body))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var created struct {
		Success bool                 `json:"success"`
		Data    model.PriceBookPrice `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &created))
	require.True(t, created.Success, response.Body.String())
	assert.Equal(t, "product:41", created.Data.PriceBook)
	assert.NotEqual(t, 987, created.Data.Id)
	assert.Greater(t, created.Data.CreatedAt, int64(1))
	assert.Equal(t, 0.73, created.Data.BillingMultiplier)
	response = accessTokenRequest(router, http.MethodPost, path, write, "", string(body))
	assert.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	// The same model/version in another product has independent terms.
	otherPrice := price
	otherPrice.BillingMultiplier = 0.81
	require.NoError(t, model.CreateProductPriceBookVersion(db, 42, &otherPrice))
	price.PriceVersion = "operator-v3"
	require.NoError(t, model.CreateProductPriceBookVersion(db, 41, &price))
	type priceBookPageResponse struct {
		Success bool `json:"success"`
		Data    struct {
			Items  []model.PriceBookPrice `json:"items"`
			NextId int                    `json:"next_id"`
		} `json:"data"`
	}
	var page priceBookPageResponse
	response = accessTokenRequest(router, http.MethodGet, path+"?limit=1", read, "", "")
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &page))
	require.True(t, page.Success, response.Body.String())
	require.Len(t, page.Data.Items, 1)
	assert.Equal(t, "operator-v3", page.Data.Items[0].PriceVersion)
	require.Positive(t, page.Data.NextId)
	response = accessTokenRequest(router, http.MethodGet, fmt.Sprintf("%s?limit=1&before_id=%d", path, page.Data.NextId), read, "", "")
	page = priceBookPageResponse{}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &page))
	require.True(t, page.Success)
	require.Len(t, page.Data.Items, 1)
	assert.Equal(t, created.Data, page.Data.Items[0])
	assert.Zero(t, page.Data.NextId)
	for _, query := range []string{"?limit=-1", "?limit=201", "?before_id=-1", "?before_id=bad"} {
		response = accessTokenRequest(router, http.MethodGet, path+query, read, "", "")
		assert.Contains(t, response.Body.String(), `"success":false`, query)
	}
	for _, book := range []string{"wallet", "product:42"} {
		invalid := created.Data
		invalid.PriceBook = book
		body, err := common.Marshal(map[string]any{"price": invalid})
		require.NoError(t, err)
		response = accessTokenRequest(router, http.MethodPost, path, write, "", string(body))
		assert.Contains(t, response.Body.String(), `"success":false`, response.Body.String())
	}
	payment.ComplianceConfirmed = false
	response = accessTokenRequest(router, http.MethodPost, path, write, "", string(body))
	assert.Contains(t, response.Body.String(), `"success":false`)
	response = accessTokenRequest(router, http.MethodGet, path, read, "", "")
	assert.Contains(t, response.Body.String(), `"success":true`, "reading is independent of payment readiness")
	var storedPlan model.SubscriptionPlan
	require.NoError(t, db.First(&storedPlan, plan.Id).Error)
	assert.Equal(t, plan, storedPlan, "publishing a version must not activate it")
	var audit model.AuditLog
	require.NoError(t, model.LOG_DB.Where("action = ?", "subscription.price_book_version_create").First(&audit).Error)
	assert.Equal(t, admin.Id, audit.UserId)
	assert.Equal(t, common.RoleAdminUser, audit.ActorRole)
	require.NotNil(t, audit.Other.Op)
	bookJSON, err := common.Marshal(audit.Other.Op.Params["price_book"])
	require.NoError(t, err)
	assert.JSONEq(t, `"product:41"`, string(bookJSON))
	auditJSON, err := common.Marshal(audit)
	require.NoError(t, err)
	assert.NotContains(t, string(auditJSON), write)
}
