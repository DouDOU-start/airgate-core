#!/usr/bin/env bash
# 校验 README.md 与 README_EN.md 结构同步。
# 二者为中英镜像（顶部注释已声明），正文语言不同，故只比对结构而非文本：
#   1) 标题层级序列（# 的数量/层级/顺序须一致，标题文本可不同）
#   2) 代码块栅栏数量（``` 行数须相等）
# 任一不一致即非零退出并打印 diff，接入 make ci / pre-commit 防镜像漂移。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ZH="$ROOT/README.md"
EN="$ROOT/README_EN.md"

for f in "$ZH" "$EN"; do
  if [[ ! -f "$f" ]]; then
    echo "❌ 缺少 $(basename "$f")"
    exit 1
  fi
done

# 标题层级序列：仅取每个标题的 # 个数，忽略文本，规避中英差异。
levels() { grep -oE '^#{1,6}' "$1" | awk '{ print length }'; }
# 代码块栅栏数量（``` 起始行）。
fences() { grep -cE '^```' "$1" || true; }

if [[ "$(levels "$ZH")" != "$(levels "$EN")" ]]; then
  echo "❌ README 标题结构不一致（README.md ↔ README_EN.md），二者须标题数量/层级/顺序一致（仅文本可不同）。"
  echo "标题数：README.md=$(levels "$ZH" | grep -c .)  README_EN.md=$(levels "$EN" | grep -c .)"
  echo "── 标题层级序列 diff（< README.md / > README_EN.md，数字为 # 层级）──"
  diff <(levels "$ZH") <(levels "$EN") || true
  echo "提示：定位到对应序号的标题修正镜像即可。"
  exit 1
fi

zh_fences="$(fences "$ZH")"
en_fences="$(fences "$EN")"
if [[ "$zh_fences" != "$en_fences" ]]; then
  echo "❌ README 代码块数量不一致：README.md=$zh_fences README_EN.md=$en_fences"
  exit 1
fi

echo "✅ README.md 与 README_EN.md 结构同步（标题 $(levels "$ZH" | grep -c .) 个 / 代码块 $((zh_fences / 2)) 段）"
