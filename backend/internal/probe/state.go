package probe

// Action 状态机判定后需要执行的动作。
type Action int

const (
	ActionNone         Action = iota
	ActionUpdateHealth        // 仅更新健康状态（持久化 + 内存）
	ActionSuspend             // 暂停 key（status→disabled_auto + health→suspended）
	ActionRecover             // 恢复 key（status→enabled + health→healthy）
)

// Transition 状态转移结果。
type Transition struct {
	NewHealth HealthStatus
	Failures  int
	Successes int
	Action    Action
}

// OnSuccess 记录一次成功（转发成功 / 探针成功）后的状态转移。
func OnSuccess(cur HealthStatus, failures, successes int) Transition {
	switch cur {
	case HealthHealthy:
		return Transition{
			NewHealth: HealthHealthy,
			Failures:  0,
			Successes: 0,
			Action:    ActionNone,
		}

	case HealthDegraded:
		return Transition{
			NewHealth: HealthHealthy,
			Failures:  0,
			Successes: 0,
			Action:    ActionUpdateHealth,
		}

	case HealthSuspended:
		// 探针首次成功 → 进入恢复观察期
		return Transition{
			NewHealth: HealthRecovering,
			Failures:  0,
			Successes: 1,
			Action:    ActionUpdateHealth,
		}

	case HealthRecovering:
		next := successes + 1
		if next >= DefaultRecoverThreshold {
			return Transition{
				NewHealth: HealthHealthy,
				Failures:  0,
				Successes: 0,
				Action:    ActionRecover,
			}
		}
		return Transition{
			NewHealth: HealthRecovering,
			Failures:  0,
			Successes: next,
			Action:    ActionUpdateHealth,
		}

	default:
		return Transition{NewHealth: cur, Action: ActionNone}
	}
}

// OnFailure 记录一次瞬态失败（5xx/网络/限流）后的状态转移。
func OnFailure(cur HealthStatus, failures, successes int) Transition {
	switch cur {
	case HealthHealthy:
		next := failures + 1
		if next >= DefaultSuspendThreshold {
			return Transition{
				NewHealth: HealthSuspended,
				Failures:  next,
				Successes: 0,
				Action:    ActionSuspend,
			}
		}
		if next >= DefaultDegradeThreshold {
			return Transition{
				NewHealth: HealthDegraded,
				Failures:  next,
				Successes: 0,
				Action:    ActionUpdateHealth,
			}
		}
		return Transition{
			NewHealth: HealthHealthy,
			Failures:  next,
			Successes: 0,
			Action:    ActionUpdateHealth,
		}

	case HealthDegraded:
		next := failures + 1
		if next >= DefaultSuspendThreshold {
			return Transition{
				NewHealth: HealthSuspended,
				Failures:  next,
				Successes: 0,
				Action:    ActionSuspend,
			}
		}
		return Transition{
			NewHealth: HealthDegraded,
			Failures:  next,
			Successes: 0,
			Action:    ActionUpdateHealth,
		}

	case HealthSuspended:
		return Transition{
			NewHealth: HealthSuspended,
			Failures:  failures + 1,
			Successes: 0,
			Action:    ActionUpdateHealth,
		}

	case HealthRecovering:
		// 恢复期失败 → 回退 suspended
		return Transition{
			NewHealth: HealthSuspended,
			Failures:  failures + 1,
			Successes: 0,
			Action:    ActionUpdateHealth,
		}

	default:
		return Transition{NewHealth: cur, Action: ActionNone}
	}
}

// OnAuthFailure 记录一次鉴权失败（401/403）后的状态转移：直接暂停。
func OnAuthFailure(cur HealthStatus) Transition {
	return Transition{
		NewHealth: HealthSuspended,
		Failures:  DefaultSuspendThreshold,
		Successes: 0,
		Action:    ActionSuspend,
	}
}
