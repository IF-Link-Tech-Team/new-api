package controller

import (
	"maps"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func filterPricingByUsableGroups(pricing []model.Pricing, usableGroup map[string]string) []model.Pricing {
	if len(pricing) == 0 {
		return pricing
	}
	if len(usableGroup) == 0 {
		return []model.Pricing{}
	}

	filtered := make([]model.Pricing, 0, len(pricing))
	for _, item := range pricing {
		visibleGroups := make([]string, 0, len(item.EnableGroup))
		for _, group := range item.EnableGroup {
			if _, ok := usableGroup[group]; ok || group == "all" {
				visibleGroups = append(visibleGroups, group)
			}
		}
		if len(visibleGroups) > 0 {
			item.EnableGroup = visibleGroups
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func GetPricing(c *gin.Context) {
	pricing := model.GetPricing()
	userId, exists := c.Get("id")
	usableGroup := map[string]string{}
	groupRatio := map[string]float64{}
	maps.Copy(groupRatio, ratio_setting.GetGroupRatioCopy())
	var group string
	role := common.RoleGuestUser
	if exists {
		user, err := model.GetUserCache(userId.(int))
		if err == nil {
			group = user.Group
			role = user.Role
			for g := range groupRatio {
				ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
				if ok {
					groupRatio[g] = ratio
				}
			}
		}
	}

	usableGroup = service.GetUserUsableGroups(group, role)
	channelPricing, err := model.APIChannelPricingMode()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if channelPricing {
		pricing, err = model.GetGroupPricing(slices.Collect(maps.Keys(usableGroup)))
		if err != nil {
			common.ApiError(c, err)
			return
		}
	} else {
		pricing = filterPricingByUsableGroups(pricing, usableGroup)
	}
	visibleVendors := make(map[int]bool)
	visibleEndpoints := make(map[string]bool)
	for _, item := range pricing {
		visibleVendors[item.VendorID] = true
		for _, endpoint := range item.SupportedEndpointTypes {
			visibleEndpoints[string(endpoint)] = true
		}
	}
	vendors := []model.PricingVendor{}
	for _, vendor := range model.GetVendors() {
		if visibleVendors[vendor.ID] {
			vendors = append(vendors, vendor)
		}
	}
	endpoints := make(map[string]common.EndpointInfo)
	for key, endpoint := range model.GetSupportedEndpointMap() {
		if visibleEndpoints[key] {
			endpoints[key] = endpoint
		}
	}
	// check groupRatio contains usableGroup
	for group := range ratio_setting.GetGroupRatioCopy() {
		if _, ok := usableGroup[group]; !ok {
			delete(groupRatio, group)
		}
	}

	c.JSON(200, gin.H{
		"success":            true,
		"data":               pricing,
		"vendors":            vendors,
		"group_ratio":        groupRatio,
		"usable_group":       usableGroup,
		"supported_endpoint": endpoints,
		"auto_groups":        service.GetUserAutoGroup(group, role),
		"pricing_version":    "a42d372ccf0b5dd13ecf71203521f9d2",
	})
}

func ResetModelRatio(c *gin.Context) {
	defaultStr := ratio_setting.DefaultModelRatio2JSONString()
	err := model.UpdateOption("ModelRatio", defaultStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	err = ratio_setting.UpdateModelRatioByJSONString(defaultStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccessT(c, "Model ratios reset", nil)
}
