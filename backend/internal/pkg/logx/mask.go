package logx

import "strings"

// MaskEmail 输出脱敏后的邮箱字符串：保留 local part 前 3 个字符与完整域名。
//
//	joineroz749@gmail.com → joi***@gmail.com
//	a@b.com               → a***@b.com   （不足 3 字符也补 ***）
//	没有 @ 的输入直接返回 ***
//
// 邮箱脱敏的唯一实现：infra/store.EmailHash 与 app 层日志均委托此函数，
// 避免多处独立实现漂移。
func MaskEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return "***"
	}
	local := email[:at]
	domain := email[at:]
	prefix := local
	if len(local) > 3 {
		prefix = local[:3]
	}
	return prefix + "***" + domain
}
