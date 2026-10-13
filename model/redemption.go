package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
)

type Redemption struct {
	Id           int            `json:"id"`
	UserId       int            `json:"user_id"`
	Key          string         `json:"key" gorm:"type:char(32);uniqueIndex"`
	Status       int            `json:"status" gorm:"default:1"`
	Name         string         `json:"name" gorm:"index"`
	Quota        int            `json:"quota" gorm:"default:100"`
	CreatedTime  int64          `json:"created_time" gorm:"bigint"`
	RedeemedTime int64          `json:"redeemed_time" gorm:"bigint"`
	Count        int            `json:"count" gorm:"-:all"` // only for api request
	UsedUserId   int            `json:"used_user_id"`
	DeletedAt    gorm.DeletedAt `gorm:"index"`
	ExpiredTime  int64          `json:"expired_time" gorm:"bigint"` // 过期时间，0 表示不过期
	// TargetPlanId, when > 0, makes this redemption create a UserSubscription
	// bound to the given plan on redeem, instead of adding Quota to the wallet.
	// 0 = legacy wallet-top-up behavior (default).
	TargetPlanId          int    `json:"target_plan_id" gorm:"default:0"`
	BatchCode             string `json:"batch_code" gorm:"type:varchar(64);index"`
	IsReserve             bool   `json:"is_reserve" gorm:"index"`
	ClaimedSubscriptionId int    `json:"claimed_subscription_id" gorm:"default:0;index"`
	ClaimedTokenId        int    `json:"claimed_token_id" gorm:"default:0;index"`
}

// ProductRedemptionClaim is the database-level one-account-per-activity guard.
// It is written in the same transaction as code consumption, entitlement and
// key creation. The two unique indexes also make retries and races safe.
type ProductRedemptionClaim struct {
	Id                 int    `json:"id"`
	UserId             int    `json:"user_id" gorm:"uniqueIndex:idx_redemption_claim_user_family,priority:1"`
	ProductFamily      string `json:"product_family" gorm:"type:varchar(64);uniqueIndex:idx_redemption_claim_user_family,priority:2"`
	RedemptionId       int    `json:"redemption_id" gorm:"uniqueIndex"`
	UserSubscriptionId int    `json:"user_subscription_id" gorm:"uniqueIndex"`
	TokenId            int    `json:"token_id" gorm:"uniqueIndex"`
	CreatedTime        int64  `json:"created_time" gorm:"bigint"`
}

type ProductRedemptionResult struct {
	RedemptionId       int    `json:"redemption_id"`
	PlanId             int    `json:"plan_id"`
	ProductType        string `json:"product_type"`
	ProductCode        string `json:"product_code"`
	ProductFamily      string `json:"product_family"`
	UserSubscriptionId int    `json:"user_subscription_id"`
	TokenId            int    `json:"token_id"`
	ExpiresAt          int64  `json:"expires_at"`
	AmountTotal        int64  `json:"amount_total"`
	Idempotent         bool   `json:"idempotent"`
}

type ProductRedemptionBatchRequest struct {
	Name         string `json:"name"`
	BatchCode    string `json:"batch_code"`
	TargetPlanId int    `json:"target_plan_id"`
	PrimaryCount int    `json:"primary_count"`
	ReserveCount int    `json:"reserve_count"`
	ExpiredTime  int64  `json:"expired_time"`
}

type ProductRedemptionBatchSummary struct {
	BatchCode          string `json:"batch_code"`
	Name               string `json:"name"`
	TargetPlanId       int    `json:"target_plan_id"`
	PrimaryCount       int    `json:"primary_count"`
	ReserveCount       int    `json:"reserve_count"`
	UnusedPrimaryCount int    `json:"unused_primary_count"`
	UnusedReserveCount int    `json:"unused_reserve_count"`
	UsedCount          int    `json:"used_count"`
	ExpiredTime        int64  `json:"expired_time"`
}

