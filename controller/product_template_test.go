package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupProductTemplateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMainDatabaseType, previousLogDatabaseType := common.MainDatabaseType(), common.LogDatabaseType()
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&model.ProductTemplate{}))

	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func createProductTemplateRow(t *testing.T, db *gorm.DB, productType string, locked bool) model.ProductTemplate {
	t.Helper()
	template := model.ProductTemplate{
		ProductType: productType, DisplayName: productType, Category: "package",
		Currency: "CNY", ModelScope: "[]", PriceRules: "[]", DisplayMetadata: "{}",
		Locked: locked,
	}
	require.NoError(t, db.Create(&template).Error)
	return template
}

func performUpdateProductTemplateRequest(t *testing.T, id int, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/subscription/admin/templates/%d", id), strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(id)}}
	AdminUpdateProductTemplate(c)
	return recorder
}

func TestAdminUpdateProductTemplateAllowsDisplayFields(t *testing.T) {
	db := setupProductTemplateTestDB(t)
	template := createProductTemplateRow(t, db, "test_pack_editable", false)

	recorder := performUpdateProductTemplateRequest(t, template.Id,
		`{"display_name":"新展示名","min_margin_percent":15}`)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)

	var updated model.ProductTemplate
	require.NoError(t, db.First(&updated, template.Id).Error)
	assert.Equal(t, "新展示名", updated.DisplayName)
	assert.Equal(t, 15.0, updated.MinMarginPercent)
}

func TestAdminUpdateProductTemplateRejectsKeyFieldChange(t *testing.T) {
	db := setupProductTemplateTestDB(t)
	template := createProductTemplateRow(t, db, "test_pack_key_field", false)

	recorder := performUpdateProductTemplateRequest(t, template.Id,
		`{"price_rules":"[]","duration_days":30,"display_name":"改名"}`)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "duration_days")

	var unchanged model.ProductTemplate
	require.NoError(t, db.First(&unchanged, template.Id).Error)
	assert.Equal(t, template.DisplayName, unchanged.DisplayName)
}

func TestAdminUpdateProductTemplateRejectsLockedTemplate(t *testing.T) {
	db := setupProductTemplateTestDB(t)
	template := createProductTemplateRow(t, db, "test_pack_locked", true)

	recorder := performUpdateProductTemplateRequest(t, template.Id, `{"display_name":"改名"}`)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "迁移")

	var unchanged model.ProductTemplate
	require.NoError(t, db.First(&unchanged, template.Id).Error)
	assert.Equal(t, template.DisplayName, unchanged.DisplayName)
}

func TestAdminUpdateProductTemplateAcceptsEchoedKeyFields(t *testing.T) {
	db := setupProductTemplateTestDB(t)
	template := createProductTemplateRow(t, db, "test_pack_echo", false)

	body := fmt.Sprintf(`{"product_type":%q,"duration_days":%d,"locked":false,"display_name":"回声更新"}`,
		template.ProductType, template.DurationDays)
	recorder := performUpdateProductTemplateRequest(t, template.Id, body)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)

	var updated model.ProductTemplate
	require.NoError(t, db.First(&updated, template.Id).Error)
	assert.Equal(t, "回声更新", updated.DisplayName)
}

func TestAdminUpdateProductTemplateRejectsOutOfRangeMargin(t *testing.T) {
	db := setupProductTemplateTestDB(t)
	template := createProductTemplateRow(t, db, "test_pack_margin", false)

	recorder := performUpdateProductTemplateRequest(t, template.Id, `{"min_margin_percent":150}`)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":false`)

	var unchanged model.ProductTemplate
	require.NoError(t, db.First(&unchanged, template.Id).Error)
	assert.Equal(t, 0.0, unchanged.MinMarginPercent)
}
