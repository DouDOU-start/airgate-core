package dto

// ListModelPricesReq 价目表列表查询参数（分页/关键词 + 可选标签过滤）。
type ListModelPricesReq struct {
	PageReq
	TagID *int64 `form:"tag_id" binding:"omitempty,gte=1"`
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
}

// ModelTagResp 模型标签响应；model_count 为引用该标签的模型数。
type ModelTagResp struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	ModelCount int64  `json:"model_count"`
}

// CreateModelTagReq 新建模型标签请求。
type CreateModelTagReq struct {
	Name string `json:"name" binding:"required,max=64"`
}

// UpdateModelTagReq 重命名模型标签请求。
type UpdateModelTagReq struct {
	Name string `json:"name" binding:"required,max=64"`
}
