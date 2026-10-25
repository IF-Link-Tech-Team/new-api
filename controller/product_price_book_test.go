package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProductOrderSelfStatusPreservesPaymentPrivacy(t *testing.T) {
	user, _ := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SubscriptionOrder{}))
	now := model.GetDBTimestamp()
	for i, status := range []string{common.TopUpStatusPending, model.SubscriptionOrderStatusPaidReview} {
		order := model.SubscriptionOrder{UserId: user.Id, TradeNo: fmt.Sprintf("self-order-%d", i), Status: status,
			ReservationExpiresAt: now - 10, ProviderPayload: "private-provider-payload", ProviderTradeNo: "private-provider-transaction"}
		require.NoError(t, model.DB.Create(&order).Error)
	}
	require.NoError(t, model.DB.Create(&model.SubscriptionOrder{UserId: user.Id + 1, TradeNo: "other-users-order"}).Error)
	router := gin.New()
	router.GET("/orders", func(c *gin.Context) {
		c.Set("id", user.Id)
		GetSubscriptionSelfOrders(c)
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/orders", nil))
	var result struct {
		Success bool                         `json:"success"`
		Data    []SubscriptionOrderStatusDTO `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.Success, response.Body.String())
	require.Len(t, result.Data, 2)
	assert.Equal(t, model.SubscriptionOrderStatusPaidReview, result.Data[0].Status)
	assert.Equal(t, common.TopUpStatusExpired, result.Data[1].Status)
	assert.Equal(t, now-10, result.Data[1].ReservationExpiresAt)
	for _, private := range []string{"private-provider-payload", "private-provider-transaction", "other-users-order"} {
		assert.NotContains(t, response.Body.String(), private)
	}
	var original model.SubscriptionOrder
	require.NoError(t, model.DB.Where("trade_no = ?", "self-order-0").First(&original).Error)
	assert.Equal(t, common.TopUpStatusPending, original.Status, "displaying expiry must not change payment evidence")
}

func TestProductCompensationRequiresRootAndKeepsReceiptPrivate(t *testing.T) {
	admin, _ := setupAccessTokenAudit(t)
	db := model.DB
	require.NoError(t, db.AutoMigrate(&model.SubscriptionPlan{}, &model.SubscriptionOrder{}, &model.PriceBookPrice{},
		&model.UserSubscription{}, &model.SubscriptionKeyBinding{}, &model.Token{}, &model.TopUp{}))
	root := &model.User{Username: "compensation-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(root).Error)
	read, _ := createScopedAccessToken(t, admin.Id, 0, "billing:read")
	adminWrite, _ := createScopedAccessToken(t, admin.Id, 0, "billing:write")
	rootRead, _ := createScopedAccessToken(t, root.Id, 0, "billing:read")
	rootWrite, _ := createScopedAccessToken(t, root.Id, 0, "billing:write")
	product := model.SubscriptionPlan{Title: "Receipt-backed promotion", PriceAmount: 17.25, Currency: "CNY",
		DurationUnit: model.SubscriptionDurationDay, DurationValue: 3, TotalAmount: 1000000, ModelScope: "review-model"}
	require.NoError(t, model.CreateProductPlan(db, &product))
	price := model.PriceBookPrice{Model: product.ModelScope, PriceVersion: "review-v1", Enabled: true, BillingMultiplier: 1,
		BillingExpression: `tier("base", p + c * 4)`, AllowedChannels: `[8]`,
		OfficialReferencePrice:  `{"input":1,"cached_input":0.2,"cache_creation":1,"output":4}`,
		UserSellingPrice:        `{"input":1,"cached_input":0.2,"cache_creation":1,"output":4}`,
		UpstreamSettlementPrice: `{"input":0.5,"cached_input":0.1,"cache_creation":0.5,"output":2}`,
		MaxUpstreamCost:         `{"input":0.5,"cached_input":0.1,"cache_creation":0.5,"output":2}`}
	require.NoError(t, model.CreateProductPriceBookVersion(db, product.Id, &price))
	product.Enabled, product.PriceVersion = true, price.PriceVersion
	require.NoError(t, model.UpdateProductPlan(db, &product, product.ConfigRevision))
	for i := range 2 {
		trade := fmt.Sprintf("compensation-api-%d", i)
		_, _, err := model.CreateSubscriptionOrder(admin.Id, product.Id, trade, "alipay", "alipay", true)
		require.NoError(t, err)
		require.NoError(t, model.ExpireSubscriptionOrder(trade, "alipay"))
		require.NoError(t, model.CompleteSubscriptionOrderWithReceipt(trade, "alipay", "alipay", model.PaymentReceipt{
			Amount: decimal.RequireFromString("17.25"), Currency: "CNY", ProviderTradeNo: "provider-" + trade,
			ProviderPayload: "private-receipt-body"}))
	}
	router := gin.New()
	router.Use(middleware.RequestId(), middleware.AccessTokenAudit())
	group := router.Group("/api/subscription/admin", middleware.AdminAuth())
	group.GET("/compensation-orders", AdminListProductCompensationOrders)
	group.POST("/compensation-orders/:trade_no/retry", middleware.RootAuth(), AdminRetryProductCompensation)
	path := "/api/subscription/admin/compensation-orders"
	response := accessTokenRequest(router, http.MethodGet, path+"?limit=1", read, "", "")
	var page struct {
		Success bool `json:"success"`
		Data    struct {
			Items  []ProductCompensationOrderDTO `json:"items"`
			NextId int                           `json:"next_id"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &page))
	require.True(t, page.Success, response.Body.String())
	require.Len(t, page.Data.Items, 1)
	assert.Equal(t, "compensation-api-1", page.Data.Items[0].TradeNo)
	assert.Equal(t, 17.25, page.Data.Items[0].PaidMoney)
	assert.NotContains(t, response.Body.String(), "private-receipt-body")
	require.Positive(t, page.Data.NextId)
	response = accessTokenRequest(router, http.MethodGet, fmt.Sprintf("%s?before_id=%d&limit=1", path, page.Data.NextId), read, "", "")
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &page))
	require.Len(t, page.Data.Items, 1)
	assert.Equal(t, "compensation-api-0", page.Data.Items[0].TradeNo)
	assert.Zero(t, page.Data.NextId)
	response = accessTokenRequest(router, http.MethodGet, path+"?before_id=1", read, "", "")
	assert.Contains(t, response.Body.String(), `"items":[]`, "empty lists must remain usable by the console")
	for _, query := range []string{"?limit=-1", "?limit=201", "?before_id=0", "?before_id=oops"} {
		response = accessTokenRequest(router, http.MethodGet, path+query, read, "", "")
		assert.Contains(t, response.Body.String(), `"success":false`)
	}
	path += "/compensation-api-0/retry"
	for _, token := range []string{read, adminWrite, rootRead} {
		response = accessTokenRequest(router, http.MethodPost, path, token, "", `{"note":"synthetic decision"}`)
		assert.Equal(t, http.StatusForbidden, response.Code)
	}
	response = accessTokenRequest(router, http.MethodPost, path, rootWrite, "", `{"note":" "}`)
	assert.Contains(t, response.Body.String(), `"success":false`)
	response = accessTokenRequest(router, http.MethodPost, path, rootWrite, "", `{"note":"synthetic reviewed receipt"}`)
	assert.Contains(t, response.Body.String(), `"success":true`)
	assert.Contains(t, response.Body.String(), `"changed":true`)
	assert.NotContains(t, response.Body.String(), "private-receipt-body")
	response = accessTokenRequest(router, http.MethodPost, path, rootWrite, "", `{"note":"second request"}`)
	assert.Contains(t, response.Body.String(), `"changed":false`)
	var order model.SubscriptionOrder
	require.NoError(t, db.Where("trade_no = ?", "compensation-api-0").First(&order).Error)
	assert.Equal(t, root.Id, order.CompensationActorId)
	assert.Equal(t, "synthetic reviewed receipt", order.CompensationNote)
	var audit model.AuditLog
	require.NoError(t, model.LOG_DB.Where("action = ?", "subscription.product_compensation_issue").First(&audit).Error)
	assert.Equal(t, root.Id, audit.UserId)
}

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
	group.PATCH("/plans/:id/prices/:price_id", AdminSetProductPriceBookVersionEnabled)
	group.POST("/plans", AdminCreateSubscriptionPlan)
	group.PUT("/plans/:id", AdminUpdateSubscriptionPlan)
	group.PATCH("/plans/:id", AdminUpdateSubscriptionPlanStatus)
	group.POST("/plans/:id/migrate-product", middleware.RootAuth(), AdminMigrateSubscriptionPlanToProduct)
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
	t.Run("public multipliers follow the frozen product book including disabled historical versions", func(t *testing.T) {
		assert.Equal(t, map[string]float64{"operator-model": 0.73}, subscriptionBillingMultipliers("", "product:41", "operator-model, operator-model,missing", "operator-v2"))
		require.NoError(t, db.Model(&model.PriceBookPrice{}).Where("id = ?", created.Data.Id).UpdateColumn("enabled", false).Error)
		assert.Equal(t, map[string]float64{"operator-model": 0.73}, subscriptionBillingMultipliers("", "product:41", "operator-model", "operator-v2"))
		assert.Empty(t, subscriptionBillingMultipliers("", "product:42", "operator-model", "operator-v2"), "another product cannot supply a missing version")
		require.NoError(t, db.Model(&model.PriceBookPrice{}).Where("id = ?", created.Data.Id).UpdateColumn("enabled", true).Error)
	})
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

	t.Run("generic draft and version conflict", func(t *testing.T) {
		payment.ComplianceConfirmed = true
		product := model.SubscriptionPlan{
			Title: "New administrator promotion", PriceAmount: 12.5, Currency: "CNY",
			TotalAmount: 8_000_000, DurationUnit: model.SubscriptionDurationDay, DurationValue: 23,
			QuotaResetPeriod: model.SubscriptionResetNever, ModelScope: "operator-model", BillingMultiplier: 1,
			Enabled: true, Listed: true, RedemptionEnabled: true,
		}
		body, err := common.Marshal(map[string]any{"plan": product, "product_owned_pricing": true})
		require.NoError(t, err)
		response := accessTokenRequest(router, http.MethodPost, "/api/subscription/admin/plans", read, "", string(body))
		require.Equal(t, http.StatusForbidden, response.Code)
		response = accessTokenRequest(router, http.MethodPost, "/api/subscription/admin/plans", write, "", string(body))
		var result struct {
			Success bool                   `json:"success"`
			Data    model.SubscriptionPlan `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
		require.True(t, result.Success, response.Body.String())
		product = result.Data
		assert.False(t, product.Enabled)
		assert.False(t, product.Listed)
		assert.False(t, product.RedemptionEnabled)
		assert.Empty(t, product.ProductType)
		assert.Zero(t, product.TemplateId)
		assert.Equal(t, fmt.Sprintf("product:%d", product.Id), product.PriceBook)
		assert.Equal(t, "CNY", product.Currency)
		assert.Equal(t, fmt.Sprint(product.Id), subscriptionPlanProductID(product))
		path := fmt.Sprintf("/api/subscription/admin/plans/%d", product.Id)
		product.Title = "Revised promotion"
		body, err = common.Marshal(map[string]any{"plan": product, "expected_revision": product.ConfigRevision})
		require.NoError(t, err)
		response = accessTokenRequest(router, http.MethodPut, path, write, "", string(body))
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
		require.True(t, result.Success, response.Body.String())
		assert.EqualValues(t, 2, result.Data.ConfigRevision)
		response = accessTokenRequest(router, http.MethodPut, path, write, "", string(body))
		assert.Equal(t, http.StatusConflict, response.Code)
		response = accessTokenRequest(router, http.MethodPatch, path, write, "", `{"listed":true,"expected_revision":1}`)
		assert.Equal(t, http.StatusConflict, response.Code)
		response = accessTokenRequest(router, http.MethodPatch, path, write, "", `{"enabled":true,"expected_revision":2}`)
		assert.Contains(t, response.Body.String(), `"success":false`, "missing prices must prevent activation")
		var after model.SubscriptionPlan
		require.NoError(t, db.First(&after, product.Id).Error)
		assert.EqualValues(t, 2, after.ConfigRevision)
		assert.Equal(t, "Revised promotion", after.Title)
		assert.False(t, after.Enabled)
		version := created.Data
		version.Id, version.PriceBook = 0, ""
		require.NoError(t, model.CreateProductPriceBookVersion(db, product.Id, &version))
		togglePath := fmt.Sprintf("%s/prices/%d", path, version.Id)
		payload := `{"enabled":false,"expected_revision":2}`
		response = accessTokenRequest(router, http.MethodPatch, togglePath, read, "", payload)
		assert.Equal(t, http.StatusForbidden, response.Code)
		for _, invalid := range []string{`{}`, `{"enabled":false}`, `{"expected_revision":2}`} {
			response = accessTokenRequest(router, http.MethodPatch, togglePath, write, "", invalid)
			assert.Contains(t, response.Body.String(), `"success":false`)
		}
		crossProductPath := fmt.Sprintf("%s/prices/%d", path, created.Data.Id)
		response = accessTokenRequest(router, http.MethodPatch, crossProductPath, write, "", payload)
		assert.Contains(t, response.Body.String(), `"success":false`, "price ownership must be checked on the server")
		payment.ComplianceConfirmed = false
		response = accessTokenRequest(router, http.MethodPatch, togglePath, write, "", payload)
		assert.Contains(t, response.Body.String(), `"success":false`)
		payment.ComplianceConfirmed = true
		response = accessTokenRequest(router, http.MethodPatch, togglePath, write, "", payload)
		var toggled struct {
			Success bool `json:"success"`
			Data    struct {
				Price    model.PriceBookPrice `json:"price"`
				Revision int64                `json:"config_revision"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &toggled))
		require.True(t, toggled.Success, response.Body.String())
		assert.False(t, toggled.Data.Price.Enabled)
		assert.EqualValues(t, 3, toggled.Data.Revision)
		response = accessTokenRequest(router, http.MethodPatch, togglePath, write, "", payload)
		assert.Equal(t, http.StatusConflict, response.Code)
		var untouched model.PriceBookPrice
		require.NoError(t, db.First(&untouched, created.Data.Id).Error)
		assert.True(t, untouched.Enabled)
		var statusAudit model.AuditLog
		require.NoError(t, model.LOG_DB.Where("action = ?", "subscription.price_book_version_status").First(&statusAudit).Error)
		auditJSON, err := common.Marshal(statusAudit)
		require.NoError(t, err)
		assert.Contains(t, string(auditJSON), `"previous_revision":2`)
		assert.Contains(t, string(auditJSON), `"config_revision":3`)
		assert.NotContains(t, string(auditJSON), write)
	})

	t.Run("explicit conversion requires root write scope and reviewed configuration", func(t *testing.T) {
		require.NoError(t, db.AutoMigrate(&model.ProductPrice{}))
		payment.ComplianceConfirmed = true
		source := model.ProductPrice{
			ProductType: model.ProductTypePackK3, Model: "conversion-model", PriceVersion: "conversion-v1",
			OfficialReferencePrice: `{"input":2,"output":8}`, UserSellingPrice: `{"input":1.9,"output":7.6}`,
			UpstreamSettlementPrice: `{"input":1,"output":4}`, MaxUpstreamCost: `{"input":1,"output":4}`,
			BillingExpression: `tier("default",p*2+c*8)`, BillingMultiplier: 0.95, AllowedChannels: `[8,9]`, Enabled: true,
		}
		require.NoError(t, db.Create(&source).Error)
		product := model.SubscriptionPlan{Title: "Existing promotion", ProductType: source.ProductType,
			Currency: "CNY", PriceAmount: 19.9, TotalAmount: 9950000, DurationUnit: model.SubscriptionDurationDay, DurationValue: 30,
			QuotaResetPeriod: model.SubscriptionResetNever, ModelScope: source.Model, PriceVersion: source.PriceVersion,
			BillingMultiplier: 0.95, TemplateId: 3, Enabled: true, Listed: true,
			AllowBalancePay: common.GetPointer(false), AllowWalletOverflow: common.GetPointer(false)}
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&product).Error)
		root := model.User{Username: "migration-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
		require.NoError(t, db.Create(&root).Error)
		rootRead, _ := createScopedAccessToken(t, root.Id, 0, "billing:read")
		rootWrite, _ := createScopedAccessToken(t, root.Id, 0, "billing:write")
		path := fmt.Sprintf("/api/subscription/admin/plans/%d/migrate-product", product.Id)
		for _, token := range []string{read, write, rootRead} {
			response := accessTokenRequest(router, http.MethodPost, path, token, "", `{"expected_revision":0}`)
			assert.Equal(t, http.StatusForbidden, response.Code)
		}
		response := accessTokenRequest(router, http.MethodPost, path, rootWrite, "", `{"expected_revision":0}`)
		var preview struct {
			Success bool                         `json:"success"`
			Data    model.ProductMigrationResult `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &preview))
		require.True(t, preview.Success, response.Body.String())
		var count int64
		require.NoError(t, db.Model(&model.PriceBookPrice{}).Where("price_book = ?", preview.Data.PriceBook).Count(&count).Error)
		assert.Zero(t, count, "preview never publishes prices")
		body, err := common.Marshal(map[string]any{"expected_revision": 0, "fingerprint": preview.Data.Fingerprint, "apply": true})
		require.NoError(t, err)
		response = accessTokenRequest(router, http.MethodPost, path, rootWrite, "", string(body))
		assert.Contains(t, response.Body.String(), `"success":false`, "a conversion needs an operator reason")
		body, err = common.Marshal(map[string]any{"expected_revision": 0, "fingerprint": preview.Data.Fingerprint, "apply": true, "reason": "Reviewed preserved configuration"})
		require.NoError(t, err)
		response = accessTokenRequest(router, http.MethodPost, path, rootWrite, "", string(body))
		assert.Contains(t, response.Body.String(), `"success":true`)
		var audit model.AuditLog
		require.NoError(t, model.LOG_DB.Where("action = ?", "subscription.product_migrate").First(&audit).Error)
		assert.Equal(t, root.Id, audit.UserId)
		var reason string
		encodedReason, err := common.Marshal(audit.Other.Op.Params["reason"])
		require.NoError(t, err)
		require.NoError(t, common.Unmarshal(encodedReason, &reason))
		assert.Equal(t, "Reviewed preserved configuration", reason)
	})
}
