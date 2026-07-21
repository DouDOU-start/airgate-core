package handler

import (
	appriskcontrol "github.com/DouDOU-start/airgate-core/internal/app/riskcontrol"
	"github.com/DouDOU-start/airgate-core/internal/moderation"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toRiskControlConfigResp(view appriskcontrol.ConfigView) dto.RiskControlConfigResp {
	cfg := view.Config
	return dto.RiskControlConfigResp{
		RiskControlEnabled:   view.RiskControlEnabled,
		Enabled:              cfg.Enabled,
		Mode:                 cfg.Mode,
		BaseURL:              cfg.BaseURL,
		Model:                cfg.Model,
		APIKeyCount:          view.APIKeyCount,
		APIKeyMasks:          view.APIKeyMasks,
		APIKeyStatuses:       view.APIKeyStatuses,
		TimeoutMS:            cfg.TimeoutMS,
		SampleRate:           cfg.SampleRate,
		AllGroups:            cfg.AllGroups,
		GroupIDs:             cfg.GroupIDs,
		RecordNonHits:        cfg.RecordNonHits,
		Thresholds:           cfg.Thresholds,
		Categories:           moderation.Categories(),
		WorkerCount:          cfg.WorkerCount,
		QueueSize:            cfg.QueueSize,
		BlockStatus:          cfg.BlockStatus,
		BlockMessage:         cfg.BlockMessage,
		EmailOnHit:           cfg.EmailOnHit,
		AutoBanEnabled:       cfg.AutoBanEnabled,
		BanThreshold:         cfg.BanThreshold,
		ViolationWindowHours: cfg.ViolationWindowHours,
		RetryCount:           cfg.RetryCount,
		HitRetentionDays:     cfg.HitRetentionDays,
		NonHitRetentionDays:  cfg.NonHitRetentionDays,
		PreHashCheckEnabled:  cfg.PreHashCheckEnabled,
		BlockedKeywords:      cfg.BlockedKeywords,
		KeywordBlockingMode:  cfg.KeywordBlockingMode,
		ModelFilter:          cfg.ModelFilter,
	}
}

func toRiskControlUpdateInput(req dto.UpdateRiskControlConfigReq) appriskcontrol.UpdateConfigInput {
	return appriskcontrol.UpdateConfigInput{
		RiskControlEnabled:   req.RiskControlEnabled,
		Enabled:              req.Enabled,
		Mode:                 req.Mode,
		BaseURL:              req.BaseURL,
		Model:                req.Model,
		APIKeys:              req.APIKeys,
		APIKeysMode:          req.APIKeysMode,
		DeleteAPIKeyHashes:   req.DeleteAPIKeyHashes,
		ClearAPIKeys:         req.ClearAPIKeys,
		TimeoutMS:            req.TimeoutMS,
		SampleRate:           req.SampleRate,
		AllGroups:            req.AllGroups,
		GroupIDs:             req.GroupIDs,
		RecordNonHits:        req.RecordNonHits,
		Thresholds:           req.Thresholds,
		WorkerCount:          req.WorkerCount,
		QueueSize:            req.QueueSize,
		BlockStatus:          req.BlockStatus,
		BlockMessage:         req.BlockMessage,
		EmailOnHit:           req.EmailOnHit,
		AutoBanEnabled:       req.AutoBanEnabled,
		BanThreshold:         req.BanThreshold,
		ViolationWindowHours: req.ViolationWindowHours,
		RetryCount:           req.RetryCount,
		HitRetentionDays:     req.HitRetentionDays,
		NonHitRetentionDays:  req.NonHitRetentionDays,
		PreHashCheckEnabled:  req.PreHashCheckEnabled,
		BlockedKeywords:      req.BlockedKeywords,
		KeywordBlockingMode:  req.KeywordBlockingMode,
		ModelFilter:          req.ModelFilter,
	}
}
