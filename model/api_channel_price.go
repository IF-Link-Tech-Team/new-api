package model

import (
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// APIChannelCatalog serializes publications across instances. Prices are append
// only; a request reads one revision and retains all candidates at that revision.
type APIChannelCatalog struct {
	ID         int   `json:"-"`
	Revision   int64 `json:"revision"`
	WriteEpoch int64 `json:"-" gorm:"default:0"`
}

type APIChannelPrice struct {
	GroupName     string `json:"-" gorm:"-"`
	ID            int64  `json:"id"`
	Revision      int64  `json:"revision" gorm:"uniqueIndex:idx_api_channel_revision,priority:1"`
	Model         string `json:"model" gorm:"type:varchar(191);uniqueIndex:idx_api_channel_revision,priority:2;index:idx_api_channel_lookup,priority:1"`
	ChannelID     int    `json:"channel_id" gorm:"uniqueIndex:idx_api_channel_revision,priority:3;index:idx_api_channel_lookup,priority:2"`
	UpstreamModel string `json:"upstream_model" gorm:"type:text"`
	SellingPrice  string `json:"selling_price" gorm:"type:text"`
	// PriceExpression preserves supported native billing rules such as
	// weekday peak/off-peak prices. When set, it is the authoritative price.
	PriceExpression string `json:"billing_expression" gorm:"type:text"`
	Enabled         bool   `json:"enabled"`
	// Nil inherits channel priority/weight. Distinct non-negative positions
	// define an explicit model-level order; all enabled rows must participate.
	Position  *int   `json:"position"`
	ActorID   int    `json:"actor_id"`
	Reason    string `json:"reason" gorm:"type:text"`
	CreatedAt int64  `json:"created_at"`
}

var ErrAPIChannelPriceConflict = errors.New("API channel prices changed; reload before saving")

// Temporary expansion switch. Ordinary upgrades never activate an empty sales
// catalog. P4 enables this explicitly, after migration and reconciliation. An
// invalid value fails closed instead of silently restoring legacy sales.
func APIChannelPricingMode() (bool, error) {
	switch os.Getenv("IFLINK_API_CHANNEL_PRICING") {
	case "", "legacy":
		return false, nil
	case "enabled":
		return true, nil
	default:
		return false, errors.New("invalid IFLINK_API_CHANNEL_PRICING mode")
	}
}

func InitializeAPIChannelCatalog(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&APIChannelCatalog{ID: 1}).Error
}

// ResolveMappedModelName is shared with the native relay mapper, including its
// chained mappings, reasoning aliases, identity mappings and cycle rejection.
func ResolveMappedModelName(raw, modelName string) (string, error) {
	if raw == "" || raw == "{}" {
		return modelName, nil
	}
	var mapping map[string]string
	if err := common.UnmarshalJsonStr(raw, &mapping); err != nil {
		return "", errors.New("unmarshal_model_mapping_failed")
	}
	current := modelName
	visited := map[string]bool{current: true}
	for {
		next, exists := mapping[current]
		if base := reasoning.BaseModelName(current); (!exists || next == "") && base != current {
			next, exists = mapping[base]
		}
		if !exists || next == "" || next == current {
			return current, nil
		}
		if visited[next] {
			return "", errors.New("model_mapping_contains_cycle")
		}
		visited[next] = true
		current = next
	}
}

func (p APIChannelPrice) Prices() (PriceComponents, error) {
	var prices PriceComponents
	var fields map[string]*float64
	if err := common.UnmarshalJsonStr(p.SellingPrice, &fields); err != nil {
		return prices, err
	}
	for _, key := range []string{"input", "cached_input", "cache_creation", "output"} {
		if fields[key] == nil {
			return prices, errors.New("all four API channel price components are required (zero is allowed)")
		}
	}
	if err := common.UnmarshalJsonStr(p.SellingPrice, &prices); err != nil {
		return prices, err
	}
	for _, value := range []float64{prices.Input, prices.CachedInput, prices.CacheCreation, prices.Output} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return prices, errors.New("invalid API channel price")
		}
	}
	return prices, nil
}