func GetAllRedemptions(startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	// 开始事务
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 获取总数
	err = tx.Model(&Redemption{}).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 获取分页数据
	err = tx.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func SearchRedemptions(keyword string, status string, startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&Redemption{})

	if keyword != "" {
		if id, err := strconv.Atoi(keyword); err == nil {
			query = query.Where("id = ? OR name LIKE ?", id, keyword+"%")
		} else {
			query = query.Where("name LIKE ?", keyword+"%")
		}
	}

	if status != "" {
		now := common.GetTimestamp()
		switch status {
		case "expired":
			query = query.Where(
				"status = ? AND expired_time != 0 AND expired_time < ?",
				common.RedemptionCodeStatusEnabled,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusEnabled):
			query = query.Where(
				"status = ? AND (expired_time = 0 OR expired_time >= ?)",
				common.RedemptionCodeStatusEnabled,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusDisabled):
			query = query.Where("status = ?", common.RedemptionCodeStatusDisabled)
		case strconv.Itoa(common.RedemptionCodeStatusUsed):
			query = query.Where("status = ?", common.RedemptionCodeStatusUsed)
		}
	}

	// Get total count
	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Get paginated data
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func GetRedemptionById(id int) (*Redemption, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	redemption := Redemption{Id: id}
	var err error = nil
	err = DB.First(&redemption, "id = ?", id).Error
	return &redemption, err
}

func Redeem(key string, userId int) (quota int, err error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return 0, errors.New("未提供兑换码")
	}
	if userId == 0 {
		return 0, errors.New("无效的 user id")
	}
	keyCol := "`key`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		keyCol = `"key"`
	}
	// The legacy wallet entry remains compatible with product codes, but product
	// provisioning must go through the same atomic entitlement + bound-key path
	// as Checkout. Never let the old wallet transaction consume a product code.
	var target Redemption
	if err := DB.Select("id", "target_plan_id").Where(keyCol+" = ?", key).First(&target).Error; err == nil && target.TargetPlanId > 0 {
		result, productErr := RedeemProductCode(key, userId, target.TargetPlanId)
		if productErr != nil {
			return 0, productErr
		}
		return int(result.AmountTotal), nil
	}
	redemption := &Redemption{}
	common.RandomSleep()
	err = DB.Transaction(func(tx *gorm.DB) error {
		err := lockForUpdate(tx).Where(keyCol+" = ?", key).First(redemption).Error
		if err != nil {
			return errors.New("无效的兑换码")
		}
		if redemption.Status != common.RedemptionCodeStatusEnabled {
			return errors.New("该兑换码已被使用")
		}
		if redemption.ExpiredTime != 0 && redemption.ExpiredTime < common.GetTimestamp() {
			return errors.New("该兑换码已过期")
		}
		// Compare-and-swap on status: only the transaction that flips
		// enabled -> used may credit quota, so a concurrent redeem of the
		// same code loses here even without a row lock (e.g. on SQLite).
		result := tx.Model(&Redemption{}).
			Where("id = ? AND status = ?", redemption.Id, common.RedemptionCodeStatusEnabled).
			Updates(map[string]interface{}{
				"redeemed_time": common.GetTimestamp(),
				"status":        common.RedemptionCodeStatusUsed,
				"used_user_id":  userId,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("该兑换码已被使用")
		}
		return tx.Model(&User{}).Where("id = ?", userId).Update("quota", gorm.Expr("quota + ?", redemption.Quota)).Error
	})
	if err != nil {
		common.SysError("redemption failed: " + err.Error())
		return 0, ErrRedeemFailed
	}
	RecordLog(userId, LogTypeTopup, fmt.Sprintf("通过兑换码充值 %s，兑换码ID %d", logger.LogQuota(redemption.Quota), redemption.Id))
	return redemption.Quota, nil
}

