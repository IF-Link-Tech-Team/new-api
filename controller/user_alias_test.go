package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPrivateUserAliases(t *testing.T) {
	previousDB, previousType := model.DB, common.MainDatabaseType()
	previousRatios := ratio_setting.GroupRatio2JSONString()
	var dialect gorm.Dialector
	versionSQL := "SELECT sqlite_version()"
	switch os.Getenv("USER_ALIAS_DIALECT") {
	case "", "sqlite":
		dialect = sqlite.Open(filepath.Join(t.TempDir(), "aliases.db"))
		common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	case "postgres":
		dialect = postgres.Open(os.Getenv("USER_ALIAS_DSN"))
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
		versionSQL = "SELECT version()"
	case "mysql":
		dialect = mysql.Open(os.Getenv("USER_ALIAS_DSN"))
		common.SetMainDatabaseType(common.DatabaseTypeMySQL)
		versionSQL = "SELECT VERSION()"
	default:
		t.Fatal("unsupported USER_ALIAS_DIALECT")
	}
	db, err := gorm.Open(dialect, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.False(t, db.Migrator().HasTable(&model.User{}), "use a fresh disposable database")
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	if os.Getenv("USER_ALIAS_DSN") != "" {
		query := "SELECT DATABASE()"
		if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
			query = "SELECT current_database()"
		}
		var name string
		require.NoError(t, db.Raw(query).Scan(&name).Error)
		require.True(t, strings.HasPrefix(name, "user_alias_"))
	}
	var version string
	require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
	t.Log(version)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"alpha":1,"beta":1}`))
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.GroupGrant{}, &model.GroupJoinRequest{}, &model.GroupWallet{}, &model.GroupWalletMember{}))
	users := []model.User{
		{Id: 1, Username: "owner", AffCode: "owner", Role: 100, Status: 1, Group: "default"},
		{Id: 2, Username: "manager", AffCode: "manager", Role: 1, Status: 1, Group: "default"},
		{Id: 3, Username: "A", DisplayName: "Original A", AffCode: "target", Role: 1, Status: 1, Group: "alpha", Quota: 1234},
		{Id: 4, Username: "other-viewer", AffCode: "other-viewer", Role: 100, Status: 1, Group: "default"},
		{Id: 5, Username: "outside", AffCode: "outside", Role: 1, Status: 1, Group: "beta"},
	}
	require.NoError(t, db.Create(&users).Error)
	for range 2 {
		require.NoError(t, db.AutoMigrate(&model.UserAlias{}))
	}
	require.NoError(t, service.SetGroupGrants(1, 2, "alpha", []string{authz.GroupMembersRead}))
	require.NoError(t, service.SetPrivateUserAlias(1, 3, "  AA  ", ""))
	require.NoError(t, service.SetPrivateUserAlias(4, 3, "BB", ""))
	require.NoError(t, service.SetPrivateUserAlias(2, 3, "Team A", "alpha"))
	require.ErrorIs(t, service.SetPrivateUserAlias(2, 5, "forbidden", "alpha"), authz.ErrGroupAccess)
	require.ErrorIs(t, service.SetPrivateUserAlias(2, 3, "forbidden", "beta"), authz.ErrGroupAccess)
	for id, want := range map[int]string{1: "AA", 2: "Team A", 4: "BB"} {
		rows, err := model.GetUserAliases(id)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, want, rows[0].Alias)
		payload, err := common.Marshal(rows)
		require.NoError(t, err)
		require.NotContains(t, string(payload), "owner_id")
	}
	require.NoError(t, service.SetPrivateUserAlias(1, 3, "Updated", ""))
	var count int64
	require.NoError(t, db.Model(&model.UserAlias{}).Where("owner_id = ? AND user_id = ?", 1, 3).Count(&count).Error)
	require.EqualValues(t, 1, count, "upsert preserves the per-viewer unique key")
	for _, invalid := range []string{strings.Repeat("界", 65), "line\nbreak", "hidden\x00text"} {
		require.Error(t, service.SetPrivateUserAlias(1, 3, invalid, ""))
	}
	require.NoError(t, service.SetPrivateUserAlias(1, 3, strings.Repeat("界", 64), ""))

	// The caller cannot supply a different owner through query or body fields.
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("id", 2); c.Next() })
	router.GET("/aliases", GetPrivateUserAliases)
	router.PUT("/aliases/:id", SetPrivateUserAlias)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/aliases/3", strings.NewReader(`{"alias":"Mine","group":"alpha","owner_id":1}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/aliases?owner_id=4", nil))
	require.Contains(t, recorder.Body.String(), "Mine")
	require.NotContains(t, recorder.Body.String(), "BB")
	var target model.User
	require.NoError(t, db.First(&target, 3).Error)
	require.Equal(t, "A", target.Username)
	require.Equal(t, "Original A", target.DisplayName)
	require.Equal(t, 1234, target.Quota)
	request = httptest.NewRequest(http.MethodPut, "/aliases/3", strings.NewReader(`{"group":"alpha"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	var invalidResponse struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &invalidResponse))
	require.False(t, invalidResponse.Success, "omitting alias must not erase it")
	rows, err := model.GetUserAliases(2)
	require.NoError(t, err)
	require.Equal(t, "Mine", rows[0].Alias)

	// A scoped manager may annotate visible applicants and former wallet members.
	require.NoError(t, db.Create(&model.User{Id: 6, Username: "applicant", AffCode: "applicant", Role: 1, Status: 1, Group: "default"}).Error)
	require.NoError(t, db.Create(&model.GroupJoinRequest{UserID: 6, GroupName: "alpha", FromGroup: "default"}).Error)
	require.ErrorIs(t, service.SetPrivateUserAlias(2, 6, "Applicant A", "alpha"), authz.ErrGroupAccess)
	require.NoError(t, service.SetGroupGrants(1, 2, "alpha", []string{authz.GroupMembersManage}))
	require.NoError(t, service.SetPrivateUserAlias(2, 6, "Applicant A", "alpha"))
	require.NoError(t, db.Where("user_id = ?", 6).Delete(&model.GroupJoinRequest{}).Error)
	require.ErrorIs(t, service.SetPrivateUserAlias(2, 6, "Not visible", "alpha"), authz.ErrGroupAccess)
	require.NoError(t, service.SetPrivateUserAlias(2, 6, "", "alpha"))
	require.NoError(t, service.SetGroupGrants(1, 2, "alpha", []string{authz.GroupWalletRead}))
	wallet := model.GroupWallet{GroupName: "alpha"}
	require.NoError(t, db.Create(&wallet).Error)
	require.NoError(t, db.Create(&model.GroupWalletMember{WalletID: wallet.ID, UserID: 5, Quota: 10}).Error)
	require.NoError(t, service.SetPrivateUserAlias(2, 5, "Former member", "alpha"))
	require.NoError(t, service.SetPrivateUserAlias(2, 5, "", "alpha"))

	require.NoError(t, service.SetGroupGrants(1, 2, "alpha", nil))
	require.ErrorIs(t, service.SetPrivateUserAlias(2, 3, "no access", "alpha"), authz.ErrGroupAccess)
	require.NoError(t, service.SetPrivateUserAlias(2, 3, "", "alpha"))
	rows, err = model.GetUserAliases(2)
	require.NoError(t, err)
	require.Empty(t, rows)
	rows, err = model.GetUserAliases(4)
	require.NoError(t, err)
	require.Equal(t, "BB", rows[0].Alias)
	for range 2 {
		require.NoError(t, db.AutoMigrate(&model.UserAlias{}))
	}
	rows, err = model.GetUserAliases(4)
	require.NoError(t, err)
	require.Equal(t, "BB", rows[0].Alias, fmt.Sprintf("aliases survive repeated migration on %s", version))
}
