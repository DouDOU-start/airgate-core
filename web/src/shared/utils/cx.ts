/** 拼接 className：过滤 falsy 值后以空格连接（组件层共用小助手） */
export function cx(...classes: Array<string | false | null | undefined>) {
  return classes.filter(Boolean).join(' ');
}