// RedeemProductCode redeems any plan-bound code. It intentionally does not
// depend on payment-compliance switches because no money, order, or wallet
// balance is involved. Wallet top-up codes continue using Redeem.
func RedeemProductCode(key string, userId int, expectedPlanId int) (*ProductRedemptionResult, error) {
	key = strings.TrimSpace(key)
	if key == "" || userId <= 0 || expectedPlanId <= 0 {
		return nil, errors.New("兑换码、用户和商品均不能为空")
	}
	var response *ProductRedemptionResult
	keyCol := "`key`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		keyCol = `"key"`
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var redemption Redemption
		if err := lockForUpdate(tx).Where(keyCol+" = ?", key).First(&redemption).Error; err != nil {
			return errors.New("无效的兑换码")
		}
		if redemption.TargetPlanId != expectedPlanId || redemption.TargetPlanId <= 0 {
			return errors.New("兑换码不适用于所选商品")
		}
		if redemption.Status == common.RedemptionCodeStatusUsed {
			if redemption.UsedUserId != userId {
				return errors.New("该兑换码已被使用")
			}
			var claim ProductRedemptionClaim
			if err := tx.Where("redemption_id = ? AND user_id = ?", redemption.Id, userId).First(&claim).Error; err != nil {
				return errors.New("兑换已完成但发放记录异常，请联系管理员")
			}
			result, err := buildProductRedemptionResultTx(tx, &redemption, &claim, true)
			if err != nil {
				return err
			}
			response = result
			return nil
		}
		if redemption.Status != common.RedemptionCodeStatusEnabled {
			return errors.New("该兑换码不可用")
		}
		if redemption.ExpiredTime != 0 && redemption.ExpiredTime < common.GetTimestamp() {
			return errors.New("该兑换码已过期")
		}
		plan, err := getSubscriptionPlanByIdTx(tx, redemption.TargetPlanId)
		if err != nil || plan == nil {
			return fmt.Errorf("兑换码绑定的商品不存在: %w", err)
		}
		if !plan.RedemptionEnabled {
			return errors.New("该商品当前暂停兑换")
		}

		// Classroom codes have a one-account-per-activity guard. Other products
		// use a per-code scope, while their own MaxPurchasePerUser/monthly-cycle
		// rules remain authoritative in CreateUserSubscriptionFromPlanTx.
		claimScope := fmt.Sprintf("redemption:%d", redemption.Id)
		if plan.ProductType == ProductTypePackDeepSeekClassroom {
			claimScope = plan.ProductFamily
		}
		claim := ProductRedemptionClaim{
			UserId: userId, ProductFamily: claimScope,
			RedemptionId: redemption.Id, CreatedTime: common.GetTimestamp(),
		}
		if err := tx.Create(&claim).Error; err != nil {
			if plan.ProductType == ProductTypePackDeepSeekClassroom {
				return errors.New("同一账户在本次课堂活动中最多领取一份")
			}
			return errors.New("该兑换请求已处理")
		}
		update := tx.Model(&Redemption{}).
			Where("id = ? AND status = ?", redemption.Id, common.RedemptionCodeStatusEnabled).
			Updates(map[string]interface{}{
				"redeemed_time": common.GetTimestamp(), "status": common.RedemptionCodeStatusUsed,
				"used_user_id": userId,
			})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return errors.New("该兑换码已被使用")
		}
		sub, token, err := createProvisionedSubscriptionFromPlanTx(tx, userId, plan, "redemption:"+redemption.BatchCode)
		if err != nil {
			return fmt.Errorf("商品权益发放失败: %w", err)
		}
		claim.UserSubscriptionId = sub.Id
		claim.TokenId = token.Id
		if err := tx.Model(&ProductRedemptionClaim{}).Where("id = ?", claim.Id).Updates(map[string]interface{}{
			"user_subscription_id": sub.Id, "token_id": token.Id,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&Redemption{}).Where("id = ?", redemption.Id).Updates(map[string]interface{}{
			"claimed_subscription_id": sub.Id, "claimed_token_id": token.Id,
		}).Error; err != nil {
			return err
		}
		response = &ProductRedemptionResult{
			RedemptionId: redemption.Id, PlanId: plan.Id, ProductType: plan.ProductType,
			ProductCode: plan.ProductCode, ProductFamily: plan.ProductFamily,
			UserSubscriptionId: sub.Id, TokenId: token.Id, ExpiresAt: sub.EndTime,
			AmountTotal: sub.AmountTotal,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	RecordLog(userId, LogTypeTopup, fmt.Sprintf("通过商品兑换码领取权益，兑换码ID %d，商品 %s", response.RedemptionId, response.ProductCode))
	return response, nil
}

func buildProductRedemptionResultTx(tx *gorm.DB, redemption *Redemption, claim *ProductRedemptionClaim, idempotent bool) (*ProductRedemptionResult, error) {
	var sub UserSubscription
	if err := tx.Where("id = ? AND user_id = ?", claim.UserSubscriptionId, claim.UserId).First(&sub).Error; err != nil {
		return nil, err
	}
	return &ProductRedemptionResult{
		RedemptionId: redemption.Id, PlanId: sub.PlanId, ProductType: sub.ProductType,
		ProductCode: sub.ProductCode, ProductFamily: sub.ProductFamily,
		UserSubscriptionId: sub.Id, TokenId: claim.TokenId, ExpiresAt: sub.EndTime,
		AmountTotal: sub.AmountTotal, Idempotent: idempotent,
	}, nil
}

func CreateProductRedemptionBatch(adminUserId int, req ProductRedemptionBatchRequest) (*ProductRedemptionBatchSummary, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.BatchCode = strings.TrimSpace(req.BatchCode)
	if adminUserId <= 0 || req.TargetPlanId <= 0 || req.Name == "" || len([]rune(req.Name)) > 40 {
		return nil, errors.New("批次名称、商品和管理员均不能为空")
	}
	if req.PrimaryCount < 1 || req.PrimaryCount > 200 || req.ReserveCount < 0 || req.ReserveCount > 20 {
		return nil, errors.New("正式码需为 1-200 个，备用码最多 20 个")
	}
	if req.ExpiredTime != 0 && req.ExpiredTime <= common.GetTimestamp() {
		return nil, errors.New("兑换码有效期必须晚于当前时间")
	}
	plan, err := GetSubscriptionPlanById(req.TargetPlanId)
	if err != nil || plan == nil {
		return nil, errors.New("商品不存在")
	}
	if !plan.RedemptionEnabled {
		return nil, errors.New("商品兑换未启用")
	}
	if req.BatchCode == "" {
		req.BatchCode = "DSCLASS-" + common.GetRandomString(12)
	}
	if len(req.BatchCode) > 64 {
		return nil, errors.New("批次编号过长")
	}
	total := req.PrimaryCount + req.ReserveCount
	rows := make([]Redemption, 0, total)
	for i := 0; i < total; i++ {
		rows = append(rows, Redemption{
			UserId: adminUserId, Key: common.GetUUID(), Status: common.RedemptionCodeStatusEnabled,
			Name: req.Name, CreatedTime: common.GetTimestamp(), ExpiredTime: req.ExpiredTime,
			TargetPlanId: req.TargetPlanId, BatchCode: req.BatchCode, IsReserve: i >= req.PrimaryCount,
		})
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&Redemption{}).Where("batch_code = ?", req.BatchCode).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("批次编号已存在")
		}
		return tx.CreateInBatches(&rows, 100).Error
	}); err != nil {
		return nil, err
	}
	return &ProductRedemptionBatchSummary{
		BatchCode: req.BatchCode, Name: req.Name, TargetPlanId: req.TargetPlanId,
		PrimaryCount: req.PrimaryCount, ReserveCount: req.ReserveCount,
		UnusedPrimaryCount: req.PrimaryCount, UnusedReserveCount: req.ReserveCount,
		ExpiredTime: req.ExpiredTime,
	}, nil
}

