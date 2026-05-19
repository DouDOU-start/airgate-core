import type { CSSProperties } from 'react';
import { Modal } from '@heroui/react';

// HeroUI 的 Modal/AlertDialog 受控模式下不渲染 trigger 子项，但内部仍包了
// react-aria 的 DialogTrigger，后者会渲染一个 PressResponder。PressResponder
// 在 useEffect 里检查是否有 usePress 子组件注册过自己，未注册则打印
// "PressResponder was rendered without a pressable child"。
//
// 关键约束：注册要透过同一份 PressResponderContext 才生效。Vite 会把
// react-aria-components 和 @heroui/react 拆成不同的优化 chunk，各自带一份
// PressResponderContext，从外部直接 import Pressable 注册不到 HeroUI 的
// PressResponder。所以这里复用 HeroUI 自己的 Modal.Trigger —— 它内部用
// HeroUI 同一 chunk 的 Pressable，能正确注册。AlertDialog 容器下也能用，
// 因为底层 DialogTrigger 是同一个 react-aria 模块。
const hiddenStyle: CSSProperties = {
  position: 'fixed',
  width: 1,
  height: 1,
  padding: 0,
  margin: -1,
  border: 0,
  opacity: 0,
  pointerEvents: 'none',
  overflow: 'hidden',
  clip: 'rect(0 0 0 0)',
};

export function DialogTriggerShim() {
  return <Modal.Trigger aria-hidden="true" tabIndex={-1} style={hiddenStyle} />;
}
