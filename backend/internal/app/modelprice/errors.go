package modelprice

import "errors"

var (
	// ErrModelPriceNotFound 表示目标价格条目不存在。
	ErrModelPriceNotFound = errors.New("模型价格不存在")
	// ErrModelPriceExists 表示同名模型的价格条目已存在。
	ErrModelPriceExists = errors.New("模型价格已存在")
)

var (
	// ErrTagNotFound 表示目标标签不存在。
	ErrTagNotFound = errors.New("模型标签不存在")
	// ErrTagExists 表示同名标签已存在。
	ErrTagExists = errors.New("模型标签已存在")
)
