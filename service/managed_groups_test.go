package service

import (
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestManagedGroupsAuthorization(t *testing.T) {
	previousDB, previousType, previousRedis := model.DB, common.MainDatabaseType(), common.RedisEnabled
	previousRatios := ratio_setting.GroupRatio2JSONString()
	previousRoles := ratio_setting.GetGroupRatioSetting().GroupMinRole.MarshalJSONString()
	var dialector gorm.Dialector
	versionSQL := "SELECT sqlite_version()"
	switch os.Getenv("GROUP_RBAC_DIALECT") {
	case "", "sqlite":
		dialector = sqlite.Open(filepath.Join(t.TempDir(), "groups.db"))
		common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	case "mysql":
		dialector = mysql.Open(os.Getenv("GROUP_RBAC_DSN"))
		common.SetMainDatabaseType(common.DatabaseTypeMySQL)
		versionSQL = "SELECT VERSION()"
	case "postgres":
		dialector = postgres.Open(os.Getenv("GROUP_RBAC_DSN"))
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
		versionSQL = "SELECT VERSION()"
	default:
		t.Fatal("unsupported GROUP_RBAC_DIALECT")
	}
	db, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	if os.Getenv("GROUP_RBAC_DSN") != "" {
		query := "SELECT DATABASE()"
		if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
			query = "SELECT current_database()"
		}
		var name string
		require.NoError(t, db.Raw(query).Scan(&name).Error)
		require.True(t, strings.HasPrefix(name, "group_rbac_"), "only disposable group_rbac_ databases may be used")
	}
	require.False(t, db.Migrator().HasTable(&model.User{}), "refuse to change an existing application database")
	model.DB, common.RedisEnabled = db, false
	t.Cleanup(func() {
		model.DB, common.RedisEnabled = previousDB, previousRedis
		common.SetMainDatabaseType(previousType)
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"group_ratio_setting.group_min_role": previousRoles}))
		sqlDB, closeErr := db.DB()
		require.NoError(t, closeErr)
		require.NoError(t, sqlDB.Close())
	})
	var version string
	require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
	t.Logf("database: %s", version)
	t.Setenv("IFLINK_API_CHANNEL_PRICING", "enabled")
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"alpha":1,"beta":1,"内部":0}`))
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"group_ratio_setting.group_min_role": `{"内部":10}`}))

	// Upgrade a representative old schema with real user/channel/price data.
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Ability{}, &model.APIChannelCatalog{}, &model.APIChannelPrice{}))
	users := []model.User{
		{Id: 1, Username: "root", Role: 100, Status: 1, Group: "default"},
		{Id: 2, Username: "manager", Role: 1, Status: 1, Group: "default", Quota: 12345},
		{Id: 3, Username: "member", Role: 1, Status: 1, Group: "default", Quota: 67890},
		{Id: 4, Username: "other", Role: 1, Status: 1, Group: "beta"},
		{Id: 5, Username: "admin", Role: 10, Status: 1, Group: "alpha"},
	}
	for i := range users {
		users[i].AffCode = users[i].Username
	}
	require.NoError(t, db.Create(&users).Error)
	channel := model.Channel{Id: 10, Name: "Shared", Key: "private-upstream-key", Group: "default", Models: "test-model", Status: 1, Type: 1, UpstreamCosts: `{"kept":true}`}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, channel.AddAbilities(db))
	require.NoError(t, db.Create(&model.APIChannelCatalog{ID: 1, Revision: 1}).Error)
	price := model.APIChannelPrice{Revision: 1, Model: "test-model", ChannelID: 10, UpstreamModel: "test-model", SellingPrice: `{"input":2,"cached_input":1,"cache_creation":3,"output":4}`, Enabled: true}
	require.NoError(t, db.Create(&price).Error)
	for range 2 {
		require.NoError(t, db.AutoMigrate(&model.GroupGrant{}, &model.GroupChannel{}, &model.GroupJoinRequest{}, &model.GroupWallet{}, &model.GroupModelPrice{}))
	}
	permissions := []string{authz.GroupMembersRead, authz.GroupMembersManage, authz.GroupChannelsRead, authz.GroupChannelsCreate, authz.GroupChannelsManage, authz.GroupChannelsAttach}
	require.NoError(t, SetGroupGrants(1, 2, "alpha", permissions))
	// Re-running startup migrations preserves both new grants and old records.
	for range 2 {
		require.NoError(t, db.AutoMigrate(&model.GroupGrant{}, &model.GroupChannel{}, &model.GroupJoinRequest{}, &model.GroupWallet{}, &model.GroupModelPrice{}))
	}

	t.Run("scope_and_role_boundaries", func(t *testing.T) {
		groups, err := ManagedGroups(2)
		require.NoError(t, err)
		require.Len(t, groups, 1)
		assert.Equal(t, "alpha", groups[0].Name)
		_, err = ListGroupMembers(2, "beta", 1)
		assert.ErrorIs(t, err, authz.ErrGroupAccess)
		assert.ErrorIs(t, SetGroupGrants(2, 4, "alpha", permissions), authz.ErrGroupAccess)
		assert.ErrorIs(t, SetGroupGrants(1, 2, "内部", permissions), authz.ErrGroupAccess)
		assert.Error(t, SetGroupGrants(1, 2, "alpha", []string{"unknown"}))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"alpha":1,"beta":1,"内部":0,"alpha,beta":1}`))
		assert.ErrorIs(t, SetGroupGrants(1, 2, "alpha,beta", permissions), authz.ErrGroupAccess, "a grant must not expand a comma-separated channel group list")
		_, err = ListGroupMembers(5, "alpha", 1)
		assert.ErrorIs(t, err, authz.ErrGroupAccess, "global admin is not a group grant")
		var manager model.User
		require.NoError(t, db.First(&manager, 2).Error)
		assert.Equal(t, 1, manager.Role)
		assert.Equal(t, "default", manager.Group)
		assert.Equal(t, 12345, manager.Quota)
	})
	t.Run("membership_requires_account_holder_consent", func(t *testing.T) {
		assert.ErrorIs(t, ReviewGroupMembership(2, 4, "alpha", true), authz.ErrGroupAccess)
		assert.ErrorIs(t, RequestGroupMembership(3, "内部"), authz.ErrGroupAccess)
		require.NoError(t, RequestGroupMembership(3, "alpha"))
		assert.ErrorIs(t, ReviewGroupMembership(2, 3, "beta", true), authz.ErrGroupAccess)
		require.NoError(t, ReviewGroupMembership(2, 3, "alpha", true))
		members, err := ListGroupMembers(2, "alpha", 1)
		require.NoError(t, err)
		require.Len(t, members.Items, 1)
		assert.Equal(t, 3, members.Items[0].ID)
		assert.ErrorIs(t, RemoveGroupMember(2, 4, "alpha"), authz.ErrGroupAccess)
		assert.ErrorIs(t, RemoveGroupMember(2, 5, "alpha"), authz.ErrGroupAccess)
		require.NoError(t, RemoveGroupMember(2, 3, "alpha"))
		var member model.User
		require.NoError(t, db.First(&member, 3).Error)
		assert.Equal(t, "default", member.Group)
		assert.Equal(t, 67890, member.Quota)
		assert.Equal(t, 1, member.Role)
		require.NoError(t, RequestGroupMembership(3, "alpha"))
		require.NoError(t, db.Model(&member).Update("group", "beta").Error)
		assert.Error(t, ReviewGroupMembership(2, 3, "alpha", true), "stale membership transfer cannot override a newer group")
	})
	t.Run("shared_channel_changes_only_own_binding", func(t *testing.T) {
		assert.ErrorIs(t, SetGroupChannelEnabled(2, "alpha", 10, true), authz.ErrGroupAccess)
		require.NoError(t, OfferGroupChannel(1, "alpha", 10, true))
		require.NoError(t, SetGroupChannelEnabled(2, "alpha", 10, true))
		var current model.Channel
		require.NoError(t, db.First(&current, 10).Error)
		assert.Equal(t, "default,alpha", current.Group)
		assert.Equal(t, channel.Key, current.Key)
		assert.Equal(t, channel.UpstreamCosts, current.UpstreamCosts)
		assert.Equal(t, 1, current.Status)
		views, err := ListGroupChannels(2, "alpha")
		require.NoError(t, err)
		require.Len(t, views, 1)
		data, err := common.Marshal(views)
		require.NoError(t, err)
		assert.NotContains(t, string(data), "private-upstream-key")
		assert.NotContains(t, string(data), "upstream_costs")
		require.NoError(t, OfferGroupChannel(1, "alpha", 10, false))
		require.NoError(t, db.First(&current, 10).Error)
		assert.Equal(t, "default", current.Group)
		assert.ErrorIs(t, SetGroupChannelEnabled(2, "alpha", 10, true), authz.ErrGroupAccess)
		// An offer over pre-existing routing cannot erase that routing.
		require.NoError(t, db.Model(&current).Update("group", "default,alpha").Error)
		require.NoError(t, OfferGroupChannel(1, "alpha", 10, true))
		assert.ErrorIs(t, SetGroupChannelEnabled(2, "alpha", 10, false), authz.ErrGroupAccess)
		require.NoError(t, OfferGroupChannel(1, "alpha", 10, false))
		require.NoError(t, db.First(&current, 10).Error)
		assert.Equal(t, "default,alpha", current.Group)
		private := model.Channel{Id: 11, Name: "Internal", Key: "private", Group: "内部", Models: "test-model", Status: 1}
		require.NoError(t, db.Create(&private).Error)
		assert.ErrorIs(t, OfferGroupChannel(1, "alpha", 11, true), authz.ErrGroupAccess)
	})
	t.Run("dedicated_channel_cannot_bypass_review_or_pricing", func(t *testing.T) {
		input := NewGroupChannel{Name: "Own", BaseURL: "https://example.com", Key: "new-secret", Models: "test-model"}
		require.NoError(t, common.Unmarshal([]byte(`{"name":"Own","base_url":"https://example.com","key":"new-secret","models":"test-model","group":"内部","status":1,"type":8,"upstream_costs":"injected"}`), &input))
		_, err := CreateGroupChannel(4, "alpha", input)
		assert.ErrorIs(t, err, authz.ErrGroupAccess)
		id, err := CreateGroupChannel(2, "alpha", input)
		require.NoError(t, err)
		assert.Error(t, SetGroupChannelEnabled(2, "alpha", id, true))
		assert.ErrorIs(t, ApproveGroupChannel(2, "alpha", id), authz.ErrGroupAccess)
		assert.Error(t, ApproveGroupChannel(1, "alpha", id), "existing prices on another channel do not authorize a new one")
		assert.Error(t, OfferGroupChannel(1, "beta", id, true))
		var own model.Channel
		require.NoError(t, db.First(&own, id).Error)
		assert.Equal(t, 2, own.Status)
		assert.Equal(t, "alpha", own.Group)
		assert.Equal(t, 1, own.Type)
		assert.Empty(t, own.UpstreamCosts)
		newPrice := model.APIChannelPrice{Revision: 1, Model: "test-model", ChannelID: id, UpstreamModel: "test-model", SellingPrice: price.SellingPrice, Enabled: true}
		require.NoError(t, db.Create(&newPrice).Error)
		require.NoError(t, ApproveGroupChannel(1, "alpha", id))
		require.NoError(t, SetGroupChannelEnabled(2, "alpha", id, true))
		require.NoError(t, db.First(&own, id).Error)
		assert.Equal(t, 1, own.Status)
		assert.ErrorIs(t, SetGroupChannelEnabled(2, "beta", id, false), authz.ErrGroupAccess)
	})

	t.Run("autonomous_prices_models_and_usage_are_group_scoped", func(t *testing.T) {
		require.NoError(t, SetGroupGrants(1, 2, "alpha", append(permissions, authz.GroupWalletRead, authz.GroupWalletManage)))
		require.NoError(t, db.Create(&model.GroupWallet{GroupName: "alpha", Mode: model.GroupWalletIndependent, Enabled: true, Activated: true}).Error)
		owned := model.Channel{Id: 31, Name: "Owned", Group: "alpha", Models: "test-model", Status: 1, Type: 1, Key: "keep-secret"}
		require.NoError(t, db.Create(&owned).Error)
		require.NoError(t, owned.AddAbilities(db))
		require.NoError(t, db.Create(&model.GroupChannel{GroupName: "alpha", ChannelID: 31, Owned: true, Reviewed: true}).Error)
		input := GroupModelRateInput{Model: "test-model", Prices: map[string]*float64{"input": common.GetPointer(2.0), "cached_input": common.GetPointer(0.2), "cache_creation": common.GetPointer(3.0), "output": common.GetPointer(8.0)}}
		assert.ErrorIs(t, SaveGroupModelRate(2, "beta", 31, input), authz.ErrGroupAccess)
		assert.ErrorIs(t, SaveGroupModelRate(2, "alpha", 10, input), authz.ErrGroupAccess, "shared channel cannot be repriced")
		require.NoError(t, SaveGroupModelRate(2, "alpha", 31, input))
		assert.Error(t, SaveGroupModelRate(2, "alpha", 31, input), "stale version cannot overwrite a rate")
		for range 2 {
			require.NoError(t, db.AutoMigrate(&model.GroupModelPrice{}))
		}
		var retained model.GroupModelPrice
		require.NoError(t, db.Where("group_name = ? AND channel_id = ? AND model = ?", "alpha", 31, input.Model).First(&retained).Error)
		assert.EqualValues(t, 1, retained.Revision)
		duplicate := retained
		duplicate.ID = 0
		assert.Error(t, db.Create(&duplicate).Error, "rate uniqueness survives repeated startup")
		invalid := input
		invalid.ExpectedRevision = 1
		invalid.Prices = map[string]*float64{"input": common.GetPointer(-1.0)}
		assert.Error(t, SaveGroupModelRate(2, "alpha", 31, invalid))
		settings, err := GetGroupModelSettings(2, "alpha")
		require.NoError(t, err)
		assert.True(t, slices.ContainsFunc(settings, func(row GroupModelSetting) bool {
			return row.ChannelID == 31 && row.Revision == 1 && row.Prices.Output == 8
		}))
		data, err := common.Marshal(settings)
		require.NoError(t, err)
		assert.NotContains(t, string(data), "keep-secret")
		require.NoError(t, SetGroupChannelModels(2, "alpha", 31, "test-model", "test-model,new-model"))
		assert.Error(t, SetGroupChannelModels(2, "alpha", 31, "test-model", "stale"))
		assert.ErrorIs(t, SetGroupChannelModels(2, "beta", 31, "test-model,new-model", "stolen"), authz.ErrGroupAccess)
		visible, err := model.APIChannelModelsForGroups(db, []string{"alpha"})
		require.NoError(t, err)
		assert.True(t, visible["test-model"])
		assert.False(t, visible["new-model"], "new model needs a price")
		input.Model = "new-model"
		require.NoError(t, SaveGroupModelRate(2, "alpha", 31, input))
		visible, err = model.APIChannelModelsForGroups(db, []string{"alpha"})
		require.NoError(t, err)
		assert.True(t, visible["new-model"])
		public, err := model.APIChannelModelsForGroups(db, []string{"default"})
		require.NoError(t, err)
		assert.False(t, public["new-model"])
		assert.NoError(t, SetGroupChannelEnabled(2, "alpha", 31, true))
		require.NoError(t, SetGroupGrants(1, 2, "beta", []string{authz.GroupChannelsManage, authz.GroupWalletManage}))
		require.NoError(t, db.Create(&model.GroupWallet{GroupName: "beta", Mode: model.GroupWalletPrepaid}).Error)
		assert.ErrorIs(t, SaveGroupModelRate(2, "beta", 31, input), authz.ErrGroupAccess)
		var unchanged model.APIChannelPrice
		require.NoError(t, db.First(&unchanged, price.ID).Error)
		assert.Equal(t, price, unchanged)
		require.NoError(t, db.AutoMigrate(&model.Log{}))
		oldLog, oldType := model.LOG_DB, common.LogDatabaseType()
		model.LOG_DB = db
		common.SetLogDatabaseType(common.MainDatabaseType())
		defer func() { model.LOG_DB = oldLog; common.SetLogDatabaseType(oldType) }()
		require.NoError(t, db.Create(&[]model.Log{{UserId: 3, Username: "member", Group: "alpha", Type: model.LogTypeConsume, ModelName: "new-model", Quota: 0, Ip: "private-ip", Other: "private-data"}, {UserId: 4, Username: "other", Group: "beta", Type: model.LogTypeConsume, Quota: 123}}).Error)
		usage, err := GetGroupUsage(2, "alpha", 1)
		require.NoError(t, err)
		require.Len(t, usage.Items, 1)
		assert.Zero(t, usage.Items[0].Quota)
		data, err = common.Marshal(usage)
		require.NoError(t, err)
		assert.NotContains(t, string(data), "private")
		assert.NotContains(t, string(data), "other")
		require.NoError(t, SetGroupGrants(1, 2, "beta", nil))
	})
	t.Run("revocation_is_immediate_and_existing_prices_survive", func(t *testing.T) {
		require.NoError(t, SetGroupGrants(1, 2, "alpha", nil))
		_, err := ListGroupChannels(2, "alpha")
		assert.ErrorIs(t, err, authz.ErrGroupAccess)
		_, err = ListGroupMembers(2, "alpha", 1)
		assert.ErrorIs(t, err, authz.ErrGroupAccess)
		var unchanged model.APIChannelPrice
		require.NoError(t, db.First(&unchanged, price.ID).Error)
		assert.Equal(t, price, unchanged)
		assert.Equal(t, float64(0), ratio_setting.GetGroupRatio("内部"))
		var count int64
		require.NoError(t, db.Model(&model.User{}).Count(&count).Error)
		assert.Equal(t, int64(5), count)
	})
	t.Run("group_only_admin_preserves_internal_access_across_policy_reload", func(t *testing.T) {
		wasMaster := common.IsMasterNode
		common.IsMasterNode = true
		t.Cleanup(func() { common.IsMasterNode = wasMaster })
		require.NoError(t, db.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))
		require.NoError(t, authz.Init(db))
		require.NoError(t, db.Model(&model.User{}).Where("id = ?", 5).Update("group", "内部").Error)
		require.NoError(t, SetGroupGrants(1, 5, "alpha", []string{authz.GroupMembersRead, authz.GroupMembersManage, authz.GroupChannelsRead, authz.GroupChannelsManage}))
		require.NoError(t, authz.SetUserPermissions(5, authz.PermissionsMap{authz.ResourceAdministration: {"access": false}, authz.ResourceChannel: {authz.ActionSensitiveWrite: true}}))
		for range 2 {
			require.NoError(t, authz.Init(db))
			assert.False(t, authz.Can(5, common.RoleAdminUser, authz.GlobalAdminAccess))
			assert.False(t, authz.Can(5, common.RoleAdminUser, authz.ChannelSensitiveWrite), "an old resource grant cannot bypass global management isolation")
			assert.True(t, authz.Can(1, common.RoleRootUser, authz.GlobalAdminAccess))
			assert.True(t, authz.Can(6, common.RoleAdminUser, authz.ChannelWrite), "other administrators retain their original authority")
			_, err := ListGroupMembers(5, "alpha", 1)
			require.NoError(t, err)
			_, err = ListGroupMembers(5, "beta", 1)
			assert.ErrorIs(t, err, authz.ErrGroupAccess)
			assert.True(t, GroupRoleAllowed("内部", common.RoleAdminUser))
			_, err = NormalizeAccessTokenScopes(5, common.RoleAdminUser, []string{"user:write"})
			assert.ErrorIs(t, err, ErrAccessTokenScopeForbidden)
		}
		var admin model.User
		require.NoError(t, db.First(&admin, 5).Error)
		assert.Equal(t, common.RoleAdminUser, admin.Role)
		assert.Equal(t, "内部", admin.Group)
	})
}

