package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func configureRoleRestrictedGroup(t *testing.T) {
	t.Helper()
	previousRoles := ratio_setting.GetGroupRatioSetting().GroupMinRole.MarshalJSONString()
	previousGroups := setting.UserUsableGroups2JSONString()
	previousRatios := ratio_setting.GroupRatio2JSONString()
	previousAuto := setting.AutoGroups2JsonString()
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"group_ratio_setting.group_min_role": `{"内部":10}`,
	}))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","auto":"Auto"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"内部":1}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["内部","default"]`))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
			"group_ratio_setting.group_min_role": previousRoles,
		}))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(previousAuto))
	})
}

func TestGroupRoleAccessVisibilityAndAutoRouting(t *testing.T) {
	setupModelListControllerTestDB(t)
	configureRoleRestrictedGroup(t)
	for _, tc := range []struct {
		name  string
		role  int
		group string
		allow bool
	}{
		{"anonymous", common.RoleGuestUser, "", false},
		{"ordinary public user", common.RoleCommonUser, "default", false},
		{"ordinary member", common.RoleCommonUser, "内部", false},
		{"administrator member", common.RoleAdminUser, "内部", true},
		{"super administrator member", common.RoleRootUser, "内部", true},
		{"administrator in default group", common.RoleAdminUser, "default", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groups := service.GetUserUsableGroups(tc.group, tc.role)
			_, visible := groups["内部"]
			assert.Equal(t, tc.allow, visible)
			assert.Contains(t, groups, "default")
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set("role", tc.role)
			for _, explicit := range []bool{false, true} {
				if explicit {
					common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"内部", "default"})
				}
				auto := service.GetRequestAutoGroups(ctx, tc.group)
				assert.Equal(t, tc.allow, common.StringsContains(auto, "内部"))
				assert.Contains(t, auto, "default")
			}
			pricing := []model.Pricing{
				{ModelName: "internal-only", EnableGroup: []string{"内部"}},
				{ModelName: "shared", EnableGroup: []string{"default", "内部"}},
			}
			visiblePricing := filterPricingByUsableGroups(pricing, groups)
			wantCount := 1
			if tc.allow {
				wantCount = 2
			}
			assert.Len(t, visiblePricing, wantCount)
			assert.Equal(t, tc.allow, common.StringsContains(visiblePricing[len(visiblePricing)-1].EnableGroup, "内部"))
			assert.Equal(t, []string{"default", "内部"}, pricing[1].EnableGroup, "filter must not mutate the shared pricing cache")
		})
	}
	assert.NotContains(t, service.GetUserUsableGroups("内部"), "内部", "missing role must fail closed for restricted groups")
}

