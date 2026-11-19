package controller

import (
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPricingUsesOnlyViewerGroupChannelPrices(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	configureRoleRestrictedGroup(t)
	t.Setenv("IFLINK_API_CHANNEL_PRICING", "enabled")
	require.NoError(t, db.AutoMigrate(&model.APIChannelCatalog{}, &model.APIChannelPrice{}, &model.GroupModelPrice{}, &model.GroupWallet{}, &model.GroupChannel{}))
	require.NoError(t, model.InitializeAPIChannelCatalog(db))
	require.NoError(t, db.Model(&model.APIChannelCatalog{}).Where("id = ?", 1).Update("revision", 1).Error)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"Astro":1,"内部":0}`))
	withTieredBillingConfig(t, map[string]string{"shared": "tiered_expr"}, map[string]string{"shared": `tier("old", p * 0 + c * 0)`})
	require.NoError(t, db.Create(&model.GroupWallet{GroupName: "Astro", Mode: model.GroupWalletIndependent}).Error)
	for _, channel := range []model.Channel{
		{Id: 1, Type: 1, Name: "private upstream", Models: "shared,not-for-sale", Group: "default", Status: 1},
		{Id: 2, Type: 1, Name: "Astro upstream", Models: "shared,astro-only", Group: "Astro", Status: 1},
		{Id: 3, Type: 1, Models: "internal-only", Group: "内部", Status: 1},
	} {
		require.NoError(t, db.Create(&channel).Error)
		require.NoError(t, channel.AddAbilities(nil))
		for _, name := range channel.GetModels() {
			price := `{"input":0,"cached_input":0,"cache_creation":0,"output":0}`
			if channel.Id == 1 {
				price = `{"input":1,"cached_input":0.02,"cache_creation":1,"output":4}`
			}
			require.NoError(t, db.Create(&model.APIChannelPrice{Revision: 1, Model: name, ChannelID: channel.Id, UpstreamModel: name, SellingPrice: price, Enabled: name != "not-for-sale"}).Error)
		}
	}
	require.NoError(t, db.Create(&model.GroupChannel{GroupName: "Astro", ChannelID: 2, Owned: true, Reviewed: true}).Error)
	require.NoError(t, db.Create(&model.GroupModelPrice{GroupName: "Astro", ChannelID: 2, Model: "shared", Revision: 1, SellingPrice: `{"input":3,"cached_input":0.3,"cache_creation":3,"output":12}`}).Error)
	model.InvalidatePricingCache()
	t.Cleanup(model.InvalidatePricingCache)
	for _, tc := range []struct {
		group      string
		role       int
		wantGroups []string
		wantCount  int
	}{
		{"default", common.RoleCommonUser, []string{"default"}, 1},
		{"Astro", common.RoleCommonUser, []string{"Astro", "default"}, 2},
		{"default", common.RoleAdminUser, []string{"default"}, 2},
	} {
		t.Run(tc.group+strconv.Itoa(tc.role), func(t *testing.T) {
			user := model.User{Username: tc.group + strconv.Itoa(tc.role), Group: tc.group, Role: tc.role, Status: 1, AffCode: tc.group + strconv.Itoa(tc.role)}
			require.NoError(t, db.Create(&user).Error)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest("GET", "/api/pricing", nil)
			ctx.Set("id", user.Id)
			GetPricing(ctx)
			var response struct {
				Data []model.Pricing `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			require.Len(t, response.Data, tc.wantCount, recorder.Body.String())
			for _, item := range response.Data {
				if item.ModelName != "shared" {
					continue
				}
				assert.ElementsMatch(t, tc.wantGroups, item.EnableGroup)
				for _, variant := range item.GroupPricing {
					if variant.Group == "Astro" {
						assert.Contains(t, variant.BillingExpr, "p * 3")
					} else {
						assert.NotContains(t, variant.BillingExpr, "p * 3")
					}
				}
				if tc.group == "default" {
					assert.Contains(t, item.BillingExpr, "p * 1")
				}
			}
			assert.NotContains(t, recorder.Body.String(), "private upstream")
			assert.NotContains(t, recorder.Body.String(), "channel_id")
			assert.NotContains(t, recorder.Body.String(), "not-for-sale")
		})
	}
}
