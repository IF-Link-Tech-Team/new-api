package relay

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const taskIdempotencyContextKey = "task_submission_idempotency"

type taskIdempotencyState struct {
	row    *model.TaskIdempotency
	owner  string
	stop   func()
	parent context.Context
	once   bool
}

func taskIdempotencyIdentity(c *gin.Context, info *relaycommon.RelayInfo) (string, string, error) {
	keys := c.Request.Header.Values("Idempotency-Key")
	if len(keys) > 1 {
		return "", "", errors.New("Idempotency-Key must be provided once")
	}
	key := c.GetHeader("Idempotency-Key")
	if len(key) > 8192 || strings.ContainsAny(key, "\r\n") {
		return "", "", errors.New("Idempotency-Key is invalid")
	}
	if key == "" {
		key = info.PublicTaskID
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return "", "", err
	}
	data, err := storage.Bytes()
	if err != nil {
		return "", "", err
	}
	var body any
	if err = common.Unmarshal(data, &body); err != nil {
		return "", "", errors.New("idempotent task submission requires JSON")
	}
	canonical, err := common.Marshal(map[string]any{
		"path": c.Request.URL.Path, "query": c.Request.URL.RawQuery,
		"group": info.UsingGroup, "body": body,
	})
	if err != nil {
		return "", "", err
	}
	return model.TaskIdempotencyScope(info.UserId, info.TokenId, c.Request.Method+" "+c.Request.URL.Path+"\x00"+key), model.TaskIdempotencyDigest(canonical), nil
}

// LookupTaskIdempotency runs before channel selection so a retry cannot move
// an ambiguous upstream order to another provider or credential.
func LookupTaskIdempotency(c *gin.Context, info *relaycommon.RelayInfo) (*model.TaskIdempotency, *dto.TaskError) {
	if c.GetHeader("Idempotency-Key") == "" {
		return nil, nil
	}
	scope := model.TaskIdempotencyScope(info.UserId, info.TokenId, c.Request.Method+" "+c.Request.URL.Path+"\x00"+c.GetHeader("Idempotency-Key"))
	row, err := model.GetTaskIdempotency(c.Request.Context(), scope)
	if err != nil {
		return nil, service.TaskErrorWrapperLocal(errors.New("failed to load task order"), "idempotency_unavailable", http.StatusServiceUnavailable)
	}
	if row == nil {
		return nil, nil
	}
	_, fingerprint, err := taskIdempotencyIdentity(c, info)
	if err != nil {
		return nil, service.TaskErrorWrapperLocal(err, "invalid_idempotency_key", http.StatusBadRequest)
	}
	if row.RequestHash != fingerprint {
		return nil, service.TaskErrorWrapperLocal(model.ErrTaskIdempotencyConflict, "idempotency_conflict", http.StatusConflict)
	}
	return row, nil
}

func prepareTaskIdempotency(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	value, ok := c.Get(jsplugin.ContextKeyPinnedPlugin)
	if !ok {
		return nil
	}
	pin, ok := value.(jsplugin.PinnedPlugin)
	if !ok || pin.Plugin == nil || !pin.Plugin.Meta.UsesSubmitIdempotency() {
		return nil
	}
	once := pin.Plugin.Meta.UsesSubmitOnce()
	if once && c.GetHeader("Idempotency-Key") == "" {
		return nil
	}
	scope, fingerprint, err := taskIdempotencyIdentity(c, info)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_idempotency_key", http.StatusBadRequest)
	}
	row, err := model.GetTaskIdempotency(c.Request.Context(), scope)
	if err != nil {
		return service.TaskErrorWrapperLocal(errors.New("failed to load task order"), "idempotency_unavailable", http.StatusServiceUnavailable)
	}
	if row != nil {
		if row.RequestHash != fingerprint || row.PluginKey != pin.Plugin.Meta.Key {
			return service.TaskErrorWrapperLocal(model.ErrTaskIdempotencyConflict, "idempotency_conflict", http.StatusConflict)
		}
		info.PublicTaskID = row.PublicTaskID
	} else {
		row = &model.TaskIdempotency{ScopeKey: scope, RequestHash: fingerprint, PublicTaskID: info.PublicTaskID, PluginKey: pin.Plugin.Meta.Key, UserID: info.UserId}
	}
	c.Set(taskIdempotencyContextKey, &taskIdempotencyState{row: row, once: once})
	return nil
}