func (p APIChannelPrice) BillingExpression() (string, error) {
	if len(p.PriceExpression) > 20000 {
		return "", errors.New("billing expression is too long")
	}
	if p.PriceExpression != "" {
		if p.PriceExpression != strings.TrimSpace(p.PriceExpression) {
			return "", errors.New("billing expression must not have surrounding whitespace")
		}
		if _, err := billingexpr.CompileFromCache(p.PriceExpression); err != nil {
			return "", err
		}
		if billingexpr.UsesFixedPricing(p.PriceExpression) {
			return "", errors.New("API channel prices must be token-based")
		}
		return p.PriceExpression, nil
	}
	prices, err := p.Prices()
	if err != nil {
		return "", err
	}
	// Explicit zero cache components stay explicit: the native engine handles
	// both OpenAI inclusive usage and Anthropic exclusive cache token counts.
	expr := fmt.Sprintf(`tier("base", p * %s + cr * %s + cc * %s + cc1h * %s + c * %s)`,
		strconv.FormatFloat(prices.Input, 'f', -1, 64),
		strconv.FormatFloat(prices.CachedInput, 'f', -1, 64),
		strconv.FormatFloat(prices.CacheCreation, 'f', -1, 64),
		strconv.FormatFloat(prices.CacheCreation, 'f', -1, 64),
		strconv.FormatFloat(prices.Output, 'f', -1, 64))
	if _, err := billingexpr.CompileFromCache(expr); err != nil {
		return "", err
	}
	return expr, nil
}

// APIChannelPricesAt includes disabled latest rows: filtering before selecting
// each latest version would resurrect a previously enabled price after removal.
func APIChannelPricesAt(db *gorm.DB, revision int64, modelName string) ([]APIChannelPrice, error) {
	latest := db.Model(&APIChannelPrice{}).Select("MAX(id)").Where("revision <= ?", revision)
	if modelName != "" {
		latest = latest.Where("model = ?", modelName)
	}
	latest = latest.Group("model, channel_id")
	rows := make([]APIChannelPrice, 0)
	err := db.Where("id IN (?)", latest).Order("model, channel_id").Find(&rows).Error
	return rows, err
}

func ReadAPIChannelCatalog(db *gorm.DB, modelName string) (int64, []APIChannelPrice, error) {
	var state APIChannelCatalog
	if err := db.First(&state, 1).Error; err != nil {
		return 0, nil, err
	}
	rows, err := APIChannelPricesAt(db, state.Revision, modelName)
	return state.Revision, rows, err
}

