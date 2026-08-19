package account

import "testing"

func TestCursor默认探测模型偏向轻量flash(t *testing.T) {
	got := pickDefaultTestModel("cursor", "", "")
	if got == "" {
		t.Fatal("cursor 无默认测试模型，目录可能未接入")
	}
	t.Logf("cursor 默认测试模型: %s", got)
}
