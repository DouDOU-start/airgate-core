package server

import (
	"context"
	"time"

	appchannel "github.com/DouDOU-start/airgate-core/internal/app/channel"
	"github.com/DouDOU-start/airgate-core/internal/infra/store"
	"github.com/DouDOU-start/airgate-core/internal/probe"
)

// probeStoreAdapter 将 ChannelStore 适配为 probe.Store。
type probeStoreAdapter struct {
	store *store.ChannelStore
}

func (a *probeStoreAdapter) ListProbeTargets(ctx context.Context) ([]probe.KeyHealthState, error) {
	snapshots, err := a.store.ListProbeEnabledKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]probe.KeyHealthState, len(snapshots))
	for i, s := range snapshots {
		out[i] = probe.KeyHealthState{
			KeyID:               s.KeyID,
			HealthStatus:        probe.HealthStatus(s.HealthStatus),
			ConsecutiveFailures: s.ConsecutiveFailures,
			ConsecutiveSuccesses: s.ConsecutiveSuccesses,
			LastProbeAt:         s.LastProbeAt,
		}
	}
	return out, nil
}

func (a *probeStoreAdapter) UpdateHealthState(ctx context.Context, keyID int, health string, failures, successes int) error {
	return a.store.UpdateKeyHealthState(ctx, keyID, health, failures, successes)
}

func (a *probeStoreAdapter) UpdateProbeTime(ctx context.Context, keyID int, at time.Time) error {
	return a.store.UpdateKeyProbeTime(ctx, keyID, at)
}

// probeTesterAdapter 将 channel.Service 适配为 probe.Tester。
type probeTesterAdapter struct {
	svc *appchannel.Service
}

func (a *probeTesterAdapter) TestKey(ctx context.Context, keyID int) error {
	return a.svc.TestKeyForProbe(ctx, keyID)
}

// probeBalanceStoreAdapter 将 ChannelStore 适配为 probe.BalanceStore。
type probeBalanceStoreAdapter struct {
	store *store.ChannelStore
}

func (a *probeBalanceStoreAdapter) ListBalanceSyncTargets(ctx context.Context, staleBefore time.Time) ([]int, error) {
	return a.store.ListBalanceSyncTargets(ctx, staleBefore)
}

// probeBalanceSyncerAdapter 将 channel.Service 适配为 probe.BalanceSyncer。
type probeBalanceSyncerAdapter struct {
	svc *appchannel.Service
}

func (a *probeBalanceSyncerAdapter) SyncBalance(ctx context.Context, keyID int) error {
	return a.svc.SyncBalanceForProbe(ctx, keyID)
}

// probeBillingProberAdapter 将 channel.Service 适配为 probe.BillingProber。
type probeBillingProberAdapter struct {
	svc *appchannel.Service
}

func (a *probeBillingProberAdapter) ProbeBilling(ctx context.Context, keyID int) (*probe.BillingProbeResult, error) {
	rate, err := a.svc.ProbeKeyBilling(ctx, keyID)
	if err != nil {
		return nil, err
	}
	return &probe.BillingProbeResult{RateMultiplier: rate}, nil
}

// probeBillingStoreAdapter 将 ChannelStore 适配为 probe.BillingProbeStore。
type probeBillingStoreAdapter struct {
	store  *store.ChannelStore
	secret string
}

func (a *probeBillingStoreAdapter) ListBillingProbeTargets(ctx context.Context) ([]probe.BillingProbeTarget, error) {
	targets, err := a.store.ListUpstreamRateTargets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]probe.BillingProbeTarget, 0, len(targets))
	for _, t := range targets {
		out = append(out, probe.BillingProbeTarget{
			KeyID:   t.KeyID,
			BaseURL: t.BaseURL,
			APIKey:  t.APIKeyCipher,
		})
	}
	return out, nil
}

func (a *probeBillingStoreAdapter) UpdateUpstreamRate(ctx context.Context, keyID int, rate float64, at time.Time) error {
	return a.store.UpdateUpstreamRate(ctx, keyID, rate, at)
}