// APIChannelModelsForGroups is the public model-list projection. It performs
// bounded bulk reads, never returning procurement costs or channel credentials.
func APIChannelModelsForGroups(db *gorm.DB, groups []string) (map[string]bool, error) {
	_, rows, err := ReadAPIChannelCatalog(db, "")
	if err != nil {
		return nil, err
	}
	var abilities []Ability
	if err := db.Where(clause.Eq{Column: "group", Value: groups}).Where("enabled = ?", true).Find(&abilities).Error; err != nil {
		return nil, err
	}
	type pair struct {
		model   string
		channel int
	}
	allowed := make(map[pair]bool, len(abilities))
	for _, ability := range abilities {
		allowed[pair{ability.Model, ability.ChannelId}] = true
	}
	var channels []Channel
	if err := db.Select("id", "type", "models", "model_mapping", "setting").Where("status = ?", common.ChannelStatusEnabled).Find(&channels).Error; err != nil {
		return nil, err
	}
	bindings := make(map[int]Channel, len(channels))
	for _, channel := range channels {
		bindings[channel.Id] = channel
	}
	result := make(map[string]bool)
	// These providers have measured task billing. They must not require a token-price
	// catalog row to appear in model discovery, or inherit one for charging.
	for _, key := range []string{"jiliu-h3", "tencent-vod", "tokenhub-media"} {
		plugin, ok := jsplugin.DefaultRegistry.Generation().Get(key)
		if !ok {
			continue
		}
		for _, channel := range channels {
			if (channel.Type != constant.ChannelTypeTaskPlugin && channel.Type != constant.ChannelTypeNewAPI) || !channel.GetSetting().BindsTaskPlugin(plugin.Meta.Key) {
				continue
			}
			for _, name := range channel.GetModels() {
				if !allowed[pair{name, channel.Id}] {
					continue
				}
				mapped, err := ResolveMappedModelName(channel.GetModelMapping(), name)
				if err != nil || !slices.Contains(plugin.Meta.Models, mapped) {
					continue
				}
				expression, configured := billing_setting.ResolveTaskBillingExpr(plugin.Meta.Key, name, mapped)
				schema, _ := plugin.Meta.UsageForModels(mapped, name)
				if configured && billing_setting.TaskExprCompatible(expression, schema) {
					result[name] = true
				}
			}
		}
	}
	for _, row := range rows {
		channel, ok := bindings[row.ChannelID]
		if !ok || !row.Enabled || !allowed[pair{row.Model, row.ChannelID}] || !slices.Contains(channel.GetModels(), row.Model) {
			continue
		}
		mapped, err := ResolveMappedModelName(channel.GetModelMapping(), row.Model)
		if err == nil && mapped == row.UpstreamModel {
			result[row.Model] = true
		}
	}
	local, err := ReadGroupModelPrices(db, groups, "")
	if err != nil {
		return nil, err
	}
	for _, price := range local {
		channel, ok := bindings[price.ChannelID]
		if ok && allowed[pair{price.Model, price.ChannelID}] && slices.Contains(channel.GetModels(), price.Model) {
			result[price.Model] = true
		}
	}
	return result, nil
}

type APIChannelPriceSnapshot struct {
	Revision int64
	Rows     []APIChannelPrice
}