func TestGroupRoleAccessRelayAndModelDiscovery(t *testing.T) {
	configureRoleRestrictedGroup(t)
	db := setupModelListControllerTestDB(t)
	require.NoError(t, i18n.Init())
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.Option{}))
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "public-model", ChannelId: 1, Enabled: true},
		{Group: "内部", Model: "internal-model", ChannelId: 2, Enabled: true},
	}).Error)
	for i, tc := range []struct {
		name, userGroup, tokenGroup string
		role, status                int
	}{
		{"ordinary implicit internal", "内部", "", common.RoleCommonUser, http.StatusForbidden},
		{"ordinary explicit internal", "内部", "内部", common.RoleCommonUser, http.StatusForbidden},
		{"ordinary forged internal", "default", "内部", common.RoleCommonUser, http.StatusForbidden},
		{"ordinary public preserved", "default", "", common.RoleCommonUser, http.StatusNoContent},
		{"administrator internal", "内部", "内部", common.RoleAdminUser, http.StatusNoContent},
		{"super administrator internal", "内部", "", common.RoleRootUser, http.StatusNoContent},
		{"administrator in default group", "default", "内部", common.RoleAdminUser, http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := model.User{Username: fmt.Sprintf("group-role-user-%d", i), Role: tc.role, Group: tc.userGroup,
				Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: fmt.Sprintf("role-test-%d", i)}
			require.NoError(t, db.Create(&user).Error)
			key := strings.Repeat(string(rune('a'+i)), 48)
			token := model.Token{UserId: user.Id, Name: tc.name, Key: key, Group: tc.tokenGroup,
				Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1}
			require.NoError(t, db.Create(&token).Error)
			router := gin.New()
			router.GET("/v1/models", middleware.TokenAuth(), func(c *gin.Context) {
				assert.Equal(t, tc.role, c.GetInt("role"), "relay role must come from the authoritative user")
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			request.Header.Set("Authorization", "Bearer "+key)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			if tc.role >= common.RoleAdminUser {
				require.NoError(t, db.Model(&user).Update("role", common.RoleCommonUser).Error)
				// Re-send the same valid credential after demotion.
				demotedRequest := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
				demotedRequest.Header.Set("Authorization", "Bearer "+key)
				recorder = httptest.NewRecorder()
				router.ServeHTTP(recorder, demotedRequest)
				assert.Equal(t, http.StatusForbidden, recorder.Code)
				require.NoError(t, db.Model(&user).Update("role", tc.role).Error)
			}

			recorder = httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user/self/models?group=内部", nil)
			ctx.Set("id", user.Id)
			ctx.Set("role", user.Role)
			GetUserModels(ctx)
			var response userModelsResponse
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, tc.role >= common.RoleAdminUser, common.StringsContains(response.Data, "internal-model"))

			recorder = httptest.NewRecorder()
			ctx, _ = gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user/self/groups", nil)
			ctx.Set("id", user.Id)
			ctx.Set("role", tc.role)
			GetUserGroups(ctx)
			var groupResponse struct {
				Data map[string]any `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &groupResponse))
			_, listed := groupResponse.Data["内部"]
			assert.Equal(t, tc.role >= common.RoleAdminUser, listed)
		})
	}
}

func TestIndependentMemberCanChooseNormalGroupKey(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	configureRoleRestrictedGroup(t)
	withSelfUseModeEnabled(t)
	t.Setenv("IFLINK_API_CHANNEL_PRICING", "legacy")
	previousMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previousMemoryCache })
	require.NoError(t, i18n.Init())
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.Option{}, &model.UserSubscription{}, &model.SubscriptionKeyBinding{}))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"Astro":1,"OtherPrivate":1,"内部":0}`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","Astro":"Astro","OtherPrivate":"Other private group"}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["default","Astro","OtherPrivate"]`))
	require.NoError(t, db.Create(&[]model.GroupWallet{
		{GroupName: "Astro", Mode: model.GroupWalletIndependent, Enabled: true},
		{GroupName: "OtherPrivate", Mode: model.GroupWalletIndependent, Enabled: true},
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "normal-model", ChannelId: 1, Enabled: true},
		{Group: "Astro", Model: "astro-model", ChannelId: 2, Enabled: true},
		{Group: "OtherPrivate", Model: "other-private-model", ChannelId: 3, Enabled: true},
		{Group: "内部", Model: "internal-model", ChannelId: 4, Enabled: true},
	}).Error)
	for i, tc := range []struct {
		name, userGroup, tokenGroup string
		status                      int
		model                       string
		subscriptionID              int
	}{
		{"member normal key", "Astro", "default", http.StatusOK, "normal-model", 0},
		{"member own key", "Astro", "Astro", http.StatusOK, "astro-model", 0},
		{"member existing implicit key", "Astro", "", http.StatusOK, "astro-model", 0},
		{"member existing plan key", "Astro", "", http.StatusOK, "normal-model", 101},
		{"member cannot use other private group", "Astro", "OtherPrivate", http.StatusForbidden, "", 0},
		{"member cannot use internal group", "Astro", "内部", http.StatusForbidden, "", 0},
		{"outsider cannot use Astro", "default", "Astro", http.StatusForbidden, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := model.User{Username: fmt.Sprintf("independent-user-%d", i), AffCode: fmt.Sprintf("independent-%d", i), Group: tc.userGroup, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
			require.NoError(t, db.Create(&user).Error)
			key := strings.Repeat(string(rune('a'+i)), 48)
			token := model.Token{UserId: user.Id, Name: tc.name, Key: key, Group: tc.tokenGroup, UserSubscriptionId: tc.subscriptionID, Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1}
			if tc.subscriptionID > 0 {
				require.NoError(t, db.Create(&model.UserSubscription{Id: tc.subscriptionID, UserId: user.Id, Status: "active", EndTime: common.GetTimestamp() + 3600, ModelScope: "normal-model"}).Error)
			}
			require.NoError(t, db.Create(&token).Error)
			if tc.subscriptionID > 0 {
				require.NoError(t, db.Create(&model.SubscriptionKeyBinding{UserId: user.Id, UserSubscriptionId: tc.subscriptionID, TokenId: token.Id}).Error)
			}
			router := gin.New()
			router.GET("/v1/models", middleware.TokenAuth(), func(c *gin.Context) { ListModels(c, constant.ChannelTypeOpenAI) })
			request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			request.Header.Set("Authorization", "Bearer "+key)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			if tc.status == http.StatusOK {
				var response struct {
					Data []struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
				require.Len(t, response.Data, 1)
				assert.Equal(t, tc.model, response.Data[0].ID)
			}
		})
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/token/auto-groups", nil)
	ctx.Set("role", common.RoleCommonUser)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "Astro")
	GetTokenAutoGroups(ctx)
	var response struct {
		Data struct {
			Groups    []string `json:"groups"`
			Available []string `json:"available_groups"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, []string{"Astro"}, response.Data.Groups)
	assert.Equal(t, []string{"Astro"}, response.Data.Available)
}
