package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relay"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type taskSubmissionTestBilling struct {
	events     *[]string
	settleErr  error
	reserveErr error
	onSettle   func()
	refunds    int
}

func (b *taskSubmissionTestBilling) Settle(int) error {
	*b.events = append(*b.events, "settle")
	if b.onSettle != nil {
		b.onSettle()
	}
	return b.settleErr
}

func (b *taskSubmissionTestBilling) Refund(*gin.Context) {
	*b.events = append(*b.events, "refund")
	b.refunds++
}

func (b *taskSubmissionTestBilling) NeedsRefund() bool        { return b.refunds == 0 }
func (b *taskSubmissionTestBilling) GetPreConsumedQuota() int { return 0 }
func (b *taskSubmissionTestBilling) Reserve(int) error {
	*b.events = append(*b.events, "reserve")
	return b.reserveErr
}

func (b *taskSubmissionTestBilling) ReserveBeforeSubmit(quota int) error { return b.Reserve(quota) }

func TestPresentTaskSubmissionUsesNativePresenterAfterPersistence(t *testing.T) {
	plugin, err := pluginruntime.CompilePlugin(`
export const meta = {apiVersion:1,key:"presenter-test",name:"Presenter",version:"1.0.0",author:{name:"Test"},models:["model"],fetchMode:"per_task",routes:[{method:"POST",path:"/vendor/jobs",type:"submit",decode:"decode",render:"created"}]};
export const native = {decode:function(ctx){return {kind:"submit",model:"model",requestBody:ctx.body.value};},created:function(ctx,task){return {data:{task_id:task.task_id},upstream:task.data};}};
export function buildSubmitRequest(){return {}} export function parseSubmitResponse(){return {taskId:"upstream"}} export function buildQueryRequest(){return {}} export function parseTaskResult(){return {status:"SUCCESS"}}
`, pluginruntime.Options{})
	require.NoError(t, err)
	priceData := types.PriceData{}
	priceData.AddOtherRatio("seconds", 5)
	task := &model.Task{TaskID: "task_public", SubmitTime: 123}
	task.SetData(map[string]any{"task_id": "upstream_private"})
	outcome := &taskSubmissionOutcome{
		Result:    &relay.TaskSubmitResult{},
		Task:      task,
		RelayInfo: &relaycommon.RelayInfo{PriceData: priceData},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/vendor/jobs", strings.NewReader(`{"model":"model"}`))
	c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Plugin: plugin, Route: plugin.Meta.Routes[0]})
	c.Set(pluginruntime.ContextKeyRouteRequest, pluginruntime.RouteRequestContext{Path: "/vendor/jobs", Method: http.MethodPost, Body: map[string]any{"kind": "json", "value": map[string]any{"model": "model"}}})

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{
		"data":{"task_id":"task_public"},
		"upstream":{"task_id":"upstream_private"}
	}`, recorder.Body.String())
	assert.JSONEq(t, `{"seconds":5}`, recorder.Header().Get("X-New-Api-Other-Ratios"))
}

func TestPresentTaskSubmissionFallbackUsesPersistedPublicID(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	outcome := &taskSubmissionOutcome{
		Result:    &relay.TaskSubmitResult{},
		Task:      &model.Task{TaskID: "task_persisted", SubmitTime: 456},
		RelayInfo: &relaycommon.RelayInfo{OriginModelName: "video-model"},
	}

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{
		"id":"task_persisted",
		"task_id":"task_persisted",
		"status":"queued",
		"model":"video-model",
		"created_at":456
	}`, recorder.Body.String())
}