func claimTaskIdempotency(c *gin.Context, info *relaycommon.RelayInfo) (*model.Task, *dto.TaskError) {
	state := taskIdempotencyStateFor(c)
	if state == nil {
		return nil, nil
	}
	candidate := *state.row
	if candidate.ChannelID != 0 && (candidate.ChannelID != info.ChannelId || candidate.BaseURL != info.ChannelBaseUrl ||
		candidate.ChannelKeyHash != model.TaskIdempotencyDigest([]byte(info.ApiKey)) || candidate.UpstreamModel != info.UpstreamModelName) {
		return nil, service.TaskErrorWrapperLocal(errors.New("the original task channel, model or credential is no longer available"), "idempotency_route_changed", http.StatusConflict)
	}
	candidate.ChannelID, candidate.BaseURL, candidate.UpstreamModel, candidate.Group = info.ChannelId, info.ChannelBaseUrl, info.UpstreamModelName, info.UsingGroup
	candidate.ChannelKeyHash = model.TaskIdempotencyDigest([]byte(info.ApiKey))
	owner := model.GenerateTaskID()
	row, err := model.ClaimTaskIdempotency(c.Request.Context(), candidate, owner)
	if err != nil {
		code, status := "idempotency_unavailable", http.StatusServiceUnavailable
		if errors.Is(err, model.ErrTaskIdempotencyConflict) || errors.Is(err, model.ErrTaskIdempotencyBusy) {
			code, status = "idempotency_conflict", http.StatusConflict
		}
		if errors.Is(err, model.ErrTaskIdempotencyBusy) {
			c.Header("Retry-After", "3")
			code = "idempotency_in_progress"
		}
		return nil, service.TaskErrorWrapperLocal(fmt.Errorf("task order unavailable: %w", err), code, status)
	}
	state.row = row
	if row.TaskRowID != 0 {
		task, err := model.TaskIdempotencyResult(c.Request.Context(), row)
		if err != nil {
			return nil, service.TaskErrorWrapperLocal(errors.New("the original task is no longer available"), "idempotency_result_unavailable", http.StatusGone)
		}
		return task, nil
	}
	state.owner = owner
	if state.once && row.RequestSignature != "" {
		return nil, service.TaskErrorWrapperLocal(errors.New("the original submission may have been accepted; this key cannot start another request"), "idempotency_submission_unknown", http.StatusConflict)
	}
	info.PublicTaskID = row.PublicTaskID
	if err := model.RecoverTaskOrderReservations(c.Request.Context(), row, owner, info.TokenId); err != nil {
		return nil, service.TaskErrorWrapperLocal(err, "idempotency_billing_pending", http.StatusConflict)
	}
	if err := model.StartTaskOrderBillingAttempt(c.Request.Context(), row, owner); err != nil {
		return nil, service.TaskErrorWrapperLocal(err, "idempotency_billing_pending", http.StatusConflict)
	}
	info.RequestId = owner
	info.TaskRequestPreflight = func(request *http.Request) error {
		if !state.once && request.Header.Get("Idempotency-Key") != row.PublicTaskID {
			return fmt.Errorf("%w: the task order key must not be overridden", model.ErrTaskIdempotencyConflict)
		}
		if request.GetBody == nil {
			return errors.New("idempotent task requests must have a replayable body")
		}
		body, err := request.GetBody()
		if err != nil {
			return err
		}
		defer body.Close()
		digest := sha256.New()
		if _, err = io.Copy(digest, body); err != nil {
			return err
		}
		encoded, err := common.Marshal(map[string]any{"url": request.URL.String(), "method": request.Method, "headers": request.Header, "body": fmt.Sprintf("%x", digest.Sum(nil))})
		if err != nil {
			return err
		}
		return model.VerifyTaskIdempotencyRequest(request.Context(), row.ScopeKey, owner, model.TaskIdempotencyDigest(encoded))
	}
	state.parent = c.Request.Context()
	ctx, cancel := context.WithCancel(state.parent)
	c.Request = c.Request.WithContext(ctx)
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(model.TaskIdempotencyLeaseTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := model.RenewTaskIdempotency(renewCtx, row.ScopeKey, owner)
				renewCancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	state.stop = func() { close(done); cancel(); <-stopped }
	return nil, nil
}

func taskIdempotencyStateFor(c *gin.Context) *taskIdempotencyState {
	value, _ := c.Get(taskIdempotencyContextKey)
	state, _ := value.(*taskIdempotencyState)
	return state
}

func HasTaskIdempotencyLease(c *gin.Context) bool {
	state := taskIdempotencyStateFor(c)
	return state != nil && state.owner != ""
}

func ReleaseTaskIdempotency(c *gin.Context) {
	state := taskIdempotencyStateFor(c)
	if state == nil || state.owner == "" {
		return
	}
	if state.stop != nil {
		state.stop()
		c.Request = c.Request.WithContext(state.parent)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := model.ReleaseTaskIdempotency(ctx, state.row.ScopeKey, state.owner); err != nil {
		logger.LogWarn(c, common.LogText("failed to release task submission lease"))
	}
}

func PersistTaskSubmission(c *gin.Context, task *model.Task, omitColumns ...string) error {
	if state := taskIdempotencyStateFor(c); state != nil && state.owner != "" {
		return model.InsertTaskIdempotent(c.Request.Context(), task, state.row.ScopeKey, state.owner, omitColumns...)
	}
	return task.InsertWithContext(c.Request.Context(), omitColumns...)
}

func RestoreTaskIdempotencyKey(c *gin.Context, channel *model.Channel, row *model.TaskIdempotency) bool {
	for index, key := range channel.GetKeys() {
		if status, exists := channel.ChannelInfo.MultiKeyStatusList[index]; exists && status != common.ChannelStatusEnabled {
			continue
		}
		if model.TaskIdempotencyDigest([]byte(key)) == row.ChannelKeyHash {
			common.SetContextKey(c, constant.ContextKeyChannelKey, key)
			common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, index)
			return true
		}
	}
	return false
}