// RequestAPIChannelPrices freezes the full candidate set before selecting a
// channel. Nil selects native pricing: legacy rollout mode or a pinned measured task.
func RequestAPIChannelPrices(ctx *gin.Context, modelName string) (*APIChannelPriceSnapshot, error) {
	key := "iflink_api_channel_prices:" + modelName
	if value, ok := ctx.Get(key); ok {
		return value.(*APIChannelPriceSnapshot), nil
	}
	enabled, err := APIChannelPricingMode()
	if err != nil {
		return nil, err
	}
	var snapshot *APIChannelPriceSnapshot
	// Only validated measured-task providers opt into their existing task price
	// and completion settlement. Other non-token endpoints remain fail-closed.
	if enabled && ctx.Request != nil && ctx.Request.Method == "POST" {
		pinned, ok := common.GetContextKeyType[jsplugin.PinnedPlugin](ctx, constant.ContextKey(jsplugin.ContextKeyPinnedPlugin))
		if ok && pinned.Plugin != nil && slices.Contains([]string{"jiliu-h3", "tencent-vod", "tokenhub-media"}, pinned.Plugin.Meta.Key) && ctx.GetString("expected_task_plugin_key") == pinned.Plugin.Meta.Key {
			native, hasNative := common.GetContextKeyType[jsplugin.PinnedRoute](ctx, constant.ContextKey(jsplugin.ContextKeyPinnedRoute))
			path := ctx.Request.URL.Path
			standard := path == "/v1/videos" || (slices.Contains([]string{"tencent-vod", "tokenhub-media"}, pinned.Plugin.Meta.Key) && (path == "/v1/images/generations" || path == "/v1/images/edits"))
			declared := hasNative && native.Plugin == pinned.Plugin && native.Route.Type == jsplugin.RouteTypeSubmit && native.Route.Path == path
			endpoint, hasEndpoint := common.GetContextKeyType[jsplugin.PinnedEndpoint](ctx, constant.ContextKey(jsplugin.ContextKeyPinnedEndpoint))
			miniMax := hasEndpoint && endpoint.Plugin == pinned.Plugin && endpoint.Protocol == jsplugin.ProtocolMiniMaxVideo && endpoint.Operation.Name == "create" && endpoint.Operation.Path == path
			if standard || declared || miniMax {
				ctx.Set(key, snapshot)
				return nil, nil
			}
		}
	}
	if enabled {
		revision, rows, err := ReadAPIChannelCatalog(DB, modelName)
		if err != nil {
			return nil, fmt.Errorf("API channel catalog unavailable: %w", err)
		}
		snapshot = &APIChannelPriceSnapshot{Revision: revision, Rows: make([]APIChannelPrice, 0, len(rows))}
		// A bound plan retains its original prices. The authenticated token group,
		// not the account's primary group, selects autonomous group pricing.
		var local []GroupModelPrice
		group := common.GetContextKeyString(ctx, constant.ContextKeyUsingGroup)
		if group != "" && common.GetContextKeyInt(ctx, constant.ContextKeyTokenUserSubscriptionId) == 0 {
			local, err = ReadGroupModelPrices(DB, []string{group}, modelName)
			if err != nil {
				return nil, err
			}
		}
		ids := make([]int, 0, len(rows)+len(local))
		for _, price := range local {
			ids = append(ids, price.ChannelID)
		}
		for _, row := range rows {
			ids = append(ids, row.ChannelID)
		}
		channels := make([]Channel, 0)
		if len(ids) > 0 {
			if err := DB.Select("id", "models", "model_mapping").Where("id IN ?", ids).Find(&channels).Error; err != nil {
				return nil, err
			}
		}
		for _, price := range local {
			for _, channel := range channels {
				if channel.Id != price.ChannelID {
					continue
				}
				row, mapErr := price.APIPrice(channel)
				if mapErr != nil {
					return nil, mapErr
				}
				for _, previous := range rows {
					if previous.ChannelID == row.ChannelID {
						row.Position = previous.Position
						break
					}
				}
				rows = slices.DeleteFunc(rows, func(old APIChannelPrice) bool { return old.ChannelID == row.ChannelID })
				rows = append(rows, row)
			}
		}
		bindings := make(map[int]string, len(channels))
		for _, channel := range channels {
			if !slices.Contains(channel.GetModels(), modelName) {
				continue
			}
			mapped, err := ResolveMappedModelName(channel.GetModelMapping(), modelName)
			if err == nil {
				bindings[channel.Id] = mapped
			}
		}
		for _, row := range rows {
			if row.Enabled && row.Model == modelName && bindings[row.ChannelID] == row.UpstreamModel && row.UpstreamModel != "" {
				snapshot.Rows = append(snapshot.Rows, row)
			}
		}
		// A new group-only route has no global position. Append it to an
		// explicitly ordered catalog rather than silently excluding it.
		lastPosition := -1
		for _, row := range snapshot.Rows {
			if row.Position != nil {
				lastPosition = max(lastPosition, *row.Position)
			}
		}
		if len(local) > 0 && lastPosition >= 0 {
			for i := range snapshot.Rows {
				if snapshot.Rows[i].Position == nil {
					lastPosition++
					snapshot.Rows[i].Position = common.GetPointer(lastPosition)
				}
			}
		}
		slices.SortStableFunc(snapshot.Rows, func(left, right APIChannelPrice) int {
			if left.Position == nil && right.Position == nil {
				return left.ChannelID - right.ChannelID
			}
			if left.Position == nil {
				return 1
			}
			if right.Position == nil {
				return -1
			}
			return *left.Position - *right.Position
		})
	}
	ctx.Set(key, snapshot)
	return snapshot, nil
}

