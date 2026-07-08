package dto

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
	TimeMixin
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
}

// ImportModelPriceItem 批量导入条目。
type ImportModelPriceItem struct {
	Model                string                 `json:"model" binding:"required"`
	InputPrice           float64                `json:"input_price" binding:"omitempty,gte=0"`
	OutputPrice          float64                `json:"output_price" binding:"omitempty,gte=0"`
	CachedInputPrice     float64                `json:"cached_input_price" binding:"omitempty,gte=0"`
	CacheCreationPrice   float64                `json:"cache_creation_price" binding:"omitempty,gte=0"`
	CacheCreation1hPrice float64                `json:"cache_creation_1h_price" binding:"omitempty,gte=0"`
	PerRequestPrice      float64                `json:"per_request_price" binding:"omitempty,gte=0"`
	PricingExtra         map[string]interface{} `json:"pricing_extra"`
}

// ImportModelPricesReq 批量导入请求（按 model 名 upsert）。
type ImportModelPricesReq struct {
	Items []ImportModelPriceItem `json:"items" binding:"required,min=1,dive"`
}

// ImportModelPricesResp 批量导入结果。
type ImportModelPricesResp struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
}