func ListProductRedemptionBatches() ([]ProductRedemptionBatchSummary, error) {
	var rows []Redemption
	if err := DB.Where("batch_code <> '' AND target_plan_id > 0").Order("id desc").Find(&rows).Error; err != nil {
		return nil, err
	}
	summaries := make([]ProductRedemptionBatchSummary, 0)
	byBatch := map[string]int{}
	now := common.GetTimestamp()
	for _, row := range rows {
		index, ok := byBatch[row.BatchCode]
		if !ok {
			index = len(summaries)
			byBatch[row.BatchCode] = index
			summaries = append(summaries, ProductRedemptionBatchSummary{
				BatchCode: row.BatchCode, Name: row.Name, TargetPlanId: row.TargetPlanId, ExpiredTime: row.ExpiredTime,
			})
		}
		s := &summaries[index]
		if row.IsReserve {
			s.ReserveCount++
		} else {
			s.PrimaryCount++
		}
		if row.Status == common.RedemptionCodeStatusUsed {
			s.UsedCount++
		} else if row.Status == common.RedemptionCodeStatusEnabled && (row.ExpiredTime == 0 || row.ExpiredTime >= now) {
			if row.IsReserve {
				s.UnusedReserveCount++
			} else {
				s.UnusedPrimaryCount++
			}
		}
	}
	return summaries, nil
}

func GetUnusedProductRedemptions(batchCode string, includeReserve bool) ([]Redemption, error) {
	query := DB.Where("batch_code = ? AND status = ? AND (expired_time = 0 OR expired_time >= ?)",
		strings.TrimSpace(batchCode), common.RedemptionCodeStatusEnabled, common.GetTimestamp())
	if !includeReserve {
		query = query.Where("is_reserve = ?", false)
	}
	var rows []Redemption
	err := query.Order("is_reserve asc, id asc").Find(&rows).Error
	return rows, err
}

func (redemption *Redemption) Insert() error {
	var err error
	err = DB.Create(redemption).Error
	return err
}

func (redemption *Redemption) SelectUpdate() error {
	// This can update zero values
	return DB.Model(redemption).Select("redeemed_time", "status").Updates(redemption).Error
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (redemption *Redemption) Update() error {
	var err error
	err = DB.Model(redemption).Select("name", "status", "quota", "redeemed_time", "expired_time").Updates(redemption).Error
	return err
}

func (redemption *Redemption) Delete() error {
	var err error
	err = DB.Delete(redemption).Error
	return err
}

func DeleteRedemptionById(id int) (err error) {
	if id == 0 {
		return errors.New("id 为空！")
	}
	redemption := Redemption{Id: id}
	err = DB.Where(redemption).First(&redemption).Error
	if err != nil {
		return err
	}
	return redemption.Delete()
}

func DeleteInvalidRedemptions() (int64, error) {
	now := common.GetTimestamp()
	result := DB.Where("status IN ? OR (status = ? AND expired_time != 0 AND expired_time < ?)", []int{common.RedemptionCodeStatusUsed, common.RedemptionCodeStatusDisabled}, common.RedemptionCodeStatusEnabled, now).Delete(&Redemption{})
	return result.RowsAffected, result.Error
}
