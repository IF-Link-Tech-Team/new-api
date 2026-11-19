package model

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"gorm.io/gorm/clause"
)

// PricingGroupVariant contains only the sale visible in one eligible group.
// Provider credentials, procurement costs and channel identifiers never leave
// the catalog through the model square.
type PricingGroupVariant struct {
	Group string `json:"group"`
	PricingPluginVariant
}

func GetGroupPricing(groups []string) ([]Pricing, error) {
	_, rows, err := ReadAPIChannelCatalog(DB, "")
	if err != nil {
		return nil, err
	}
	var abilities []Ability
	if err = DB.Where(clause.Eq{Column: "group", Value: groups}).Where("enabled = ?", true).Order("model, channel_id").Find(&abilities).Error; err != nil {
		return nil, err
	}
	var channels []Channel
	if err = DB.Select("id", "type", "models", "model_mapping", "setting").Where("status = ?", common.ChannelStatusEnabled).Find(&channels).Error; err != nil {
		return nil, err
	}
	byChannel := make(map[int]Channel, len(channels))
	for _, channel := range channels {
		byChannel[channel.Id] = channel
	}
	type pair struct {
		model   string
		channel int
	}
	prices := make(map[pair]APIChannelPrice, len(rows))
	for _, row := range rows {
		if row.Enabled {
			prices[pair{row.Model, row.ChannelID}] = row
		}
	}
	local, err := ReadGroupModelPrices(DB, groups, "")
	if err != nil {
		return nil, err
	}
	type groupPair struct {
		group   string
		channel int
		model   string
	}
	groupPrices := make(map[groupPair]APIChannelPrice, len(local))
	for _, rate := range local {
		channel, ok := byChannel[rate.ChannelID]
		if !ok {
			continue
		}
		row, err := rate.APIPrice(channel)
		if err != nil {
			return nil, err
		}
		groupPrices[groupPair{rate.GroupName, rate.ChannelID, rate.Model}] = row
	}
	visibleAbilities := make([]AbilityWithChannel, 0, len(abilities))
	for _, ability := range abilities {
		if channel, ok := byChannel[ability.ChannelId]; ok {
			visibleAbilities = append(visibleAbilities, AbilityWithChannel{Ability: ability, ChannelType: channel.Type, ChannelSetting: channel.Setting, ChannelModelMapping: channel.ModelMapping})
		}
	}
	advancedConfigs := loadPricingAdvancedCustomConfigs(visibleAbilities)
	variants := make(map[string][]PricingGroupVariant)
	endpoints := make(map[string][]constant.EndpointType)
	generation := jsplugin.DefaultRegistry.Generation()
	for _, ability := range abilities {
		channel, ok := byChannel[ability.ChannelId]
		if !ok || !slices.Contains(channel.GetModels(), ability.Model) {
			continue
		}
		mapped, mapErr := ResolveMappedModelName(channel.GetModelMapping(), ability.Model)
		if mapErr != nil {
			continue
		}
		variant := PricingGroupVariant{Group: ability.Group, PricingPluginVariant: PricingPluginVariant{BillingMode: billing_setting.BillingModeTieredExpr}}
		nativeTask := false
		if channel.Type == constant.ChannelTypeTaskPlugin || channel.Type == constant.ChannelTypeNewAPI {
			for _, key := range []string{"jiliu-h3", "tencent-vod", "tokenhub-media"} {
				plugin, found := generation.Get(key)
				if !found || !channel.GetSetting().BindsTaskPlugin(key) || !slices.Contains(plugin.Meta.Models, mapped) {
					continue
				}
				nativeTask = true
				expression, configured := billing_setting.ResolveTaskBillingExpr(key, ability.Model, mapped)
				schema, examples := plugin.Meta.UsageForModels(mapped, ability.Model)
				if configured && billing_setting.TaskExprCompatible(expression, schema) {
					variant.BillingExpr = expression
					variant.PluginKey, variant.PluginName, variant.Icon = key, plugin.Meta.Name, plugin.Meta.Icon
					variant.BillingUsageSchema, variant.BillingUsageExamples = jsplugin.CloneUsageSchema(schema), jsplugin.CloneUsageExamples(examples)
				}
				break
			}
		}
		if !nativeTask {
			row, found := groupPrices[groupPair{ability.Group, ability.ChannelId, ability.Model}]
			if !found {
				row, found = prices[pair{ability.Model, ability.ChannelId}]
			}
			if !found || row.UpstreamModel != mapped {
				continue
			}
			variant.BillingExpr, err = row.BillingExpression()
			if err != nil {
				return nil, fmt.Errorf("model square price unavailable: %w", err)
			}
		}
		if variant.BillingExpr == "" {
			continue
		}
		if !slices.ContainsFunc(variants[ability.Model], func(existing PricingGroupVariant) bool {
			return existing.Group == variant.Group && existing.PluginKey == variant.PluginKey && existing.BillingExpr == variant.BillingExpr
		}) {
			variants[ability.Model] = append(variants[ability.Model], variant)
		}
		endpoints[ability.Model] = append(endpoints[ability.Model], getPricingEndpointTypesForAbility(AbilityWithChannel{Ability: ability, ChannelType: channel.Type, ChannelSetting: channel.Setting, ChannelModelMapping: channel.ModelMapping}, advancedConfigs)...)
	}
	result := make([]Pricing, 0, len(variants))
	for _, metadata := range GetPricing() {
		entries := variants[metadata.ModelName]
		if len(entries) == 0 {
			continue
		}
		slices.SortStableFunc(entries, func(a, b PricingGroupVariant) int {
			if a.Group < b.Group {
				return -1
			}
			if a.Group > b.Group {
				return 1
			}
			return 0
		})
		item := Pricing{
			ModelName: metadata.ModelName, Description: metadata.Description,
			Icon: metadata.Icon, Tags: metadata.Tags, VendorID: metadata.VendorID, OwnerBy: metadata.OwnerBy,
			QuotaType: 0, BillingMode: entries[0].BillingMode, BillingExpr: entries[0].BillingExpr,
			BillingUsageSchema: entries[0].BillingUsageSchema, BillingUsageExamples: entries[0].BillingUsageExamples,
			GroupPricing: entries,
		}
		for i := range item.GroupPricing {
			entry := &item.GroupPricing[i]
			if len(entry.BillingUsageSchema) > 0 {
				// Native task models also advertise their configured submit/query
				// endpoints, rather than only the channel's generic OpenAI endpoint.
				endpoints[item.ModelName] = append(endpoints[item.ModelName], metadata.SupportedEndpointTypes...)
			}
			if entry.PluginName == "" {
				entry.PluginName = entry.Group
			} else {
				entry.PluginName = entry.Group + " · " + entry.PluginName
			}
			entry.PluginKey = "price-" + strconv.Itoa(i+1)
			if !slices.Contains(item.EnableGroup, entry.Group) {
				item.EnableGroup = append(item.EnableGroup, entry.Group)
			}
		}
		for _, endpoint := range endpoints[item.ModelName] {
			if !slices.Contains(item.SupportedEndpointTypes, endpoint) {
				item.SupportedEndpointTypes = append(item.SupportedEndpointTypes, endpoint)
			}
		}
		result = append(result, item)
	}
	return result, nil
}