func TestPresentTaskSubmissionUsesHostOpenAIVideoCreateReceipt(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(pluginruntime.ContextKeyPinnedEndpoint, pluginruntime.PinnedEndpoint{
		Protocol:  "openai_video",
		Operation: pluginruntime.HostProtocolOperation{Name: "create"},
	})
	task := &model.Task{
		TaskID:     "task_public",
		Status:     model.TaskStatusSubmitted,
		Progress:   "0%",
		CreatedAt:  456,
		Properties: model.Properties{OriginModelName: "video-model"},
	}
	outcome := &taskSubmissionOutcome{Result: &relay.TaskSubmitResult{}, Task: task, RelayInfo: &relaycommon.RelayInfo{}}

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{"id":"task_public","object":"video","model":"video-model","status":"queued","progress":0,"created_at":456}`, recorder.Body.String())
	assert.NotContains(t, recorder.Body.String(), "task_id")
}

func TestExecuteTaskSubmissionRefundsWhenInsertFails(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, false, &events)
	_ = database
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "task_insert_failed", taskErr.Code)
	assert.Equal(t, []string{"reserve", "insert", "refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionSettlementFailureStaysDurableAndWritesNothing(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events, settleErr: errors.New("settlement failed")}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "task_billing_settlement_failed", taskErr.Code)
	assert.Equal(t, []string{"reserve", "insert", "settle"}, events)
	assert.Zero(t, billing.refunds)
	var count int64
	require.NoError(t, database.Model(&model.Task{}).Where("task_id = ?", "task_public").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionPersistsPinnedPluginProvenance(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })

	c := taskSubmissionTestContext()
	c.Set(common.RequestIdKey, "request-public")
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{
		Generation: &pluginruntime.RoutingGeneration{Number: 42},
		Plugin: &pluginruntime.LoadedPlugin{Meta: pluginruntime.Meta{
			Key:        "document-parser",
			Name:       "Document Parser",
			Version:    "1.2.3",
			APIVersion: 1,
			Author: pluginruntime.AuthorMeta{
				Name: "Community Author",
				URL:  "https://plugins.example/author",
			},
		}},
	})
	billing := &taskSubmissionTestBilling{events: &events}
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream-private",
			Platform:       constant.TaskPlatform("document-parser"),
		}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	require.NotNil(t, outcome.Task.PrivateData.Execution)
	require.NotNil(t, outcome.Task.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, "request-public", outcome.Task.PrivateData.Execution.RequestID)
	assert.Equal(t, "/plugin/submit", outcome.Task.PrivateData.Execution.RequestPath)
	assert.Equal(t, "1.2.3", outcome.Task.PrivateData.Execution.TaskPlugin.Version)
	assert.Equal(t, uint64(42), outcome.Task.PrivateData.Execution.TaskPlugin.Generation)
	require.NotNil(t, outcome.Task.PrivateData.Execution.TaskPlugin.Author)
	assert.Equal(t, "Community Author", outcome.Task.PrivateData.Execution.TaskPlugin.Author.Name)
	assert.Equal(t, "https://plugins.example/author", outcome.Task.PrivateData.Execution.TaskPlugin.Author.URL)

	var stored model.Task
	require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
	require.NotNil(t, stored.PrivateData.Execution)
	require.NotNil(t, stored.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, "document-parser", stored.PrivateData.Execution.TaskPlugin.Key)
	require.NotNil(t, stored.PrivateData.Execution.TaskPlugin.Author)
	assert.Equal(t, "Community Author", stored.PrivateData.Execution.TaskPlugin.Author.Name)
	assert.Equal(t, "upstream-private", stored.PrivateData.UpstreamTaskID)
}

// A submit route declaring retainResult: false persists the task row for
// billing but never writes the upstream snapshot for an immediate terminal
// result, while the in-memory task still carries it for the presenter. An
// asynchronous result on the same route is retained because polling and
// retrieval depend on it.
func TestExecuteTaskSubmissionHonorsRouteRetainResult(t *testing.T) {
	retainFalse := false
	for _, tc := range []struct {
		name          string
		immediate     *relaycommon.TaskInfo
		wantDiscarded bool
	}{
		{"immediate success is discarded", &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, Progress: "100%"}, true},
		{"immediate failure is discarded", &relaycommon.TaskInfo{Status: model.TaskStatusFailure, Reason: "rejected"}, true},
		{"asynchronous result is retained", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := make([]string, 0, 3)
			database, _ := openTaskDialectDatabase(t, &model.Task{}, &model.User{}, &model.Channel{})
			previousDB := model.DB
			model.DB = database
			t.Cleanup(func() { model.DB = previousDB })
			previousLogConsumeEnabled := common.LogConsumeEnabled
			common.LogConsumeEnabled = false
			t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })

			c := taskSubmissionTestContext()
			c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{
				Plugin: &pluginruntime.LoadedPlugin{Meta: pluginruntime.Meta{Key: "sync-images"}},
				Route:  pluginruntime.Route{Method: http.MethodPost, Path: "/sync/images", Type: pluginruntime.RouteTypeSubmit, RetainResult: &retainFalse},
			})
			info := taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &events})
			upstream := []byte(`{"data":[{"url":"https://cdn.example/a.png"}]}`)

			outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
				return &relay.TaskSubmitResult{
					UpstreamTaskID: "task_public",
					Platform:       constant.TaskPlatform("sync-images"),
					TaskData:       upstream,
					Immediate:      tc.immediate,
				}, nil
			})
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			assert.JSONEq(t, string(upstream), string(outcome.Task.Data), "presenter keeps the in-memory snapshot")
			assert.Equal(t, tc.wantDiscarded, outcome.Task.PrivateData.ResultDiscarded)
			assert.Equal(t, !tc.wantDiscarded, outcome.Task.ResultRetrievable())

			var stored model.Task
			require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
			assert.Equal(t, tc.wantDiscarded, stored.PrivateData.ResultDiscarded)
			var nullCount int64
			require.NoError(t, database.Model(&model.Task{}).Where("task_id = ? AND data IS NULL", "task_public").Count(&nullCount).Error)
			if tc.wantDiscarded {
				assert.Equal(t, int64(1), nullCount, "snapshot column must stay NULL")
				artifacts, err := projectTaskArtifacts(&stored)
				require.NoError(t, err)
				assert.Empty(t, artifacts)
			} else {
				assert.Equal(t, int64(0), nullCount)
				assert.JSONEq(t, string(upstream), string(stored.Data))
			}
			listed := model.TaskGetAllUserTask(1, 0, 10, model.SyncTaskQueryParams{})
			require.Len(t, listed, 1)
			assert.Empty(t, listed[0].Data, "task lists never select the snapshot column")
		})
	}
}

func TestExecuteTaskSubmissionRefundsCancellationBeforeDurableBarrier(t *testing.T) {
	events := make([]string, 0, 2)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		cancel()
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionDisconnectBeforeUpstreamAcceptanceSkipsSubmitAndRefunds(t *testing.T) {
	events := make([]string, 0, 1)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)
	submitted := false

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		submitted = true
		return nil, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.False(t, submitted)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionCallerCancellationDuringSubmitRefundsBeforeDurableBarrier(t *testing.T) {
	events := make([]string, 0, 1)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)
	submitStarted := make(chan struct{})
	done := make(chan struct{})
	var outcome *taskSubmissionOutcome
	var taskErr *dto.TaskError

	go func() {
		defer close(done)
		outcome, taskErr = executeTaskSubmissionWith(c, info, func(c *gin.Context, _ *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
			close(submitStarted)
			<-c.Request.Context().Done()
			return nil, service.TaskErrorWrapperLocal(c.Request.Context().Err(), "do_request_failed", http.StatusInternalServerError)
		})
	}()
	select {
	case <-submitStarted:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "submission did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "submission did not stop after disconnect")
	}

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionDisconnectAfterDurableInsertDoesNotRefund(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	billing := &taskSubmissionTestBilling{
		events:   &events,
		onSettle: cancel,
	}
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	assert.Equal(t, "task_public", outcome.Task.TaskID)
	assert.Equal(t, []string{"reserve", "insert", "settle"}, events)
	assert.Zero(t, billing.refunds)
	var count int64
	require.NoError(t, database.Model(&model.Task{}).Where("task_id = ?", "task_public").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	assert.False(t, c.Writer.Written())
}

// setupTaskSubmissionDatabase opens the dialect selected by
// TEST_TASK_DB_DIALECT (SQLite in memory by default) and records every task
// INSERT in events so tests can assert the reserve → insert → settle order.
// Without migrate the task table does not exist and inserts fail.
func setupTaskSubmissionDatabase(t *testing.T, migrate bool, events *[]string) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	var models []any
	if migrate {
		models = append(models, &model.Task{})
	}
	database, _ := openTaskDialectDatabase(t, models...)
	require.NoError(t, database.Callback().Create().Before("gorm:create").Register("test:task-submit-order", func(*gorm.DB) {
		*events = append(*events, "insert")
	}))
	model.DB = database
	t.Cleanup(func() { model.DB = previousDB })
	return database
}

func taskSubmissionTestContext() *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/plugin/submit", strings.NewReader(`{}`))
	return c
}

func taskSubmissionRelayInfo(billing relaycommon.BillingSettler) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:          1,
		UsingGroup:      "default",
		OriginModelName: "plugin-model",
		Billing:         billing,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			PublicTaskID:  "task_public",
			LockedChannel: &model.Channel{Id: 1, Type: constant.ChannelTypeTaskPlugin, Name: "plugin"},
		},
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1, ChannelType: constant.ChannelTypeTaskPlugin},
	}
}

// Uses the real submit adaptor, expression evaluator, BillingSession, task row
// and consume log. Set TEST_TASK_DB_DIALECT plus TEST_MYSQL_DSN or
// TEST_POSTGRES_DSN to exercise the same contract on an external test database.
// Unique table prefixes keep the fixture isolated from all existing tables.
// openTaskDialectDatabase opens the engine selected by TEST_TASK_DB_DIALECT
// (default SQLite in memory; MySQL and PostgreSQL through TEST_MYSQL_DSN and
// TEST_POSTGRES_DSN) with a unique table prefix, migrates the given models and
// drops them on cleanup. It logs the engine version so database verification
// runs leave a record.
func openTaskDialectDatabase(t *testing.T, models ...any) (*gorm.DB, common.DatabaseType) {
	t.Helper()
	models = append(models, &model.GroupWallet{}, &model.GroupWalletMember{}, &model.GroupWalletCharge{}, &model.GroupWalletEntry{})
	dialect := common.DatabaseType(os.Getenv("TEST_TASK_DB_DIALECT"))
	var driver gorm.Dialector
	switch dialect {
	case "", common.DatabaseTypeSQLite:
		dialect = common.DatabaseTypeSQLite
		driver = sqlite.Open(":memory:")
	case common.DatabaseTypeMySQL:
		require.NotEmpty(t, os.Getenv("TEST_MYSQL_DSN"))
		driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
	case common.DatabaseTypePostgreSQL:
		require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
		driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
	default:
		t.Fatalf("unsupported test dialect %q", dialect)
	}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("tsubmit_%d_", time.Now().UnixNano())}})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(models...))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(models...)) })
	var version string
	if dialect == common.DatabaseTypeSQLite {
		require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
	} else {
		require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
	}
	t.Logf("database: %s %s", dialect, version)
	return db, dialect
}

func TestImmediateTaskSettlementDatabase(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Channel{}, &model.Task{}, &model.Log{}, &model.BillingRefund{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, true, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldConsume, oldExport
	})

	const expression = `u("units") == 7 ? tier("missing", u("missing") * 1.0) : u("units") == 8 ? tier("invalid", -1.0) : u("units") > 3 ? tier("bulk", u("units") * 0.01) : tier("small", u("units") * 0.01)`
	withTieredBillingConfig(t, map[string]string{"document-model": "tiered_expr"}, map[string]string{"document-model": expression})
	const source = `
export const meta={apiVersion:1,key:"generic-settlement",name:"Generic settlement",version:"1.0.0",author:{name:"Test"},models:["document-model"],fetchMode:"per_task",usageSchema:{units:{type:"number",unit:"count"}}};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/compile",body:ctx.requestBody};}
export function parseSubmitResponse(ctx,resp){return {taskId:"vendor-job",taskData:resp.body,immediate:{status:resp.body.status,reason:"provider rejected job"}};}
export function extractUsage(){return {units:4};}
export function extractUsageOnComplete(ctx,result,body){return body.usage;}
export function parseTaskResult(){throw new Error("completed submissions must not poll");}
export function buildQueryRequest(){throw new Error("completed submissions must not poll");}
`
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{})
	require.NoError(t, err)
	for index, tc := range []struct {
		name, status string
		actual       any
		count        float64
	}{
		{"partial", "SUCCESS", 2, 2}, {"zero", "SUCCESS", 0, 0}, {"larger", "SUCCESS", 6, 6},
		{"invalid usage", "SUCCESS", -1, 4}, {"expression failure", "SUCCESS", 7, 4}, {"negative result", "SUCCESS", 8, 4}, {"missing usage", "SUCCESS", nil, 4}, {"failed", "FAILURE", 9, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				body := map[string]any{"status": tc.status}
				if tc.actual != nil {
					body["usage"] = map[string]any{"units": tc.actual}
				}
				encoded, err := common.Marshal(body)
				if err != nil {
					panic(err)
				}
				_, _ = w.Write(encoded)
			}))
			defer server.Close()
			initial := int(20 * common.QuotaPerUnit)
			user := model.User{Username: fmt.Sprintf("task_user_%d", index), AffCode: fmt.Sprintf("task_aff_%d", index), Quota: initial}
			require.NoError(t, db.Create(&user).Error)
			ch := model.Channel{Name: "test provider", Type: constant.ChannelTypeTaskPlugin}
			require.NoError(t, db.Create(&ch).Error)
			c := taskSubmissionTestContext()
			c.Set("group", "default")
			c.Set("username", user.Username)
			c.Set("task_request", map[string]any{"model": "document-model"})
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "document-model")
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(c, constant.ContextKeyChannelId, ch.Id)
			common.SetContextKey(c, constant.ContextKeyChannelType, ch.Type)
			info := taskSubmissionRelayInfo(nil)
			info.UserId = user.Id
			info.OriginModelName = "document-model"
			info.UserGroup = "default"
			info.UsingGroup = "default"
			info.IsPlayground = true
			info.UserSetting.BillingPreference = "wallet_only"
			info.PublicTaskID = model.GenerateTaskID()
			info.LockedChannel = &ch
			outcome, taskErr := executeTaskSubmission(c, info)
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			want := common.QuotaRound(tc.count * 0.01 * common.QuotaPerUnit)
			assert.Equal(t, want, outcome.Result.Quota)
			assert.Equal(t, want, info.PriceData.Quota)
			var stored model.Task
			require.NoError(t, db.Where("task_id = ?", info.PublicTaskID).First(&stored).Error)
			assert.Equal(t, want, stored.Quota)
			assert.Equal(t, model.TaskStatus(tc.status), stored.Status)
			assert.Positive(t, stored.FinishTime)
			assert.Equal(t, float64(4), info.TieredBillingSnapshot.EstimatedQuotaBeforeGroup/(0.01*common.QuotaPerUnit))
			if tc.status == "SUCCESS" {
				assert.Equal(t, tc.count, stored.PrivateData.BillingContext.TieredSnapshot.UsageFacts["units"])
			}
			var updated model.User
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-want, updated.Quota)
			assert.Equal(t, want, updated.UsedQuota)
			var logs []model.Log
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, want, logs[0].Quota)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
			if tc.status == "SUCCESS" {
				assert.Equal(t, tc.count, other["usage_facts"].(map[string]any)["units"])
			}
			assert.False(t, c.Writer.Written(), "presentation must follow persistence and settlement")
			require.NoError(t, info.Billing.Settle(want))
			info.Billing.Refund(c)
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-want, updated.Quota, "terminal settlement is idempotent")
		})
	}
}

func TestAcceptedSubmitStreamNeverRetries(t *testing.T) {
	c := taskSubmissionTestContext()
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "task_accepted", Source: "system"}, decideTaskRetry(c, &dto.TaskError{StatusCode: 502, LocalError: true, NoRetry: true}, 3))
}

// Local task rejections carry a message but no cause; the response and the
// decision record must still be produced.
func TestRespondTaskSubmissionErrorWithoutCause(t *testing.T) {
	previousErrorLog := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = false
	t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })
	c := taskSubmissionTestContext()
	taskErr := &dto.TaskError{Code: "get_channel_failed", Message: "no channel", StatusCode: http.StatusServiceUnavailable, LocalError: true}
	require.NotPanics(t, func() { respondTaskSubmissionError(c, taskErr) })
	assert.Equal(t, http.StatusServiceUnavailable, c.Writer.Status())
	events := service.RequestPolicy(c).Events()
	require.Len(t, events, 1)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "request_failed", Source: "system"}, events[0].Decision)
	assert.Equal(t, http.StatusServiceUnavailable, events[0].Status)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "local_rejection", Source: "system"}, decideTaskRetry(c, &dto.TaskError{StatusCode: http.StatusForbidden, LocalError: true, Message: "billing"}, 2), "local errors stop once the status rules do not force a retry")
}

func TestExecuteTaskSubmissionRefundsWhenFinalReserveFails(t *testing.T) {
	events := []string{}
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events, reserveErr: errors.New("insufficient funds")}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{Platform: "plugin", Quota: 600, Immediate: &relaycommon.TaskInfo{Status: "SUCCESS"}}, nil
	})
	require.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusForbidden, taskErr.StatusCode)
	assert.Equal(t, []string{"reserve", "refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestTaskIdempotencyDatabase(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.Task{}, &model.User{}, &model.BillingRefund{})
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&model.TaskIdempotency{})) })
	previousDB, previousType := model.DB, common.MainDatabaseType()
	previousRedis, previousMemory := common.RedisEnabled, common.MemoryCacheEnabled
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	model.DB = db
	common.SetDatabaseTypes(dialect, common.LogDatabaseType())
	if dialect != common.DatabaseTypeSQLite {
		pool, err := db.DB()
		require.NoError(t, err)
		pool.SetMaxOpenConns(8)
	}
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetDatabaseTypes(previousType, common.LogDatabaseType())
		common.RedisEnabled, common.MemoryCacheEnabled = previousRedis, previousMemory
	})
	// Exercise the additive migration repeatedly against existing task data.
	legacy := &model.Task{TaskID: "legacy-task", UserId: 1, Quota: 23}
	require.NoError(t, db.Create(legacy).Error)
	for range 2 {
		require.NoError(t, db.AutoMigrate(&model.Task{}, &model.TaskIdempotency{}))
	}
	var retained model.Task
	require.NoError(t, db.First(&retained, legacy.ID).Error)
	assert.Equal(t, 23, retained.Quota)
	candidate := model.TaskIdempotency{
		ScopeKey: model.TaskIdempotencyScope(1, 2, "customer-order"), RequestHash: "request-one",
		PublicTaskID: "task_order", PluginKey: "jiliu-h3", ChannelID: 9, ChannelKeyHash: "credential-hash",
		BaseURL: "https://api.jiliu.cn", UpstreamModel: "MiniMax-H3", Group: "default", UserID: 1,
	}
	row, err := model.ClaimTaskIdempotency(t.Context(), candidate, "owner-one")
	require.NoError(t, err)
	assert.Equal(t, "owner-one", row.LeaseOwner)
	require.NoError(t, model.VerifyTaskIdempotencyRequest(t.Context(), row.ScopeKey, "owner-one", "wire-request-one"))
	require.NoError(t, model.VerifyTaskIdempotencyRequest(t.Context(), row.ScopeKey, "owner-one", "wire-request-one"))
	require.ErrorIs(t, model.VerifyTaskIdempotencyRequest(t.Context(), row.ScopeKey, "owner-one", "wire-request-changed"), model.ErrTaskIdempotencyConflict)
	require.NoError(t, model.RenewTaskIdempotency(t.Context(), row.ScopeKey, row.LeaseOwner))
	_, err = model.ClaimTaskIdempotency(t.Context(), candidate, "owner-two")
	require.ErrorIs(t, err, model.ErrTaskIdempotencyBusy)
	conflict := candidate
	conflict.RequestHash = "different-body"
	_, err = model.ClaimTaskIdempotency(t.Context(), conflict, "owner-two")
	require.ErrorIs(t, err, model.ErrTaskIdempotencyConflict)
	require.NoError(t, db.Model(&model.TaskIdempotency{}).Where("scope_key = ?", row.ScopeKey).Update("lease_until", 0).Error)
	resumed, err := model.ClaimTaskIdempotency(t.Context(), candidate, "owner-two")
	require.NoError(t, err)
	assert.Equal(t, row.PublicTaskID, resumed.PublicTaskID)
	require.NoError(t, model.ReleaseTaskIdempotency(t.Context(), row.ScopeKey, "owner-one"))
	task := &model.Task{TaskID: row.PublicTaskID, UserId: 1, Quota: 10}
	require.ErrorIs(t, model.InsertTaskIdempotent(t.Context(), task, row.ScopeKey, "owner-one"), model.ErrTaskIdempotencyLease)
	require.NoError(t, model.InsertTaskIdempotent(t.Context(), task, row.ScopeKey, "owner-two"))
	replay, err := model.ClaimTaskIdempotency(t.Context(), candidate, "owner-three")
	require.NoError(t, err)
	assert.Equal(t, task.ID, replay.TaskRowID)
	restored, err := model.TaskIdempotencyResult(t.Context(), replay)
	require.NoError(t, err)
	assert.Equal(t, task.TaskID, restored.TaskID)
	assert.NotEqual(t, candidate.ScopeKey, model.TaskIdempotencyScope(2, 2, "customer-order"))
	assert.NotEqual(t, candidate.ScopeKey, model.TaskIdempotencyScope(1, 3, "customer-order"))

	candidate.ScopeKey = model.TaskIdempotencyScope(1, 2, "concurrent-order")
	candidate.PublicTaskID = "task_concurrent"
	var owners int
	var mutex sync.Mutex
	var workers sync.WaitGroup
	for index := range 8 {
		workers.Go(func() {
			_, err := model.ClaimTaskIdempotency(t.Context(), candidate, fmt.Sprintf("worker-%d", index))
			mutex.Lock()
			defer mutex.Unlock()
			if err == nil {
				owners++
			} else {
				assert.ErrorIs(t, err, model.ErrTaskIdempotencyBusy)
			}
		})
	}
	workers.Wait()
	assert.Equal(t, 1, owners)

	// Simulate a process loss after a known reservation, before task insertion.
	user := model.User{Username: "crash-reservation", AffCode: "crash-reservation", Quota: 90}
	require.NoError(t, db.Create(&user).Error)
	crashed := candidate
	crashed.ScopeKey = model.TaskIdempotencyScope(user.Id, 0, "crashed-order")
	crashed.PublicTaskID, crashed.UserID, crashed.BillingRequestID = "task_crashed", user.Id, "old-attempt"
	crashed.LeaseOwner, crashed.LeaseUntil, crashed.TaskRowID = "", 0, 0
	require.NoError(t, db.Create(&crashed).Error)
	receipt := model.BillingRefund{RequestID: "old-attempt", UserID: user.Id, WalletQuota: 10, Status: model.BillingRefundReserved}
	require.NoError(t, db.Create(&receipt).Error)
	recovered, err := model.ClaimTaskIdempotency(t.Context(), crashed, "new-attempt")
	require.NoError(t, err)
	require.NoError(t, model.RecoverTaskOrderReservations(t.Context(), recovered, "new-attempt", 0))
	require.NoError(t, model.RecoverTaskOrderReservations(t.Context(), recovered, "new-attempt", 0))
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 100, user.Quota, "the old reservation is refunded exactly once")
	require.NoError(t, model.StartTaskOrderBillingAttempt(t.Context(), recovered, "new-attempt"))
	assert.Equal(t, "new-attempt", recovered.BillingRequestID, "subscription retries must not reuse a refunded reservation ID")
	partial := model.BillingRefund{RequestID: "new-attempt", UserID: user.Id, WalletQuota: 10, Status: model.BillingRefundPrepared}
	require.NoError(t, db.Create(&partial).Error)
	require.ErrorIs(t, model.RecoverTaskOrderReservations(t.Context(), recovered, "new-attempt", 0), model.ErrTaskIdempotencyBillingReview)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 100, user.Quota, "an ambiguous partial reservation cannot authorize a guessed refund")
}

func TestJiliuTaskIdempotencyBilling(t *testing.T) {
	t.Setenv("IFLINK_API_CHANNEL_PRICING", "enabled")
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Channel{}, &model.Ability{}, &model.Task{}, &model.TaskIdempotency{}, &model.Log{}, &model.BillingRefund{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, true, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldConsume, oldExport
	})
	withTieredBillingConfig(t, map[string]string{}, map[string]string{})
	expressions, err := common.Marshal(map[string]string{"jiliu-h3::MiniMax-H3": `(u("resolution") == "480P" ? tier("480P", (u("seconds") + u("input_video_seconds")) * 0.06) : u("resolution") == "768P" ? tier("768P", (u("seconds") + u("input_video_seconds")) * 0.14) : tier("1080P", (u("seconds") + u("input_video_seconds")) * 0.28)) + max(u("input_images") - 3, 0) * 0.7`})
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{billing_setting.PluginBillingExprOption: string(expressions)}))
	source, err := builtinplugins.Source("jiliu-h3")
	require.NoError(t, err)
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{})
	require.NoError(t, err)
	registry := pluginruntime.NewRegistry()
	require.NoError(t, registry.ReplaceOverrides([]*pluginruntime.LoadedPlugin{plugin}))
	generation := registry.Generation()
	var keys []string
	failNext := false
	var mutex sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			assert.Equal(t, "/v1/videos/provider-job", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"provider-job","status":"completed","seconds":"5","metadata":{"resolution":"768p","url":"http://localhost/private"}}`))
			return
		}
		mutex.Lock()
		defer mutex.Unlock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if failNext {
			failNext = false
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"accepted but response unavailable"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"provider-job","status":"queued"}`))
	}))
	defer server.Close()
	initial := common.QuotaRound(20 * common.QuotaPerUnit)
	user := model.User{Username: "idempotent-user", AffCode: "idempotent-aff", Quota: initial}
	require.NoError(t, db.Create(&user).Error)
	setting := `{"task_plugin_key":"jiliu-h3"}`
	baseURL := server.URL
	ch := model.Channel{Name: "idempotent-provider", Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Key: "test-key", BaseURL: &baseURL, Setting: &setting, Group: "default", Models: "MiniMax-H3"}
	require.NoError(t, db.Create(&ch).Error)
	require.NoError(t, db.Create(&model.Ability{ChannelId: ch.Id, Group: "default", Model: "MiniMax-H3", Enabled: true}).Error)
	officialFormat := false
	run := func(key string, seconds int) (*taskSubmissionOutcome, *dto.TaskError, *gin.Context) {
		body := map[string]any{"model": "MiniMax-H3", "task": "t2va", "prompt": "ocean", "target": map[string]any{"duration_seconds": seconds, "short_edge": 768, "aspect_ratio": "16:9"}}
		path := "/v1/videos"
		if officialFormat {
			path = "/v2/video_generation"
			body = map[string]any{"model": "MiniMax-H3", "content": []any{map[string]any{"type": "text", "text": "ocean"}}, "duration": seconds, "resolution": "768P", "ratio": "16:9"}
		}
		encoded, err := common.Marshal(body)
		require.NoError(t, err)
		c := taskSubmissionTestContext()
		c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(encoded)))
		c.Request.Header.Set("Content-Type", "application/json")
		if key != "" {
			c.Request.Header.Set("Idempotency-Key", key)
		}
		c.Set("group", "default")
		c.Set("username", user.Username)
		c.Set("task_request", body)
		c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
		c.Set("expected_task_plugin_key", plugin.Meta.Key)
		if officialFormat {
			binding, found := generation.LookupEndpoint("POST", path, "MiniMax-H3")
			require.True(t, found)
			c.Set(pluginruntime.ContextKeyPinnedEndpoint, pluginruntime.PinnedEndpoint{Generation: generation, Plugin: plugin, Protocol: binding.Protocol, Operation: binding.Operation, Model: "MiniMax-H3"})
			middleware.PrepareTaskPluginEndpoint()(c)
			require.False(t, c.IsAborted(), "official format must validate before billing")
		}
		policy, err := service.ResolveChannelSelectionPolicy(c, "MiniMax-H3")
		require.NoError(t, err, "native H3 billing must not require a token-price catalog row")
		require.Nil(t, policy)
		common.SetContextKey(c, constant.ContextKeyOriginalModel, "MiniMax-H3")
		common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
		common.SetContextKey(c, constant.ContextKeyChannelKey, "test-key")
		common.SetContextKey(c, constant.ContextKeyChannelId, ch.Id)
		common.SetContextKey(c, constant.ContextKeyChannelType, ch.Type)
		info := taskSubmissionRelayInfo(nil)
		info.UserId, info.OriginModelName, info.UserGroup, info.UsingGroup = user.Id, "MiniMax-H3", "default", "default"
		info.IsPlayground = true
		info.UserSetting.BillingPreference = "wallet_only"
		info.PublicTaskID, info.LockedChannel = model.GenerateTaskID(), &ch
		outcome, taskErr := executeTaskSubmission(c, info)
		assert.NoError(t, c.Request.Context().Err(), "lease cleanup must not cancel response rendering")
		common.CleanupBodyStorage(c)
		return outcome, taskErr, c
	}
	first, taskErr, _ := run("client-order", 5)
	require.Nil(t, taskErr)
	replay, taskErr, _ := run("client-order", 5)
	require.Nil(t, taskErr)
	assert.Equal(t, first.Task.ID, replay.Task.ID)
	_, taskErr, _ = run("client-order", 6)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusConflict, taskErr.StatusCode)
	assert.Len(t, keys, 1)
	mutex.Lock()
	failNext = true
	mutex.Unlock()
	_, taskErr, _ = run("retry-order", 5)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusServiceUnavailable, taskErr.StatusCode)
	resumed, taskErr, _ := run("retry-order", 5)
	require.Nil(t, taskErr)
	require.NotNil(t, resumed)
	require.Len(t, keys, 3)
	assert.Equal(t, keys[1], keys[2], "ambiguous retry must reuse the upstream order key")
	assert.NotEqual(t, keys[0], keys[1])
	mutex.Lock()
	failNext = true
	mutex.Unlock()
	_, taskErr, _ = run("header-change-order", 5)
	require.NotNil(t, taskErr)
	changedAuth := `{"Authorization":"Bearer different-account"}`
	require.NoError(t, db.Model(&ch).Update("header_override", changedAuth).Error)
	_, taskErr, _ = run("header-change-order", 5)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusConflict, taskErr.StatusCode)
	assert.Len(t, keys, 4, "changed final credentials must be rejected before another upstream request")
	require.NoError(t, db.Model(&ch).Update("header_override", nil).Error)
	pending, err := model.FindDueBillingRefunds(time.Now().Unix(), 100)
	require.NoError(t, err)
	for _, receipt := range pending {
		require.NoError(t, model.ProcessBillingRefund(receipt.ID))
	}
	var updated model.User
	require.NoError(t, db.First(&updated, user.Id).Error)
	assert.Equal(t, initial-common.QuotaRound(1.4*common.QuotaPerUnit), updated.Quota, "two distinct five-second 768P orders are charged once each at CNY 0.14 per second")
	var count int64
	require.NoError(t, db.Model(&model.Task{}).Count(&count).Error)
	assert.Equal(t, int64(2), count)
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&count).Error)
	assert.Equal(t, int64(2), count)

	// Order keys keep their existing per-route scope; retrying either wire
	// format replays its own task without billing or contacting Jiliu again.
	officialFormat = true
	converted, taskErr, convertedContext := run("client-order", 5)
	require.Nil(t, taskErr)
	replayed, taskErr, _ := run("client-order", 5)
	require.Nil(t, taskErr)
	assert.Equal(t, converted.Task.ID, replayed.Task.ID)
	assert.NotEqual(t, first.Task.ID, converted.Task.ID)
	assert.Len(t, keys, 5)
	_, taskErr, _ = run("client-order", 6)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusConflict, taskErr.StatusCode)
	presentTaskSubmission(convertedContext, converted)
	assert.Equal(t, http.StatusOK, convertedContext.Writer.Status())
	require.NoError(t, db.First(&updated, user.Id).Error)
	assert.Equal(t, initial-common.QuotaRound(2.1*common.QuotaPerUnit), updated.Quota)

	previousAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://gateway.example"
	t.Cleanup(func() { system_setting.ServerAddress = previousAddress })
	previousAdaptorFactory := service.GetTaskAdaptorFunc
	service.GetTaskAdaptorFunc = func(platform constant.TaskPlatform) service.TaskPollingAdaptor { return relay.GetTaskAdaptor(platform) }
	t.Cleanup(func() { service.GetTaskAdaptorFunc = previousAdaptorFactory })
	service.DispatchPlatformUpdate(context.Background(), converted.Task.Platform,
		map[int][]string{ch.Id: {"provider-job"}}, map[string]*model.Task{"provider-job": converted.Task})
	require.NoError(t, db.First(converted.Task, converted.Task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), converted.Task.Status)
	assert.Equal(t, common.QuotaRound(0.7*common.QuotaPerUnit), converted.Task.Quota)
	for _, taskID := range []string{first.Task.TaskID, converted.Task.TaskID} {
		for _, viewer := range []int{user.Id, user.Id + 1} {
			recorder := httptest.NewRecorder()
			engine := gin.New()
			engine.GET("/v2/query/video_generation/:task_id", func(c *gin.Context) {
				common.SetContextKey(c, constant.ContextKeyUserId, viewer)
				deps := defaultPluginProtocolBridgeDeps()
				deps.resolvePlugin = func(constant.TaskPlatform) (*pluginruntime.LoadedPlugin, *pluginruntime.RoutingGeneration, bool) {
					return plugin, generation, true
				}
				retrieveMiniMaxVideo(c, deps)
			})
			request := httptest.NewRequest(http.MethodGet, "/v2/query/video_generation/"+taskID, nil)
			request.Host = "untrusted.example"
			request.Header.Set("X-Forwarded-Host", "untrusted.example")
			engine.ServeHTTP(recorder, request)
			if viewer == user.Id {
				assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				assert.Contains(t, recorder.Body.String(), `"id":"`+taskID+`"`)
				if taskID == converted.Task.TaskID {
					assert.Contains(t, recorder.Body.String(), `"status":"succeeded"`)
					assert.Contains(t, recorder.Body.String(), `"url":"https://gateway.example/v1/videos/`+taskID+`/content"`)
					assert.NotContains(t, recorder.Body.String(), "untrusted.example")
				} else {
					assert.Contains(t, recorder.Body.String(), `"status":"queued"`)
				}
			} else {
				assert.Equal(t, http.StatusNotFound, recorder.Code)
				assert.Contains(t, recorder.Body.String(), `"type":"error"`)
			}
			assert.NotContains(t, recorder.Body.String(), "provider-job")
			assert.NotContains(t, recorder.Body.String(), "test-key")
		}
	}
}

func TestTokenHubMediaSubmissionBilling(t *testing.T) {
	t.Setenv("IFLINK_API_CHANNEL_PRICING", "enabled")
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Channel{}, &model.Ability{}, &model.Task{}, &model.TaskIdempotency{}, &model.Log{}, &model.BillingRefund{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, false, false, true
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = oldRedis, oldMemory, oldBatch, oldConsume
	})
	withTieredBillingConfig(t, map[string]string{}, map[string]string{})
	source, err := builtinplugins.Source("tokenhub-media")
	require.NoError(t, err)
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{})
	require.NoError(t, err)
	registry := pluginruntime.NewRegistry()
	require.NoError(t, registry.ReplaceOverrides([]*pluginruntime.LoadedPlugin{plugin}))
	generation := registry.Generation()
	expressions := map[string]string{}
	for _, name := range plugin.Meta.Models {
		if name == "hy-3d-3.1" {
			continue
		}
		expressions["tokenhub-media::"+name] = `tier("base", u("tokens") * 10 / 1000000)`
	}
	encoded, err := common.Marshal(expressions)
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{billing_setting.PluginBillingExprOption: string(encoded)}))
	for _, tc := range []struct {
		name, path, body, response, upstream string
		immediate                            bool
		cost                                 float64
	}{
		{"seedream-image-v5.0-pro", "/v1/images/generations", `{"model":"seedream-image-v5.0-pro","prompt":"a cat"}`, `{"data":[{"url":"https://example.com/cat.png"}],"tokenhub_usage":{"total_tokens":30000}}`, "/v1/wand/si-image/generation", true, 0.3},
		{"minimax-image-h3", "/v1/images/generations", `{"model":"minimax-image-h3","prompt":"a cat","n":2}`, `{"data":[{"url":"https://example.com/a.png"},{"url":"https://example.com/b.png"}],"tokenhub_usage":{"total_tokens":85000}}`, "/v1/wand/minimax-image/generation", true, 0.85},
		{"indextts-2", "/tokenhub-media/v1/audio/generations", `{"model":"indextts-2","text":"你好。","voice":"https://example.com/voice.wav"}`, `{"id":"index-job","status":"completed","output":{"data":"https://example.com/index.wav"},"usage":{"chars":9,"total_tokens":270}}`, "/v1/wand/tts/sync_tts", true, 0.0027},
		{"tenlai-tts-v1.0", "/tokenhub-media/v1/audio/generations", `{"model":"tenlai-tts-v1.0","text":"你好。","voice_setting":{"voice_id":"502001"}}`, `{"status":"completed","data":{"audio_url":"https://example.com/tenlai.mp3"}}`, "/v1/tenlai/ttsproxy/text_to_voice", true, 0.00225},
		{"tenlai-asr-v1.0", "/tokenhub-media/v1/audio/generations", `{"model":"tenlai-asr-v1.0","input_url":"https://example.com/voice.wav"}`, `{"id":17256336078,"status":"completed","output":{"text":"你好"},"audio_duration":1.5}`, "/v1/tenlai/asrproxy/create_rec_task", true, 0.00033},
		{"wand-asr-v1", "/tokenhub-media/v1/audio/generations", `{"model":"wand-asr-v1","input_url":"https://example.com/voice.wav"}`, `{"id":"asr-job","status":"completed","output":{"text":"你好"},"usage":{"total_token":50}}`, "/v1/wand/asrproxy/sync_transcribe", true, 0.0005},
		{"minimax-speech-2.8-turbo", "/tokenhub-media/v1/audio/generations", `{"model":"minimax-speech-2.8-turbo","text":"你好。","voice_setting":{"voice_id":"system-voice"}}`, `{"data":{"audio":"https://example.com/speech.mp3","status":2},"usage":{"total_tokens":180}}`, "/v1/wand/minimax-tts/sync_tts", true, 0.0018},
		{"minimax-speech-2.6-hd", "/tokenhub-media/v1/audio/generations", `{"model":"minimax-speech-2.6-hd","text":"你好。","voice_setting":{"voice_id":"system-voice"}}`, `{"data":{"audio":"https://example.com/speech.mp3","status":2},"usage":{"total_tokens":315}}`, "/v1/wand/minimax-tts/sync_tts", true, 0.00315},
		{"minimax-speech-2.6-turbo", "/tokenhub-media/v1/audio/generations", `{"model":"minimax-speech-2.6-turbo","text":"你好。","voice_setting":{"voice_id":"system-voice"}}`, `{"data":{"audio":"https://example.com/speech.mp3","status":2},"usage":{"total_tokens":180}}`, "/v1/wand/minimax-tts/sync_tts", true, 0.0018},
		{"minimax-speech-02-hd", "/tokenhub-media/v1/audio/generations", `{"model":"minimax-speech-02-hd","text":"你好。","voice_setting":{"voice_id":"system-voice"}}`, `{"data":{"audio":"https://example.com/speech.mp3","status":2},"usage":{"total_tokens":315}}`, "/v1/wand/minimax-tts/sync_tts", true, 0.00315},
		{"minimax-speech-02-turbo", "/tokenhub-media/v1/audio/generations", `{"model":"minimax-speech-02-turbo","text":"你好。","voice_setting":{"voice_id":"system-voice"}}`, `{"data":{"audio":"https://example.com/speech.mp3","status":2},"usage":{"total_tokens":180}}`, "/v1/wand/minimax-tts/sync_tts", true, 0.0018},
		{"minimax-video-h3", "/v2/video_generation", `{"model":"minimax-video-h3","content":[{"type":"text","text":"a cat walking"}],"duration":6,"resolution":"768P","ratio":"16:9"}`, `{"task":{"id":"vendor-job","status":"queued"}}`, "/v1/wand/minimax-video-v2/generation", false, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			malformed := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				assert.Equal(t, tc.upstream, r.URL.Path)
				assert.Equal(t, "Bearer synthetic-key", r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				if malformed {
					_, _ = w.Write([]byte(`{`))
					return
				}
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			initial := common.QuotaRound(100 * common.QuotaPerUnit)
			user := model.User{Username: tc.name, AffCode: tc.name, Quota: initial}
			require.NoError(t, db.Create(&user).Error)
			baseURL, setting := server.URL, `{"task_plugin_key":"tokenhub-media"}`
			ch := model.Channel{Name: tc.name, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Key: "synthetic-key", BaseURL: &baseURL, Setting: &setting, Group: "default", Models: tc.name}
			require.NoError(t, db.Create(&ch).Error)
			require.NoError(t, db.Create(&model.Ability{ChannelId: ch.Id, Group: "default", Model: tc.name, Enabled: true}).Error)
			run := func(key, body string) (*taskSubmissionOutcome, *dto.TaskError) {
				c := taskSubmissionTestContext()
				defer common.CleanupBodyStorage(c)
				c.Request = httptest.NewRequest("POST", tc.path, strings.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				if key != "" {
					c.Request.Header.Set("Idempotency-Key", key)
				}
				c.Set("group", "default")
				c.Set("username", user.Username)
				c.Set("expected_task_plugin_key", plugin.Meta.Key)
				c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Generation: generation, Plugin: plugin})
				if strings.HasPrefix(tc.path, "/tokenhub-media/") {
					for _, route := range plugin.Meta.Routes {
						if route.Path == tc.path {
							c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Generation: generation, Plugin: plugin, Route: route})
						}
					}
					middleware.PrepareTaskPluginRoute()(c)
				} else {
					binding, found := generation.LookupEndpoint("POST", tc.path, tc.name)
					require.True(t, found)
					c.Set(pluginruntime.ContextKeyPinnedEndpoint, pluginruntime.PinnedEndpoint{Generation: generation, Plugin: plugin, Protocol: binding.Protocol, Operation: binding.Operation, Model: tc.name})
					middleware.PrepareTaskPluginEndpoint()(c)
				}
				require.False(t, c.IsAborted())
				policy, err := service.ResolveChannelSelectionPolicy(c, tc.name)
				require.NoError(t, err)
				require.Nil(t, policy, "task route must not require token catalog pricing")
				common.SetContextKey(c, constant.ContextKeyOriginalModel, tc.name)
				common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
				common.SetContextKey(c, constant.ContextKeyChannelKey, "synthetic-key")
				common.SetContextKey(c, constant.ContextKeyChannelId, ch.Id)
				common.SetContextKey(c, constant.ContextKeyChannelType, ch.Type)
				info := taskSubmissionRelayInfo(nil)
				info.UserId, info.OriginModelName, info.UserGroup, info.UsingGroup = user.Id, tc.name, "default", "default"
				info.IsPlayground = true
				info.UserSetting.BillingPreference = "wallet_only"
				info.PublicTaskID, info.LockedChannel = model.GenerateTaskID(), &ch
				return executeTaskSubmission(c, info)
			}
			outcome, taskErr := run("media-order", tc.body)
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			assert.Equal(t, 1, calls)
			var updated model.User
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-common.QuotaRound(tc.cost*common.QuotaPerUnit), updated.Quota)
			if tc.immediate {
				assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), outcome.Task.Status)
			} else {
				assert.NotEqual(t, model.TaskStatus(model.TaskStatusFailure), outcome.Task.Status)
			}
			replay, taskErr := run("media-order", tc.body)
			require.Nil(t, taskErr)
			assert.Equal(t, outcome.Task.ID, replay.Task.ID)
			assert.Equal(t, 1, calls, "replay must not submit upstream")
			var changed map[string]any
			require.NoError(t, common.Unmarshal([]byte(tc.body), &changed))
			if _, exists := changed["text"]; exists {
				changed["text"] = "再见。"
			} else if _, exists := changed["input_url"]; exists {
				changed["input_url"] = "https://example.com/other.wav"
			} else if tc.name == "minimax-video-h3" {
				changed["content"] = []any{map[string]any{"type": "text", "text": "a dog walking"}}
			} else {
				changed["prompt"] = "a dog"
			}
			changedBody, err := common.Marshal(changed)
			require.NoError(t, err)
			_, taskErr = run("media-order", string(changedBody))
			require.NotNil(t, taskErr)
			assert.Equal(t, "idempotency_conflict", taskErr.Code)
			for range 2 {
				_, taskErr = run("", tc.body)
				require.Nil(t, taskErr, "unkeyed requests remain independent")
			}
			assert.Equal(t, 3, calls)
			malformed = true
			_, taskErr = run("ambiguous-order", tc.body)
			require.NotNil(t, taskErr)
			assert.Equal(t, 4, calls)
			malformed = false
			_, taskErr = run("ambiguous-order", tc.body)
			require.NotNil(t, taskErr)
			assert.Equal(t, "idempotency_submission_unknown", taskErr.Code)
			assert.Equal(t, 4, calls, "an accepted unreadable response must never be resubmitted")
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-3*common.QuotaRound(tc.cost*common.QuotaPerUnit), updated.Quota)
		})
	}
}

func TestTokenHubAsyncASRBilling(t *testing.T) {
	t.Setenv("IFLINK_API_CHANNEL_PRICING", "enabled")
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Channel{}, &model.Ability{}, &model.Task{}, &model.TaskIdempotency{}, &model.Log{}, &model.BillingRefund{})
	oldDB, oldLogDB, oldAdaptor := model.DB, model.LOG_DB, service.GetTaskAdaptorFunc
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, false, false, true
	t.Cleanup(func() {
		model.DB, model.LOG_DB, service.GetTaskAdaptorFunc = oldDB, oldLogDB, oldAdaptor
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = oldRedis, oldMemory, oldBatch, oldConsume
	})
	withTieredBillingConfig(t, map[string]string{}, map[string]string{})
	source, err := builtinplugins.Source("tokenhub-media")
	require.NoError(t, err)
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{})
	require.NoError(t, err)
	registry := pluginruntime.NewRegistry()
	require.NoError(t, registry.ReplaceOverrides([]*pluginruntime.LoadedPlugin{plugin}))
	generation := registry.Generation()
	service.GetTaskAdaptorFunc = func(constant.TaskPlatform) service.TaskPollingAdaptor { return taskplugin.New(plugin) }
	expressions := map[string]string{}
	for _, name := range []string{"tenlai-asr-v1.0", "hy-asr-3.0-preview", "wand-asr-v1"} {
		expressions["tokenhub-media::"+name] = `tier("base", u("tokens") * 10 / 1000000)`
	}
	encoded, err := common.Marshal(expressions)
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{billing_setting.PluginBillingExprOption: string(encoded)}))
	for _, name := range []string{"tenlai-asr-v1.0", "hy-asr-3.0-preview", "wand-asr-v1"} {
		for _, terminal := range []string{"completed", "failed"} {
			t.Run(name+"/"+terminal, func(t *testing.T) {
				submits, queries := 0, 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if strings.HasSuffix(r.URL.Path, "async_create") || strings.HasSuffix(r.URL.Path, "create_rec_task") {
						submits++
						if name == "tenlai-asr-v1.0" {
							_, _ = w.Write([]byte(`{"id":17256336078}`))
						} else {
							_, _ = w.Write([]byte(`{"id":"asr-job","status":"queued"}`))
						}
						return
					}
					queries++
					if name == "tenlai-asr-v1.0" {
						assert.Equal(t, "POST", r.Method)
						var body map[string]any
						require.NoError(t, common.DecodeJson(r.Body, &body))
						assert.EqualValues(t, 17256336078, body["task_id"])
					} else {
						assert.Equal(t, "/v1/wand/asrproxy/tasks/asr-job", r.URL.Path)
					}
					result := map[string]any{"status": terminal, "output": map[string]any{"text": "hello"}, "audio_duration": 1.5}
					if name == "hy-asr-3.0-preview" {
						result["output"] = map[string]any{"subtitle_url": "https://cdn.example/transcript.srt"}
					}
					if name != "tenlai-asr-v1.0" {
						result["usage"] = map[string]any{"total_token": 33}
					}
					if terminal == "failed" {
						result["error"] = map[string]any{"message": "invalid audio"}
					}
					b, err := common.Marshal(result)
					require.NoError(t, err)
					_, _ = w.Write(b)
				}))
				defer server.Close()
				initial := common.QuotaRound(100 * common.QuotaPerUnit)
				user := model.User{Username: name + terminal, AffCode: name + terminal, Quota: initial}
				require.NoError(t, db.Create(&user).Error)
				baseURL, setting := server.URL, `{"task_plugin_key":"tokenhub-media"}`
				ch := model.Channel{Name: name, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Key: "test", BaseURL: &baseURL, Setting: &setting, Group: "default", Models: name}
				require.NoError(t, db.Create(&ch).Error)
				body := map[string]any{"model": name, "input_url": "https://example.com/voice.wav"}
				if name != "tenlai-asr-v1.0" {
					body["async"] = true
				}
				encoded, err := common.Marshal(body)
				require.NoError(t, err)
				c := taskSubmissionTestContext()
				defer common.CleanupBodyStorage(c)
				path := "/tokenhub-media/v1/audio/generations"
				c.Request = httptest.NewRequest("POST", path, strings.NewReader(string(encoded)))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Set("group", "default")
				c.Set("username", user.Username)
				c.Set("expected_task_plugin_key", plugin.Meta.Key)
				c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Generation: generation, Plugin: plugin})
				for _, route := range plugin.Meta.Routes {
					if route.Path == path {
						c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Generation: generation, Plugin: plugin, Route: route})
					}
				}
				middleware.PrepareTaskPluginRoute()(c)
				require.False(t, c.IsAborted())
				common.SetContextKey(c, constant.ContextKeyOriginalModel, name)
				common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
				common.SetContextKey(c, constant.ContextKeyChannelKey, "test")
				common.SetContextKey(c, constant.ContextKeyChannelId, ch.Id)
				common.SetContextKey(c, constant.ContextKeyChannelType, ch.Type)
				info := taskSubmissionRelayInfo(nil)
				info.UserId, info.OriginModelName, info.UserGroup, info.UsingGroup = user.Id, name, "default", "default"
				info.IsPlayground = true
				info.UserSetting.BillingPreference = "wallet_only"
				info.PublicTaskID, info.LockedChannel = model.GenerateTaskID(), &ch
				outcome, taskErr := executeTaskSubmission(c, info)
				require.Nil(t, taskErr)
				task := outcome.Task
				assert.NotEqual(t, model.TaskStatusSuccess, task.Status)
				upstreamID := task.GetUpstreamTaskID()
				poll := func() {
					require.NoError(t, service.UpdateVideoTasks(t.Context(), task.Platform, map[int][]string{ch.Id: {upstreamID}}, map[string]*model.Task{upstreamID: task}))
				}
				poll()
				wantStatus, want := model.TaskStatusSuccess, common.QuotaRound(33*0.00001*common.QuotaPerUnit)
				if terminal == "failed" {
					wantStatus, want = model.TaskStatusFailure, 0
				}
				assert.Equal(t, model.TaskStatus(wantStatus), task.Status)
				assert.Equal(t, want, task.Quota)
				var updated model.User
				require.NoError(t, db.First(&updated, user.Id).Error)
				assert.Equal(t, initial-want, updated.Quota)
				poll()
				require.NoError(t, db.First(&updated, user.Id).Error)
				assert.Equal(t, initial-want, updated.Quota, "terminal replay must not settle twice")
				assert.Equal(t, 1, submits)
				assert.Equal(t, 2, queries)
			})
		}
	}
}
