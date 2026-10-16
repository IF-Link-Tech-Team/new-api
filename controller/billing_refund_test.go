package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBillingRefundHTTPRequiresRootAndRecordsReviewWithoutKeyDisclosure(t *testing.T) {
	user := setupIFLinkPayHTTPTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.BillingRefund{}))
	t.Cleanup(func() { require.NoError(t, model.DB.Exec("DELETE FROM billing_refunds").Error) })
	receipt := &model.BillingRefund{UserID: user.Id, RequestID: "refund-http-authorization", WalletQuota: 100}
	require.NoError(t, model.CreateBillingRefundReceipt(receipt))
	require.NoError(t, model.SaveBillingRefundReservation(receipt))
	require.NoError(t, model.QueueBillingRefund(receipt.ID))
	rootPAT, adminPAT := "synthetic-root-pat", "synthetic-admin-pat"
	root := &model.User{Username: "refund-root", AffCode: "refund-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1, AccessToken: &rootPAT}
	admin := &model.User{Username: "refund-admin", AffCode: "refund-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1, AccessToken: &adminPAT}
	require.NoError(t, model.DB.Create(root).Error)
	require.NoError(t, model.DB.Create(admin).Error)
	engine := gin.New()
	engine.GET("/api/billing-refund/", middleware.RootAuth(), ListBillingRefunds)
	engine.POST("/api/billing-refund/:id/review", middleware.RootAuth(), ReviewBillingRefund)
	for _, tc := range []struct {
		token string
		code  int
	}{{"", http.StatusUnauthorized}, {adminPAT, http.StatusForbidden}, {rootPAT, http.StatusOK}} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/billing-refund/", nil)
		if tc.token != "" {
			request.Header.Set("Authorization", "Bearer "+tc.token)
		}
		engine.ServeHTTP(response, request)
		assert.Equal(t, tc.code, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), rootPAT)
		assert.NotContains(t, response.Body.String(), adminPAT)
	}
	for _, tc := range []struct {
		token string
		code  int
	}{{adminPAT, http.StatusForbidden}, {rootPAT, http.StatusOK}} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/billing-refund/%d/review", receipt.ID), strings.NewReader(`{"action":"retry","reason":"synthetic outage resolved after independent verification"}`))
		request.Header.Set("Authorization", "Bearer "+tc.token)
		engine.ServeHTTP(response, request)
		assert.Equal(t, tc.code, response.Code, response.Body.String())
	}
	require.NoError(t, model.DB.First(receipt, receipt.ID).Error)
	assert.Equal(t, root.Id, receipt.ReviewedBy)
	assert.Contains(t, receipt.ReviewReason, "independent verification")
	var after model.User
	require.NoError(t, model.DB.First(&after, user.Id).Error)
	assert.Equal(t, 100, after.Quota, "review enqueues work and never directly credits a balance")
}
