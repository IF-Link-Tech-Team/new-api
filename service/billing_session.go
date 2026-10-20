package service

import (
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// BillingSession — 统一计费会话
// ---------------------------------------------------------------------------

// BillingSession 封装单次请求的预扣费/结算/退款生命周期。
// 实现 relaycommon.BillingSettler 接口。
type BillingSession struct {
	relayInfo        *relaycommon.RelayInfo
	funding          FundingSource
	preConsumedQuota int  // 实际预扣额度（信任用户可能为 0）
	tokenConsumed    int  // 令牌额度实际扣减量
	extraReserved    int  // 发送前补充预扣的额度（订阅退款时需要单独回滚）
	trusted          bool // 是否命中信任额度旁路
	fundingSettled   bool // funding.Settle 已成功，资金来源已提交
	settled          bool // Settle 全部完成（资金 + 令牌）
	refunded         bool // Refund 已调用
	refundReceipt    *model.BillingRefund
	mu               sync.Mutex
}

// Settle 根据实际消耗额度进行结算。
// 资金来源和令牌额度分两步提交：若资金来源已提交但令牌调整失败，
// 会标记 fundingSettled 防止 Refund 对已提交的资金来源执行退款。
func (s *BillingSession) Settle(actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actualQuota < 0 {
		return errors.New("actual quota cannot be negative")
	}
	if s.refunded {
		return errors.New("billing session already refunded")
	}
	if s.settled {
		return nil
	}
	if s.refundReceipt != nil {
		if err := model.BeginBillingRefundSettlement(s.refundReceipt.ID, actualQuota); err != nil {
			return err
		}
	}
	delta := actualQuota - s.preConsumedQuota
	if delta == 0 {
		s.settled = true
		return s.finishRefundReceipt(nil)
	}
	// 1) 调整资金来源（仅在尚未提交时执行，防止重复调用）
	if !s.fundingSettled {
		if err := s.funding.Settle(delta); err != nil {
			if s.refundReceipt != nil && errors.Is(err, model.ErrSubscriptionQuotaInsufficient) {
				if restoreErr := model.TransitionBillingRefund(s.refundReceipt.ID, []string{model.BillingRefundSettling}, model.BillingRefundReserved); restoreErr != nil {
					return restoreErr
				}
			}
			return err
		}
		s.fundingSettled = true
	}
	// 2) 调整令牌额度
	var tokenErr error
	if !s.relayInfo.IsPlayground {
		tokenErr = model.AdjustTokenQuotaDirect(s.relayInfo.TokenId, s.relayInfo.TokenKey, -delta)
		if tokenErr != nil {
			// 资金来源已提交，令牌调整失败只能记录日志；标记 settled 防止 Refund 误退资金
			common.SysLog(fmt.Sprintf("error adjusting token quota after funding settled (userId=%d, tokenId=%d, delta=%d): %s",
				s.relayInfo.UserId, s.relayInfo.TokenId, delta, tokenErr.Error()))
		}
	}
	// 3) 更新 relayInfo 上的订阅 PostDelta（用于日志）
	if s.funding.Source() == BillingSourceSubscription {
		s.relayInfo.SubscriptionPostDelta += int64(delta)
	}
	s.settled = true
	if err := s.finishRefundReceipt(tokenErr); err != nil {
		return err
	}
	return tokenErr
}

func (s *BillingSession) finishRefundReceipt(settleErr error) error {
	if s.refundReceipt == nil {
		return nil
	}
	failure := ""
	if settleErr != nil {
		failure = settleErr.Error()
	}
	return model.FinishBillingRefundSettlement(s.refundReceipt.ID, failure)
}

// Refund 退还所有预扣费，幂等安全，异步执行。
func (s *BillingSession) Refund(c *gin.Context) {
	s.mu.Lock()
	if s.settled || s.refunded || !s.needsRefundLocked() {
		s.mu.Unlock()
		return
	}
	if s.refundReceipt == nil {
		s.mu.Unlock()
		common.SysError("billing refund missing durable receipt")
		return
	}
	id := s.refundReceipt.ID
	// Persist intent before the terminal in-memory flag or asynchronous work.
	// If the DB is unavailable, keep the reserved receipt and NeedsRefund true;
	// reconciliation will expose the stale receipt rather than silently lose it.
	if err := model.QueueBillingRefund(id); err != nil {
		s.mu.Unlock()
		logger.LogError(c, fmt.Sprintf("cannot persist refund intent: receipt=%d user=%d error=%v", id, s.relayInfo.UserId, err))
		return
	}
	s.refunded = true
	s.mu.Unlock()
	gopool.Go(func() { processBillingRefund(id) })
}

// NeedsRefund 返回是否存在需要退还的预扣状态。
func (s *BillingSession) NeedsRefund() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.needsRefundLocked()
}

