package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func ListBillingRefunds(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit < 1 || limit > 100 || offset < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid pagination"})
		return
	}
	receipts, total, err := model.ListBillingRefunds(c.Query("status"), limit, offset)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": receipts, "total": total}})
}

func ReviewBillingRefund(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid receipt ID"})
		return
	}
	var body struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid request"})
		return
	}
	reviewer := c.GetInt("id")
	if err := model.ReviewBillingRefund(id, reviewer, body.Action, strings.TrimSpace(body.Reason)); err != nil {
		common.ApiError(c, err)
		return
	}
	model.RecordLog(reviewer, model.LogTypeManage, fmt.Sprintf("billing refund reconciliation: receipt=%d action=%s", id, body.Action))
	c.JSON(http.StatusOK, gin.H{"success": true})
}
