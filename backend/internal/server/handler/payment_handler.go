package handler

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	apppayment "github.com/DouDOU-start/airgate-core/internal/app/payment"
	"github.com/DouDOU-start/airgate-core/internal/app/payment/provider"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
	"github.com/DouDOU-start/airgate-core/internal/server/response"
)

// PaymentHandler 支付域 HTTP 处理器（用户充值 + 管理端配置 + 支付回调）。
type PaymentHandler struct {
	service *apppayment.Service
}

// NewPaymentHandler 创建支付处理器。
func NewPaymentHandler(service *apppayment.Service) *PaymentHandler {
	return &PaymentHandler{service: service}
}

func toPaymentOrderResp(o apppayment.Order) dto.PaymentOrderResp {
	return dto.PaymentOrderResp{
		OutTradeNo:    o.OutTradeNo,
		UserID:        o.UserID,
		UserEmail:     o.UserEmail,
		Method:        o.Method,
		ProviderID:    o.ProviderID,
		Amount:        o.Amount,
		Status:        o.Status,
		Subject:       o.Subject,
		PaymentURL:    o.PaymentURL,
		QRCodeContent: o.QRCodeContent,
		PaidAt:        o.PaidAt,
		ExpiresAt:     o.ExpiresAt,
		CreatedAt:     o.CreatedAt,
		UpdatedAt:     o.UpdatedAt,
	}
}

// ===================== 用户端 =====================

// ListMethods 可用支付方式（未配置时返回空列表 + configured=false，前端据此提示）。
func (h *PaymentHandler) ListMethods(c *gin.Context) {
	result := h.service.AvailableMethods(c.Request.Context())
	response.Success(c, dto.PaymentMethodsResp{
		Methods:    result.Methods,
		Configured: result.Configured,
	})
}

// CreateOrder 用户下单。
func (h *PaymentHandler) CreateOrder(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}
	var req dto.CreatePaymentOrderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}
	order, err := h.service.CreateOrder(c.Request.Context(), apppayment.CreateOrderInput{
		UserID:   userID,
		Amount:   req.Amount,
		Method:   req.Method,
		Subject:  req.Subject,
		ClientIP: c.ClientIP(),
	})
	if err != nil {
		switch {
		case errors.Is(err, apppayment.ErrInvalidAmount),
			errors.Is(err, apppayment.ErrDailyLimit),
			errors.Is(err, apppayment.ErrNoProvider),
			errors.Is(err, apppayment.ErrNotConfigured):
			response.BadRequest(c, err.Error())
		default:
			response.Error(c, http.StatusBadGateway, http.StatusBadGateway, "下单失败: "+err.Error())
		}
		return
	}
	response.Success(c, toPaymentOrderResp(order))
}

// ListUserOrders 用户充值记录。
func (h *PaymentHandler) ListUserOrders(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	orders, total, err := h.service.ListUserOrders(c.Request.Context(), apppayment.UserOrderFilter{
		UserID:   userID,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		response.InternalError(c, "查询失败")
		return
	}
	list := make([]dto.PaymentOrderResp, 0, len(orders))
	for _, o := range orders {
		list = append(list, toPaymentOrderResp(o))
	}
	response.Success(c, dto.PaymentOrderListResp{List: list, Total: total})
}

// GetUserOrder 用户查单（续付/支付状态轮询）。
func (h *PaymentHandler) GetUserOrder(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		response.Unauthorized(c, "用户未认证")
		return
	}
	order, err := h.service.GetUserOrder(c.Request.Context(), userID, c.Param("out_trade_no"))
	if err != nil {
		response.NotFound(c, "订单不存在")
		return
	}
	response.Success(c, toPaymentOrderResp(order))
}

// ===================== 管理端 =====================

// AdminListOrders 管理端订单列表 + 统计。
func (h *PaymentHandler) AdminListOrders(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	list, total, stats, err := h.service.AdminListOrders(c.Request.Context(), apppayment.AdminOrderFilter{
		Page:     page,
		PageSize: pageSize,
		Email:    strings.TrimSpace(c.Query("email")),
		Status:   c.Query("status"),
	})
	if err != nil {
		response.InternalError(c, "查询失败")
		return
	}
	orders := make([]dto.PaymentOrderResp, 0, len(list))
	for _, o := range list {
		orders = append(orders, toPaymentOrderResp(o))
	}
	response.Success(c, dto.AdminPaymentOrdersResp{
		List:  orders,
		Total: total,
		Stats: dto.PaymentOrderStatsResp{
			Total:       stats.Total,
			Paid:        stats.Paid,
			Pending:     stats.Pending,
			Expired:     stats.Expired,
			TotalAmount: stats.TotalAmount,
			TodayAmount: stats.TodayAmount,
		},
	})
}