// PublishAPIChannelPrices atomically publishes a batch, including price and
// routing changes. The revision CAS is also the SQLite write lock; it avoids
// read-then-write races and is shared by MySQL/PostgreSQL writers.
func PublishAPIChannelPrices(db *gorm.DB, expectedRevision int64, changes []APIChannelPrice, actorID int, reason string) (int64, error) {
	if expectedRevision < 0 || expectedRevision >= common.MaxWalletQuota || len(changes) == 0 || len(changes) > 500 || actorID <= 0 || strings.TrimSpace(reason) == "" || len(reason) > 2000 {
		return 0, errors.New("invalid API channel price publication")
	}
	newRevision := expectedRevision + 1
	err := db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&APIChannelCatalog{}).Where("id = ? AND revision = ?", 1, expectedRevision).Update("revision", newRevision)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrAPIChannelPriceConflict
		}
		current, err := APIChannelPricesAt(tx, expectedRevision, "")
		if err != nil {
			return err
		}
		byModel := make(map[string]map[int]APIChannelPrice)
		for _, row := range current {
			if byModel[row.Model] == nil {
				byModel[row.Model] = make(map[int]APIChannelPrice)
			}
			byModel[row.Model][row.ChannelID] = row
		}
		seen := make(map[string]map[int]bool)
		for i := range changes {
			row := &changes[i]
			if row.Model == "" || row.Model != strings.TrimSpace(row.Model) || len(row.Model) > 191 || strings.ContainsAny(row.Model, ",\r\n") || row.ChannelID <= 0 || row.Position != nil && (*row.Position < 0 || *row.Position > 10000) {
				return errors.New("invalid model, channel or route position")
			}
			if seen[row.Model] == nil {
				seen[row.Model] = make(map[int]bool)
			}
			if seen[row.Model][row.ChannelID] {
				return errors.New("duplicate model/channel in publication")
			}
			seen[row.Model][row.ChannelID] = true
			if _, err := row.BillingExpression(); err != nil {
				return err
			}
			if row.PriceExpression != "" {
				if err := billing_setting.SmokeTestExpr(row.PriceExpression); err != nil {
					return fmt.Errorf("model %s channel %d: %w", row.Model, row.ChannelID, err)
				}
			}
			// Disabled orphan rows can always be retired. Re-enabling requires a
			// valid, exact client model and explicit mapping confirmation.
			if row.Enabled {
				var channel Channel
				if err := lockForUpdate(tx).First(&channel, row.ChannelID).Error; err != nil {
					return err
				}
				if !slices.Contains(channel.GetModels(), row.Model) {
					return fmt.Errorf("model %s is not registered on channel %d", row.Model, row.ChannelID)
				}
				upstream, err := ResolveMappedModelName(channel.GetModelMapping(), row.Model)
				if err != nil || strings.TrimSpace(upstream) == "" || row.UpstreamModel != upstream {
					return errors.New("channel model mapping changed; reload before saving")
				}
			}
			row.ID, row.Revision = 0, newRevision
			row.ActorID, row.Reason, row.CreatedAt = actorID, strings.TrimSpace(reason), common.GetTimestamp()
			if byModel[row.Model] == nil {
				byModel[row.Model] = make(map[int]APIChannelPrice)
			}
			byModel[row.Model][row.ChannelID] = *row
		}
		for name := range seen {
			positions := make(map[int]bool)
			ordered, inherited := false, false
			for _, row := range byModel[name] {
				if !row.Enabled {
					continue
				}
				if row.Position == nil {
					inherited = true
					continue
				}
				ordered = true
				if positions[*row.Position] {
					return errors.New("route positions must be distinct within a model")
				}
				positions[*row.Position] = true
			}
			if ordered && inherited {
				return errors.New("set a route position for every enabled channel, or inherit for all")
			}
		}
		if err := validateAPIChannelProductReferences(tx, current, changes); err != nil {
			return err
		}
		return tx.Create(&changes).Error
	})
	return newRevision, err
}

