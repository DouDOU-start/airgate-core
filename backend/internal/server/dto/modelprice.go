package dto

// ListModelPricesReq 价目表列表查询参数（分页/关键词 + 可选标签/启用状态/广场可见过滤）。
type ListModelPricesReq struct {
	PageReq
	TagID *int64 `form:"tag_id" binding:"omitempty,gte=1"`
	// Enabled 按模型启用状态过滤（省略 = 不过滤）。
	Enabled *bool `form:"enabled"`
	// MarketVisible 按广场可见状态过滤（省略 = 不过滤）。
	MarketVisible *bool `form:"market_visible"`
}

// SyncModelPricesReq 指定要从远端同步的模型，模型列表不能为空。
type SyncModelPricesReq struct {
	Models []string `json:"models" binding:"required,min=1,max=500"`
}

// BulkUpdateModelPricesReq 模型价格批量操作请求。
type BulkUpdateModelPricesReq struct {
	IDs    []int  `json:"ids" binding:"required,min=1"`
	Action string `json:"action" binding:"required,oneof=enable disable delete"`
}

// BulkUpdateModelPricesResp 模型价格批量操作响应。
type BulkUpdateModelPricesResp struct {
	Success    int                    `json:"success"`
	Failed     int                    `json:"failed"`
	SuccessIDs []int                  `json:"success_ids"`
	FailedIDs  []int                  `json:"failed_ids"`
	Results    []BulkModelPriceResult `json:"results"`
}