// AdminListProviders 服务商实例列表 + 协议类型元信息。
func (h *PaymentHandler) AdminListProviders(c *gin.Context) {
	views, kinds, err := h.service.AdminListProviders(c.Request.Context())
	if err != nil {
		response.InternalError(c, "查询失败")
		return
	}
	providers := make([]dto.PaymentProviderResp, 0, len(views))
	for _, v := range views {
		providers = append(providers, dto.PaymentProviderResp{
			ID:               v.ProviderKey,
			Kind:             v.Kind,
			Name:             v.Name,
			Enabled:          v.Enabled,
			Config:           v.Config,
			SupportedMethods: emptyIfNilStrings(v.SupportedMethods),
			IsRunning:        v.IsRunning,
			SensitiveKeys:    emptyIfNilStrings(v.SensitiveKeys),
		})
	}
	response.Success(c, dto.PaymentProvidersResp{Providers: providers, Kinds: kinds})
}

// AdminUpsertProvider 新增/编辑服务商实例（保存即热加载）。
func (h *PaymentHandler) AdminUpsertProvider(c *gin.Context) {
	var req dto.UpsertPaymentProviderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}
	id, err := h.service.AdminUpsertProvider(c.Request.Context(), apppayment.UpsertProviderInput{
		ProviderKey: req.ID,
		OriginalKey: req.OriginalID,
		Kind:        req.Kind,
		Enabled:     req.Enabled,
		Config:      req.Config,
	})
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, dto.UpsertPaymentProviderResp{ID: id})
}

// AdminDeleteProvider 删除服务商实例。
func (h *PaymentHandler) AdminDeleteProvider(c *gin.Context) {
	if err := h.service.AdminDeleteProvider(c.Request.Context(), c.Param("id")); err != nil {
		if errors.Is(err, apppayment.ErrProviderNotFound) {
			response.NotFound(c, err.Error())
			return
		}
		response.InternalError(c, "删除失败")
		return
	}
	response.Success(c, gin.H{"ok": true})
}

// ===================== 支付回调（公开端点，验签在 provider 内完成） =====================

// callbackBodyLimit 回调体上限（支付平台通知都很小，防滥用）。
const callbackBodyLimit = 1 << 20

// HandleCallback 支付平台异步通知入口。
// 同时兼容 form-urlencoded（易支付/支付宝，GET 参数也并入）与
// 原始 JSON body + header 签名（微信 V3 / easypay）两类协议。
func (h *PaymentHandler) HandleCallback(c *gin.Context) {
	providerID := c.Param("provider_id")

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, callbackBodyLimit))
	if err != nil {
		c.String(http.StatusBadRequest, "fail")
		return
	}
	form := url.Values{}
	for k, vs := range c.Request.URL.Query() {
		form[k] = vs
	}
	if strings.Contains(c.GetHeader("Content-Type"), "application/x-www-form-urlencoded") {
		if parsed, err := url.ParseQuery(string(body)); err == nil {
			for k, vs := range parsed {
				form[k] = vs
			}
		}
	}

	res, err := h.service.HandleCallback(c.Request.Context(), providerID, provider.CallbackRequest{
		Form:    form,
		Body:    body,
		Headers: c.Request.Header,
	})
	if err != nil {
		// 验签失败/入账失败统一回 fail，让平台按自身策略重试
		c.String(http.StatusBadRequest, "fail")
		return
	}

	reply := res.Reply
	if reply == "" {
		reply = "success"
	}
	switch res.ReplyType {
	case "json":
		c.Data(http.StatusOK, "application/json", []byte(reply))
	case "xml":
		c.Data(http.StatusOK, "text/xml; charset=utf-8", []byte(reply))
	default:
		c.String(http.StatusOK, "%s", reply)
	}
}