func validateAPIChannelProductReferences(tx *gorm.DB, current []APIChannelPrice, changes []APIChannelPrice) error {
	removedModels := make(map[string]bool)
	for _, row := range changes {
		if !row.Enabled {
			removedModels[row.Model] = true
		}
	}
	if len(removedModels) == 0 {
		return nil
	}
	future := make(map[string]APIChannelPrice, len(current)+len(changes))
	for _, row := range current {
		future[fmt.Sprintf("%s:%d", row.Model, row.ChannelID)] = row
	}
	for _, row := range changes {
		future[fmt.Sprintf("%s:%d", row.Model, row.ChannelID)] = row
	}
	var channels []Channel
	if err := tx.Select("id", "status", "models", "model_mapping").Find(&channels).Error; err != nil {
		return err
	}
	bindings := make(map[int]Channel, len(channels))
	for _, channel := range channels {
		bindings[channel.Id] = channel
	}
	hasFutureRoute := func(model string, rates []ProductChannelRate) bool {
		for _, rate := range rates {
			row, ok := future[fmt.Sprintf("%s:%d", model, rate.ChannelID)]
			channel := bindings[rate.ChannelID]
			mapped, err := ResolveMappedModelName(channel.GetModelMapping(), model)
			if ok && row.Enabled && channel.Status == common.ChannelStatusEnabled && slices.Contains(channel.GetModels(), model) && err == nil && mapped == row.UpstreamModel {
				return true
			}
		}
		return false
	}
	check := func(owner string, ownerID int, modelScope, book, version string) error {
		if book == "" {
			return nil
		}
		for _, model := range explicitModelIds(modelScope) {
			if !removedModels[model] {
				continue
			}
			price, err := ResolvePriceBookPrice(tx, book, model, version)
			if err != nil {
				return err
			}
			if price == nil || price.ChannelRates == "" {
				continue
			}
			rates, err := price.ProductChannelRates()
			if err != nil {
				return err
			}
			if !hasFutureRoute(model, rates) {
				return fmt.Errorf("cannot disable API channel: no usable API channel remains for %s %d model %s", owner, ownerID, model)
			}
		}
		return nil
	}
	var plans []SubscriptionPlan
	if err := tx.Select("id", "model_scope", "price_book", "price_version").
		Where("(enabled = ? OR redemption_enabled = ?) AND (product_type = ? OR product_type IS NULL)", true, true, "").Find(&plans).Error; err != nil {
		return err
	}
	for _, plan := range plans {
		book, err := ProductPriceBook(plan.Id)
		if err != nil || plan.PriceBook != book {
			continue
		}
		if err := check("active product", plan.Id, plan.ModelScope, book, plan.PriceVersion); err != nil {
			return err
		}
	}
	var entitlements []UserSubscription
	if err := tx.Select("id", "model_scope", "price_book", "price_version").
		Where("status = ? AND end_time > ?", "active", getDBTimestampTx(tx)).Find(&entitlements).Error; err != nil {
		return err
	}
	for _, entitlement := range entitlements {
		if err := check("active entitlement", entitlement.Id, entitlement.ModelScope, entitlement.PriceBook, entitlement.PriceVersion); err != nil {
			return err
		}
	}
	// An unpaid or compensation-pending order can still issue its frozen
	// configuration later. Keep its last usable route until it is resolved;
	// disabling the current product does not extinguish that obligation.
	if tx.Migrator().HasTable(&SubscriptionOrder{}) {
		var orders []SubscriptionOrder
		if err := tx.Select("id", "model_scope", "price_book", "price_version").
			Where("(status = ? OR status = ?) AND (price_book <> ?)", common.TopUpStatusPending, SubscriptionOrderStatusPaidReview, "").Find(&orders).Error; err != nil {
			return err
		}
		for _, order := range orders {
			if err := check("unresolved order", order.Id, order.ModelScope, order.PriceBook, order.PriceVersion); err != nil {
				return err
			}
		}
	}
	return nil
}