func (s *BillingSession) needsRefundLocked() bool {
	if s.settled || s.refunded || s.fundingSettled {
		// fundingSettled 时资金来源已提交结算，不能再退预扣费
		return false
	}
	if s.tokenConsumed > 0 {
		return true
	}
	// 订阅可能在 tokenConsumed=0 时仍预扣了额度
	if sub, ok := s.funding.(*SubscriptionFunding); ok && sub.preConsumed > 0 {
		return true
	}
	return false
}

// GetPreConsumedQuota 返回实际预扣的额度。
func (s *BillingSession) GetPreConsumedQuota() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.preConsumedQuota
}

func (s *BillingSession) Reserve(targetQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.settled || s.refunded || s.trusted || targetQuota <= s.preConsumedQuota {
		return nil
	}

	delta := targetQuota - s.preConsumedQuota
	if delta <= 0 {
		return nil
	}
	if s.refundReceipt == nil {
		// 预扣截断为 0 的会话还没有持久凭据；本次 Reserve 会真实扣款，
		// 必须先建立审计锚点，否则后续 Refund 会静默丢失这笔退款。
		receipt := &model.BillingRefund{RequestID: s.relayInfo.RequestId, UserID: s.relayInfo.UserId, TokenID: s.relayInfo.TokenId}
		if sub, ok := s.funding.(*SubscriptionFunding); ok {
			receipt.SubscriptionID = sub.boundSubscriptionId
		}
		if err := model.CreateBillingRefundReceipt(receipt); err != nil {
			return err
		}
		s.refundReceipt = receipt
	} else {
		walletQuota, tokenQuota, extraQuota := s.refundReceipt.WalletQuota, s.refundReceipt.TokenQuota, s.refundReceipt.ExtraQuota
		if s.funding.Source() == BillingSourceWallet {
			walletQuota += int64(delta)
		} else {
			extraQuota += int64(delta)
		}
		if !s.relayInfo.IsPlayground {
			tokenQuota += int64(delta)
		}
		if err := model.PrepareBillingRefundResize(s.refundReceipt.ID, walletQuota, tokenQuota, extraQuota); err != nil {
			return err
		}
	}

	if err := s.reserveFunding(delta); err != nil {
		if errors.Is(err, model.ErrSubscriptionQuotaInsufficient) {
			if restoreErr := s.saveRefundReservation(); restoreErr != nil {
				return restoreErr
			}
		}
		return err
	}
	if err := s.reserveToken(delta); err != nil {
		rollbackErr := s.rollbackFundingReserve(delta)
		if rollbackErr == nil && errors.Is(err, ErrTokenQuotaInsufficient) {
			if restoreErr := s.saveRefundReservation(); restoreErr != nil {
				return restoreErr
			}
		}
		return err
	}

	s.preConsumedQuota += delta
	s.tokenConsumed += delta
	s.extraReserved += delta
	s.syncRelayInfo()
	if err := s.saveRefundReservation(); err != nil {
		return err
	}
	return nil
}

func (s *BillingSession) saveRefundReservation() error {
	if s.refundReceipt == nil {
		return nil
	}
	receipt := s.refundReceipt
	if !s.relayInfo.IsPlayground {
		receipt.TokenQuota = int64(s.tokenConsumed)
	}
	switch funding := s.funding.(type) {
	case *WalletFunding:
		receipt.WalletQuota = int64(funding.consumed)
	case *SubscriptionFunding:
		receipt.SubscriptionID, receipt.SubscriptionQuota, receipt.ExtraQuota = funding.subscriptionId, funding.preConsumed, int64(s.extraReserved)
	}
	return model.SaveBillingRefundReservation(receipt)
}

// ---------------------------------------------------------------------------
// PreConsume — 统一预扣费入口（含信任额度旁路）
// ---------------------------------------------------------------------------

