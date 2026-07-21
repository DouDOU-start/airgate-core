package moderation

// evaluateScores 阈值判定：已知类别任一 score >= threshold 即 flagged；
// highest 取全部返回类别（含未配置阈值的未知类别）里分数最高者。
func evaluateScores(scores map[string]float64, thresholds map[string]float64) (flagged bool, highestCategory string, highestScore float64) {
	for _, category := range categoryOrder {
		score := scores[category]
		if score > highestScore || highestCategory == "" {
			highestScore = score
			highestCategory = category
		}
		if score >= thresholds[category] {
			flagged = true
		}
	}
	for category, score := range scores {
		if score > highestScore || highestCategory == "" {
			highestScore = score
			highestCategory = category
		}
	}
	return flagged, highestCategory, highestScore
}