func TestGroupWalletFunding(t *testing.T) {
	require.NoError(t, i18n.Init())
	previousDB, previousType, previousRedis := model.DB, common.MainDatabaseType(), common.RedisEnabled
	previousRatios := ratio_setting.GroupRatio2JSONString()
	var dialector gorm.Dialector
	switch os.Getenv("GROUP_WALLET_DIALECT") {
	case "", "sqlite":
		dialector = sqlite.Open(filepath.Join(t.TempDir(), "wallet.db"))
		common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	case "mysql":
		dialector = mysql.Open(os.Getenv("GROUP_WALLET_DSN"))
		common.SetMainDatabaseType(common.DatabaseTypeMySQL)
	case "postgres":
		dialector = postgres.Open(os.Getenv("GROUP_WALLET_DSN"))
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	default:
		t.Fatal("unsupported wallet dialect")
	}
	db, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.False(t, db.Migrator().HasTable(&model.User{}), "use a fresh disposable database")
	if os.Getenv("GROUP_WALLET_DSN") != "" {
		query := "SELECT DATABASE()"
		if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
			query = "SELECT current_database()"
		}
		var name string
		require.NoError(t, db.Raw(query).Scan(&name).Error)
		require.True(t, strings.HasPrefix(name, "group_wallet_"))
	}
	model.DB, common.RedisEnabled = db, false
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	if os.Getenv("GROUP_WALLET_DSN") != "" {
		sqlDB.SetMaxOpenConns(8)
	}
	var databaseVersion string
	versionQuery := "SELECT sqlite_version()"
	if os.Getenv("GROUP_WALLET_DIALECT") == "mysql" {
		versionQuery = "SELECT VERSION()"
	}
	if os.Getenv("GROUP_WALLET_DIALECT") == "postgres" {
		versionQuery = "SELECT version()"
	}
	require.NoError(t, db.Raw(versionQuery).Scan(&databaseVersion).Error)
	t.Log(databaseVersion)
	t.Cleanup(func() {
		model.DB, common.RedisEnabled = previousDB, previousRedis
		common.SetMainDatabaseType(previousType)
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"alpha":1,"beta":1}`))
	// Existing user/token/grant rows survive the additive wallet migration.
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.GroupGrant{}, &model.BillingRefund{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}))
	users := []model.User{{Id: 1, Username: "root", AffCode: "root", Role: 100, Status: 1, Group: "default"}, {Id: 2, Username: "manager", AffCode: "manager", Role: 1, Status: 1, Group: "default"}, {Id: 3, Username: "member", AffCode: "member", Role: 1, Status: 1, Group: "alpha"}, {Id: 4, Username: "outsider", AffCode: "outsider", Role: 1, Status: 1, Group: "beta", Quota: 999}}
	require.NoError(t, db.Create(&users).Error)
	token := model.Token{Id: 1, UserId: 3, Key: "wallet-test-token", RemainQuota: 10000, Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&token).Error)
	// Upgrade the released wallet schema without reclassifying or moving money.
	require.NoError(t, db.AutoMigrate(&legacyGroupWallet{}))
	require.NoError(t, db.Create(&legacyGroupWallet{ID: 90, GroupName: "legacy", Quota: 321}).Error)
	for range 2 {
		require.NoError(t, db.AutoMigrate(&model.GroupWallet{}, &model.GroupWalletMember{}, &model.GroupWalletCharge{}, &model.GroupWalletEntry{}, &model.BillingRefund{}))
	}
	legacy, err := model.FindGroupWallet("legacy")
	require.NoError(t, err)
	require.EqualValues(t, 321, legacy.Quota)
	require.Empty(t, legacy.Mode)
	require.False(t, legacy.Activated)
	require.NoError(t, SetGroupGrants(1, 2, "alpha", []string{authz.GroupWalletAllocate}))
	uninitialized, err := GetGroupWallet(2, "alpha", 1)
	require.NoError(t, err)
	require.False(t, uninitialized.Wallet.Activated)
	require.Len(t, uninitialized.Members, 1, "existing members are visible before wallet activation")
	require.Equal(t, 3, uninitialized.Members[0].UserID)
	require.ErrorIs(t, FundGroupWallet(2, "alpha", 1000, "not permitted", "test-op-1"), authz.ErrGroupAccess)
	require.NoError(t, FundGroupWallet(1, "alpha", 1000, "test allocation", "test-op-2"))
	require.NoError(t, FundGroupWallet(1, "alpha", 1000, "test allocation", "test-op-2"))
	require.Error(t, FundGroupWallet(1, "alpha", 2000, "changed request", "test-op-2"))
	require.NoError(t, SetGroupWalletEnabled(1, "alpha", true))
	require.NoError(t, AllocateGroupWallet(2, "alpha", 3, 600, "project", "test-op-3"))
	require.NoError(t, AllocateGroupWallet(2, "alpha", 3, 600, "project", "test-op-3"))
	require.ErrorIs(t, AllocateGroupWallet(2, "alpha", 4, 10, "cross group", "test-op-4"), authz.ErrGroupAccess)
	require.ErrorIs(t, AllocateGroupWallet(2, "beta", 4, 10, "cross group", "test-op-5"), authz.ErrGroupAccess)
	require.ErrorIs(t, AllocateGroupWallet(2, "alpha", 3, 401, "too much", "test-op-6"), model.ErrGroupWalletInsufficient)
	wallet, err := model.FindGroupWallet("alpha")
	require.NoError(t, err)
	var member model.GroupWalletMember
	balance := func() int64 {
		require.NoError(t, db.Where("wallet_id = ? AND user_id = ?", wallet.ID, 3).First(&member).Error)
		return member.Quota
	}
	require.EqualValues(t, 600, balance())
	createSession := func(id string, quota int) (*BillingSession, *gin.Context, *relaycommon.RelayInfo) {
		c, _ := gin.CreateTestContext(nil)
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		info := &relaycommon.RelayInfo{UserId: 3, TokenId: 1, TokenKey: token.Key, UsingGroup: "alpha", RequestId: id, ForcePreConsume: true}
		session, apiErr := NewBillingSession(c, info, quota)
		require.Nil(t, apiErr)
		info.Billing = session
		return session, c, info
	}
	session, _, info := createSession("group-success", 100)
	require.Equal(t, BillingSourceGroupWallet, info.BillingSource)
	require.EqualValues(t, 500, balance())
	info.UsingGroup = "beta"
	require.NotNil(t, PreConsumeBilling(cForGroupWallet(), 100, info), "retry cannot spend alpha funds in beta")
	info.UsingGroup = "alpha"
	require.NoError(t, session.Settle(180))
	require.NoError(t, session.Settle(180))
	require.EqualValues(t, 420, balance())
	var user model.User
	require.NoError(t, db.First(&user, 3).Error)
	require.Zero(t, user.Quota, "personal wallet is never debited")
	session, c, _ := createSession("group-refund", 50)
	session.Refund(c)
	require.Eventually(t, func() bool {
		var charge model.GroupWalletCharge
		return db.Where("request_id = ?", "group-refund").First(&charge).Error == nil && charge.Refunded
	}, 3*time.Second, 10*time.Millisecond)
	require.EqualValues(t, 420, balance())
	require.NoError(t, model.RefundGroupWallet(wallet.ID, 3, "group-refund"))
	require.EqualValues(t, 420, balance())
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("quota", 9999).Error)
	c, _ = gin.CreateTestContext(nil)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	_, apiErr := NewBillingSession(c, &relaycommon.RelayInfo{UserId: 3, TokenId: 1, TokenKey: token.Key, UsingGroup: "alpha", RequestId: "too-expensive"}, 421)
	require.NotNil(t, apiErr)
	require.Contains(t, apiErr.Error(), "Group wallet allocation")
	require.EqualValues(t, 420, balance())
	legacyInfo := &relaycommon.RelayInfo{UserId: 3, UsingGroup: "beta", RequestId: "legacy-retry", IsPlayground: true}
	legacySession, legacyErr := NewBillingSession(cForGroupWallet(), legacyInfo, 0)
	require.Nil(t, legacyErr)
	legacyInfo.Billing = legacySession
	legacyInfo.UsingGroup = "alpha"
	require.NotNil(t, PreConsumeBilling(cForGroupWallet(), 10, legacyInfo), "personal funding cannot follow a retry into a group wallet")
	// Existing tasks retain the wallet identity even after pause and membership change.
	session, _, info = createSession("group-task", 100)
	require.NoError(t, session.Settle(100))
	task := &model.Task{UserId: 3, Quota: 100, PrivateData: model.TaskPrivateData{BillingSource: info.BillingSource, GroupWalletID: info.GroupWalletID, GroupWalletRequestID: info.GroupWalletRequestID}}
	require.NoError(t, SetGroupWalletEnabled(1, "alpha", false))
	_, pausedErr := NewBillingSession(cForGroupWallet(), &relaycommon.RelayInfo{UserId: 3, TokenId: 1, TokenKey: token.Key, UsingGroup: "alpha", RequestId: "paused-group"}, 1)
	require.NotNil(t, pausedErr, "pause never falls back to the funded personal wallet")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("group", "beta").Error)
	require.NoError(t, taskAdjustFunding(task, -40))
	require.NoError(t, taskAdjustFunding(task, -40))
	require.EqualValues(t, 360, balance())
	require.NoError(t, AllocateGroupWallet(2, "alpha", 3, -360, "reclaim former member", "test-op-7"))
	require.EqualValues(t, 0, balance())
	require.ErrorIs(t, model.ReserveGroupWallet(wallet.ID, 3, "after-removal", 1), model.ErrGroupWalletUnavailable)
	require.NoError(t, SetGroupWalletEnabled(1, "alpha", true))
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("group", "alpha").Error)
	require.NoError(t, AllocateGroupWallet(2, "alpha", 3, 50, "concurrency", "test-op-8"))
	// Reservation locks prevent concurrent requests from spending one allocation twice.
	var wg sync.WaitGroup
	successes := make(chan int, 2)
	for _, id := range []string{"parallel-a", "parallel-b"} {
		wg.Go(func() {
			if model.ReserveGroupWallet(wallet.ID, 3, id, 40) == nil {
				successes <- 1
			}
		})
	}
	wg.Wait()
	close(successes)
	require.Len(t, successes, 1)
	require.EqualValues(t, 10, balance())
	view, err := GetGroupWallet(2, "alpha", 1)
	require.NoError(t, err)
	require.NotEmpty(t, view.Entries)
	_, err = GetGroupWallet(4, "alpha", 1)
	require.ErrorIs(t, err, authz.ErrGroupAccess)
	require.NoError(t, db.First(&user, 3).Error)
	require.Equal(t, 9999, user.Quota)
	var fresh int64
	require.NoError(t, db.Model(&model.GroupWallet{}).Count(&fresh).Error)
	require.EqualValues(t, 2, fresh, "the legacy wallet is preserved; no other groups are created automatically")
	require.NoError(t, db.Delete(&model.User{}, 3).Error)
	deletedView, err := GetGroupWallet(2, "alpha", 1)
	require.NoError(t, err)
	require.Len(t, deletedView.Members, 1)
	require.False(t, deletedView.Members[0].Active)
	require.NoError(t, AllocateGroupWallet(2, "alpha", 3, -10, "recover deleted member allocation", "reclaim-deleted"))
	require.EqualValues(t, 0, balance())

	t.Run("independent_group_is_self_managed_and_isolated", func(t *testing.T) {
		previousUsable := setting.UserUsableGroups2JSONString()
		previousAuto := setting.AutoGroups2JsonString()
		t.Cleanup(func() {
			require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUsable))
			require.NoError(t, setting.UpdateAutoGroupsByJsonString(previousAuto))
		})
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","alpha":"Prepaid","beta":"Independent"}`))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["default","alpha","beta"]`))
		require.ErrorIs(t, SetGroupWalletMode(2, "beta", model.GroupWalletIndependent), authz.ErrGroupAccess)
		require.Error(t, SetGroupWalletMode(1, "beta", "unknown"))
		require.NoError(t, SetGroupWalletMode(1, "beta", model.GroupWalletIndependent))
		require.NoError(t, SetGroupGrants(1, 2, "beta", []string{authz.GroupWalletManage, authz.GroupWalletAllocate}))
		require.NoError(t, SetGroupWalletEnabled(2, "beta", true))
		require.NoError(t, FundGroupWallet(2, "beta", 900, "local project budget", "independent-fund"))
		require.NoError(t, FundGroupWallet(2, "beta", 900, "local project budget", "independent-fund"))
		require.NoError(t, FundGroupWallet(2, "beta", -100, "adjust project budget", "independent-reduce"))
		require.NoError(t, AllocateGroupWallet(2, "beta", 4, 600, "member budget", "independent-member"))
		require.ErrorIs(t, FundGroupWallet(4, "beta", 100, "member cannot issue credit", "independent-denied"), authz.ErrGroupAccess)
		require.NoError(t, SetGroupGrants(1, 2, "alpha", []string{authz.GroupWalletManage, authz.GroupWalletAllocate}))
		require.ErrorIs(t, FundGroupWallet(2, "alpha", 100, "prepaid is not mintable", "prepaid-denied"), authz.ErrGroupAccess)
		require.ErrorIs(t, SetGroupWalletEnabled(2, "alpha", false), authz.ErrGroupAccess)
		require.Error(t, SetGroupWalletMode(1, "beta", model.GroupWalletPrepaid), "issued credits cannot become prepaid funds")
		require.Equal(t, map[string]string{"default": "Default", "alpha": "Prepaid", "beta": "Independent"}, GetUserUsableGroups("beta", 1), "independent membership must retain normal model access")
		require.NotContains(t, GetUserUsableGroups("default", 100), "beta", "global visibility does not grant independent membership")
		require.Equal(t, []string{"beta"}, GetUserAutoGroup("beta", 1))
		require.Equal(t, []string{"beta"}, FilterUserTokenAutoGroups("beta", []string{"alpha", "beta", "default"}, 1))
		require.False(t, IsUserSelectableGroup("beta", "default", 1), "custom Auto cannot mix personal and independent funding")
		require.True(t, IsUserSelectableGroup("beta", "beta", 1))
		info := &relaycommon.RelayInfo{UserId: 4, UsingGroup: "beta", RequestId: "independent-spend", IsPlayground: true}
		independentSession, apiErr := NewBillingSession(cForGroupWallet(), info, 50)
		require.Nil(t, apiErr)
		require.Equal(t, BillingSourceGroupWallet, info.BillingSource)
		require.NoError(t, independentSession.Settle(75))
		// A normal-group request uses personal funds, not the independent allocation.
		personalInfo := &relaycommon.RelayInfo{UserId: 4, UsingGroup: "default", RequestId: "independent-member-personal", IsPlayground: true, ForcePreConsume: true}
		personalSession, personalErr := NewBillingSession(cForGroupWallet(), personalInfo, 100)
		require.Nil(t, personalErr)
		require.Equal(t, BillingSourceWallet, personalInfo.BillingSource)
		require.NoError(t, personalSession.Settle(125))
		var personalUser model.User
		require.NoError(t, db.First(&personalUser, 4).Error)
		require.Equal(t, 874, personalUser.Quota)
		independentView, err := GetGroupWallet(2, "beta", 1)
		require.NoError(t, err)
		require.EqualValues(t, 525, independentView.Allocated)
		require.EqualValues(t, 75, independentView.Used)
		_, personalErr = NewBillingSession(cForGroupWallet(), &relaycommon.RelayInfo{UserId: 4, UsingGroup: "default", RequestId: "independent-member-personal-insufficient", IsPlayground: true, ForcePreConsume: true}, 875)
		require.NotNil(t, personalErr, "personal insufficiency must not spend the independent allocation")
		plan := model.SubscriptionPlan{Title: "Normal group plan", Enabled: true, QuotaResetPeriod: model.SubscriptionResetNever}
		require.NoError(t, db.Create(&plan).Error)
		model.InvalidateSubscriptionPlanCache(plan.Id)
		t.Cleanup(func() { model.InvalidateSubscriptionPlanCache(plan.Id) })
		sub := model.UserSubscription{UserId: 4, PlanId: plan.Id, AmountTotal: 1000, Status: "active", ModelScope: "normal-model", StartTime: time.Now().Unix() - 60, EndTime: time.Now().Unix() + 3600}
		require.NoError(t, db.Create(&sub).Error)
		subInfo := &relaycommon.RelayInfo{UserId: 4, UsingGroup: "default", OriginModelName: "normal-model", RequestId: "independent-member-normal-plan", TokenUserSubscriptionId: sub.Id, IsPlayground: true, ForcePreConsume: true}
		subSession, subErr := NewBillingSession(cForGroupWallet(), subInfo, 100)
		require.Nil(t, subErr)
		require.Equal(t, BillingSourceSubscription, subInfo.BillingSource)
		require.NoError(t, subSession.Settle(150))
		require.NoError(t, db.First(&sub, sub.Id).Error)
		require.EqualValues(t, 150, sub.AmountUsed)
		_, subErr = NewBillingSession(cForGroupWallet(), &relaycommon.RelayInfo{UserId: 4, UsingGroup: "default", OriginModelName: "normal-model", RequestId: "independent-member-normal-plan-insufficient", TokenUserSubscriptionId: sub.Id, IsPlayground: true, ForcePreConsume: true}, 851)
		require.NotNil(t, subErr, "an exhausted plan must not use personal or independent-group funds")
		independentView, err = GetGroupWallet(2, "beta", 1)
		require.NoError(t, err)
		require.EqualValues(t, 525, independentView.Allocated)
		require.EqualValues(t, 75, independentView.Used)
		_, apiErr = NewBillingSession(cForGroupWallet(), &relaycommon.RelayInfo{UserId: 4, UsingGroup: "beta", TokenUserSubscriptionId: 9}, 1)
		require.NotNil(t, apiErr)
		require.Contains(t, apiErr.Error(), "Subscription keys cannot be used")
		require.NoError(t, SetGroupWalletEnabled(2, "beta", false))
		_, apiErr = NewBillingSession(cForGroupWallet(), &relaycommon.RelayInfo{UserId: 4, UsingGroup: "beta"}, 1)
		require.NotNil(t, apiErr, "paused independent groups cannot use the funded personal wallet")
		var originalUser model.User
		require.NoError(t, db.First(&originalUser, 4).Error)
		require.Equal(t, 874, originalUser.Quota)
		view, err := GetGroupWallet(2, "beta", 1)
		require.NoError(t, err)
		require.EqualValues(t, 200, view.Wallet.Quota)
		require.EqualValues(t, 525, view.Allocated)
		require.EqualValues(t, 75, view.Used)
		require.False(t, view.CanChangeMode)
		// Astro's existing zero-priced model must remain usable at zero balance.
		require.NoError(t, SetGroupWalletEnabled(2, "beta", true))
		require.NoError(t, db.Create(&model.User{Id: 6, Username: "free-member", AffCode: "free-member", Role: 1, Status: 1, Group: "beta"}).Error)
		freeInfo := &relaycommon.RelayInfo{UserId: 6, UsingGroup: "beta", RequestId: "independent-free", IsPlayground: true}
		freeSession, freeErr := NewBillingSession(cForGroupWallet(), freeInfo, 0)
		require.Nil(t, freeErr)
		require.Equal(t, BillingSourceGroupWallet, freeInfo.BillingSource)
		require.NoError(t, freeSession.Settle(0))
		_, freeErr = NewBillingSession(cForGroupWallet(), &relaycommon.RelayInfo{UserId: 6, UsingGroup: "beta", RequestId: "independent-no-allocation", IsPlayground: true}, 1)
		require.NotNil(t, freeErr, "a paid model still needs an allocation")
	})
}

type legacyGroupWallet struct {
	ID        int
	GroupName string `gorm:"size:64;uniqueIndex"`
	Enabled   bool
	Activated bool
	Quota     int64 `gorm:"type:bigint"`
}

func (legacyGroupWallet) TableName() string { return "group_wallets" }

func cForGroupWallet() *gin.Context {
	c, _ := gin.CreateTestContext(nil)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	c.Request.Header.Set("Accept-Language", "en")
	return c
}