// BulkModelPriceResult 单个模型价格批量操作结果。
type BulkModelPriceResult struct {
	ID      int    `json:"id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// ModelPriceResp 模型价格响应。价格单位 USD / 1M tokens；per_request_price 为 USD / 次。
type ModelPriceResp struct {
	ID                   int64                  `json:"id"`
	Model                string                 `json:"model"`
	InputPrice           float64                `json:"input_price"`
	OutputPrice          float64                `json:"output_price"`
	CachedInputPrice     float64                `json:"cached_input_price"`
	CacheCreationPrice   float64                `json:"cache_creation_price"`
	CacheCreation1hPrice float64                `json:"cache_creation_1h_price"`
	PerRequestPrice      float64                `json:"per_request_price"`
	PricingExtra         map[string]interface{} `json:"pricing_extra,omitempty"`
	// Tag 模型标签（家族归类，可空）。
	Tag *ModelTagRef `json:"tag,omitempty"`
	// Enabled 是否参与平台计费目录与模型路由。
	Enabled bool `json:"enabled"`
	// MarketVisible 是否在模型广场（未登录可见的公开价目页）展示。
	MarketVisible bool `json:"market_visible"`
	TimeMixin
}

// ModelTagRef 模型上挂载的标签引用。
type ModelTagRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// CreateModelPriceReq 创建模型价格请求。
type CreateModelPriceReq struct {
	Model                string                 `json:"model" binding:"required"`
	InputPrice           float64                `json:"input_price" binding:"omitempty,gte=0"`
	OutputPrice          float64                `json:"output_price" binding:"omitempty,gte=0"`
	CachedInputPrice     float64                `json:"cached_input_price" binding:"omitempty,gte=0"`
	CacheCreationPrice   float64                `json:"cache_creation_price" binding:"omitempty,gte=0"`
	CacheCreation1hPrice float64                `json:"cache_creation_1h_price" binding:"omitempty,gte=0"`
	PerRequestPrice      float64                `json:"per_request_price" binding:"omitempty,gte=0"`
	PricingExtra         map[string]interface{} `json:"pricing_extra"`
	// TagID 模型标签 ID（省略或 0 = 不挂标签）。
	TagID *int64 `json:"tag_id" binding:"omitempty,gte=0"`
	// Enabled 是否启用；省略时默认 true。
	Enabled *bool `json:"enabled"`
	// MarketVisible 是否在模型广场展示；省略时默认 true。
	MarketVisible *bool `json:"market_visible"`
}

// UpdateModelPriceReq 更新模型价格请求（partial，指针字段）。
// PricingExtra 为整体替换语义：提供时整块写入（空对象清空扩展）。
type UpdateModelPriceReq struct {
	Model                *string                `json:"model"`
	InputPrice           *float64               `json:"input_price" binding:"omitempty,gte=0"`
	OutputPrice          *float64               `json:"output_price" binding:"omitempty,gte=0"`
	CachedInputPrice     *float64               `json:"cached_input_price" binding:"omitempty,gte=0"`
	CacheCreationPrice   *float64               `json:"cache_creation_price" binding:"omitempty,gte=0"`
	CacheCreation1hPrice *float64               `json:"cache_creation_1h_price" binding:"omitempty,gte=0"`
	PerRequestPrice      *float64               `json:"per_request_price" binding:"omitempty,gte=0"`
	PricingExtra         map[string]interface{} `json:"pricing_extra"`
	// TagID 三态：省略 = 不改；0 = 清空标签；正数 = 设为该标签。
	TagID *int64 `json:"tag_id" binding:"omitempty,gte=0"`
	// Enabled 省略 = 不改；否则设为该值。
	Enabled *bool `json:"enabled"`
	// MarketVisible 省略 = 不改；否则设为该值。
	MarketVisible *bool `json:"market_visible"`
}

// ModelTagResp 模型标签响应；model_count 为引用该标签的模型数。
type ModelTagResp struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	ModelCount int64  `json:"model_count"`
}

// ModelMarketItemResp 模型广场公开展示条目：价格字段子集 + 服务档倍率/长上下文阶梯
// （计价维度，公开展示；pricing_extra 里其余内部键如 video 不对外暴露）。
type ModelMarketItemResp struct {
	Model                string       `json:"model"`
	InputPrice           float64      `json:"input_price"`
	OutputPrice          float64      `json:"output_price"`
	CachedInputPrice     float64      `json:"cached_input_price"`
	CacheCreationPrice   float64      `json:"cache_creation_price"`
	CacheCreation1hPrice float64      `json:"cache_creation_1h_price"`
	PerRequestPrice      float64      `json:"per_request_price"`
	Tag                  *ModelTagRef `json:"tag,omitempty"`
	// ServiceTiers 服务档倍率（如 priority=2.0、flex=0.5），未配置时省略。
	ServiceTiers map[string]float64 `json:"service_tiers,omitempty"`
	// LongContext 长上下文阶梯，未配置时省略。
	LongContext *ModelMarketLongContext `json:"long_context,omitempty"`
	// ImageSizePrices 图像分辨率价表（USD/张，键 "quality:size" 或裸 "size"），未配置时省略。
	ImageSizePrices map[string]float64 `json:"image_size_prices,omitempty"`
	// VideoPerSecond 视频未命中分辨率表时的基础秒价，未配置时省略。
	VideoPerSecond float64 `json:"video_per_second,omitempty"`
	// VideoResolutionPrices 视频分辨率秒价表（USD/秒，键如 480p/720p/1080p），未配置时省略。
	VideoResolutionPrices map[string]float64 `json:"video_resolution_prices,omitempty"`
}

// ModelMarketLongContext 长上下文阶梯：完整 prompt 超过阈值时各维度单价按对应倍率放大。
type ModelMarketLongContext struct {
	ThresholdTokens  int     `json:"threshold_tokens"`
	InputMultiplier  float64 `json:"input_multiplier"`
	OutputMultiplier float64 `json:"output_multiplier"`
	CachedMultiplier float64 `json:"cached_multiplier"`
}

// ModelMarketMultiplierRange 非专属分组的倍率区间（专属谈价分组不参与，见后端 service 注释）。
type ModelMarketMultiplierRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// ModelMarketResp 模型广场公开响应；Multiplier 为空表示无可展示的倍率区间，前端只展示价格。
type ModelMarketResp struct {
	List       []ModelMarketItemResp       `json:"list"`
	Total      int64                       `json:"total"`
	Page       int                         `json:"page"`
	PageSize   int                         `json:"page_size"`
	Multiplier *ModelMarketMultiplierRange `json:"multiplier,omitempty"`
}

// CreateModelTagReq 新建模型标签请求。
type CreateModelTagReq struct {
	Name string `json:"name" binding:"required,max=64"`
}

// UpdateModelTagReq 重命名模型标签请求。
type UpdateModelTagReq struct {
	Name string `json:"name" binding:"required,max=64"`
}