// preConsume 执行预扣费：信任检查 -> 令牌预扣 -> 资金来源预扣。
// 任一步骤失败时原子回滚已完成的步骤。
func (s *BillingSession) preConsume(c *gin.Context, quota int) *types.NewAPIError {
	effectiveQuota := quota

	// ---- 信任额度旁路 ----
	if s.shouldTrust(c) {
		s.trusted = true
		effectiveQuota = 0
		logger.LogInfo(c, fmt.Sprintf("用户 %d 额度充足, 信任且不需要预扣费 (funding=%s)", s.relayInfo.UserId, s.funding.Source()))
	} else if effectiveQuota > 0 {
		logger.LogInfo(c, fmt.Sprintf("用户 %d 需要预扣费 %s (funding=%s)", s.relayInfo.UserId, logger.FormatQuota(effectiveQuota), s.funding.Source()))
	}

	// Create a durable audit anchor before debiting anything. A crash in the
	// existing multi-step reservation leaves a prepared receipt for review,
	// never an automatic credit based only on an expired request.
	if effectiveQuota > 0 {
		s.refundReceipt = &model.BillingRefund{RequestID: s.relayInfo.RequestId, UserID: s.relayInfo.UserId, TokenID: s.relayInfo.TokenId}
		if !s.relayInfo.IsPlayground {
			s.refundReceipt.TokenQuota = int64(effectiveQuota)
		}
		if s.funding.Source() == BillingSourceWallet {
			s.refundReceipt.WalletQuota = int64(effectiveQuota)
		}
		if sub, ok := s.funding.(*SubscriptionFunding); ok {
			s.refundReceipt.SubscriptionID = sub.boundSubscriptionId
			s.refundReceipt.SubscriptionQuota = int64(effectiveQuota)
		}
		if err := model.CreateBillingRefundReceipt(s.refundReceipt); err != nil {
			return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
		}
	}
	// ---- 1) 预扣令牌额度 ----
	if effectiveQuota > 0 {
		if err := PreConsumeTokenQuota(s.relayInfo, effectiveQuota); err != nil {
			if errors.Is(err, ErrTokenQuotaInsufficient) && s.refundReceipt != nil {
				if closeErr := model.TransitionBillingRefund(s.refundReceipt.ID, []string{model.BillingRefundPrepared}, model.BillingRefundSettled); closeErr != nil {
					common.SysError(closeErr.Error())
				}
			}
			return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		s.tokenConsumed = effectiveQuota
	}

	// ---- 2) 预扣资金来源 ----
	if err := s.funding.PreConsume(effectiveQuota); err != nil {
		var rollbackErr error
		// 预扣费失败，回滚令牌额度
		if s.tokenConsumed > 0 && !s.relayInfo.IsPlayground {
			if rollbackErr = model.AdjustTokenQuotaDirect(s.relayInfo.TokenId, s.relayInfo.TokenKey, s.tokenConsumed); rollbackErr != nil {
				common.SysLog(fmt.Sprintf("error rolling back token quota (userId=%d, tokenId=%d, amount=%d, fundingErr=%s): %s",
					s.relayInfo.UserId, s.relayInfo.TokenId, s.tokenConsumed, err.Error(), rollbackErr.Error()))
			}
			s.tokenConsumed = 0
		}
		if errors.Is(err, model.ErrNoActiveSubscription) ||
			errors.Is(err, model.ErrSubscriptionQuotaInsufficient) ||
			errors.Is(err, model.ErrBoundSubscriptionUnavailable) ||
			errors.Is(err, model.ErrSubscriptionModelUnsupported) {
			if rollbackErr == nil && s.refundReceipt != nil {
				if closeErr := model.TransitionBillingRefund(s.refundReceipt.ID, []string{model.BillingRefundPrepared}, model.BillingRefundSettled); closeErr != nil {
					common.SysError(closeErr.Error())
				}
			}
			return types.NewErrorWithStatusCode(fmt.Errorf("订阅额度不足或未配置订阅: %w", err), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}

	s.preConsumedQuota = effectiveQuota

	// ---- 同步 RelayInfo 兼容字段 ----
	s.syncRelayInfo()
	if err := s.saveRefundReservation(); err != nil {
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}

	return nil
}

func (s *BillingSession) reserveFunding(delta int) error {
	switch funding := s.funding.(type) {
	case *WalletFunding:
		// 与结算补扣（SettleBilling 正差额 → WalletFunding.Settle）语义一致：
		// 全额无条件扣减，余额不足的部分记为欠费（余额可为负），不中断请求，
		// 保证日志记录的预扣额度与用户余额的实际变动始终对账一致。
		// Durable billing writes the debit directly to SQL.
		if err := model.AdjustUserQuotaDirect(funding.userId, -delta); err != nil {
			return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
		}
		funding.consumed += delta
		return nil
	case *SubscriptionFunding:
		if err := model.PostConsumeUserSubscriptionDelta(funding.subscriptionId, int64(delta)); err != nil {
			return types.NewErrorWithStatusCode(
				fmt.Errorf("订阅额度不足或未配置订阅: %w", err),
				types.ErrorCodeInsufficientUserQuota,
				http.StatusForbidden,
				types.ErrOptionWithSkipRetry(),
				types.ErrOptionWithNoRecordErrorLog(),
			)
		}
		return nil
	default:
		return types.NewError(fmt.Errorf("unsupported funding source: %s", s.funding.Source()), types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
}

func (s *BillingSession) rollbackFundingReserve(delta int) error {
	switch funding := s.funding.(type) {
	case *WalletFunding:
		if err := model.AdjustUserQuotaDirect(funding.userId, delta); err != nil {
			common.SysLog("error rolling back wallet funding reserve: " + err.Error())
			return err
		} else {
			funding.consumed -= delta
		}
	case *SubscriptionFunding:
		if err := model.PostConsumeUserSubscriptionDelta(funding.subscriptionId, -int64(delta)); err != nil {
			common.SysLog("error rolling back subscription funding reserve: " + err.Error())
			return err
		}
	}
	return nil
}

func (s *BillingSession) reserveToken(delta int) error {
	if delta <= 0 || s.relayInfo.IsPlayground {
		return nil
	}
	if err := PreConsumeTokenQuota(s.relayInfo, delta); err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return nil
}

// shouldTrust only bypasses wallet reservations; subscription reservations must be persisted.
func (s *BillingSession) shouldTrust(c *gin.Context) bool {
	// 异步任务（ForcePreConsume=true）必须预扣全额，不允许信任旁路
	if s.relayInfo.ForcePreConsume {
		return false
	}

	trustQuota := common.GetTrustQuota()
	if trustQuota <= 0 {
		return false
	}

	// 检查令牌是否充足
	tokenTrusted := s.relayInfo.TokenUnlimited
	if !tokenTrusted {
		tokenQuota := c.GetInt("token_quota")
		tokenTrusted = tokenQuota > trustQuota
	}
	if !tokenTrusted {
		return false
	}

	switch s.funding.Source() {
	case BillingSourceWallet:
		return s.relayInfo.UserQuota > trustQuota
	case BillingSourceSubscription:
		// Subscription reservations need a positive amount to persist their
		// request-id record and lock the selected entitlement.
		return false
	default:
		return false
	}
}

// syncRelayInfo 将 BillingSession 的状态同步到 RelayInfo 的兼容字段上。
func (s *BillingSession) syncRelayInfo() {
	info := s.relayInfo
	info.FinalPreConsumedQuota = s.preConsumedQuota
	info.BillingSource = s.funding.Source()

	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		info.SubscriptionId = sub.subscriptionId
		info.SubscriptionPreConsumed = sub.preConsumed + int64(s.extraReserved)
		info.SubscriptionPostDelta = 0
		info.SubscriptionAmountTotal = sub.AmountTotal
		info.SubscriptionAmountUsedAfterPreConsume = sub.AmountUsedAfter + int64(s.extraReserved)
		info.SubscriptionPlanId = sub.PlanId
		info.SubscriptionPlanTitle = sub.PlanTitle
	} else {
		info.SubscriptionId = 0
		info.SubscriptionPreConsumed = 0
	}
}

// ---------------------------------------------------------------------------
// NewBillingSession — 根据 Key 绑定选择唯一资金来源
// ---------------------------------------------------------------------------

// NewBillingSession uses the API key as the sole funding selector. Bound keys
// consume exactly one entitlement; every unbound key is wallet-only. V1 has no
// automatic search, cross-pool split, or wallet/subscription fallback.
func NewBillingSession(c *gin.Context, relayInfo *relaycommon.RelayInfo, preConsumedQuota int) (*BillingSession, *types.NewAPIError) {
	if relayInfo == nil {
		return nil, types.NewError(fmt.Errorf("relayInfo is nil"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	if preConsumedQuota < 0 {
		return nil, types.NewError(fmt.Errorf("invalid preConsumedQuota"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	var funding FundingSource
	if relayInfo.TokenUserSubscriptionId > 0 {
		if preConsumedQuota == 0 {
			preConsumedQuota = 1 // A subscription reservation must have a persisted, refundable amount.
		}
		funding = &SubscriptionFunding{
			requestId: relayInfo.RequestId, userId: relayInfo.UserId, modelName: relayInfo.OriginModelName,
			boundSubscriptionId: relayInfo.TokenUserSubscriptionId,
		}
	} else {
		userQuota, err := model.GetUserQuota(relayInfo.UserId, false)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if userQuota < preConsumedQuota {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("预扣费额度失败, 用户剩余额度: %s, 需要预扣费额度: %s", logger.FormatQuota(userQuota), logger.FormatQuota(preConsumedQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		relayInfo.UserQuota = userQuota
		funding = &WalletFunding{userId: relayInfo.UserId}
	}
	session := &BillingSession{relayInfo: relayInfo, funding: funding}
	if apiErr := session.preConsume(c, preConsumedQuota); apiErr != nil {
		return nil, apiErr
	}
	return session, nil
}
