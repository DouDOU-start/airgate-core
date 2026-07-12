/**
 * 登录页左侧「柔和氛围极光」—— 纯 CSS，无粒子无角色。
 * 几团克莱因蓝/靛蓝渐变光雾极缓慢漂移呼吸，叠一层极淡噪点增质感。
 * active（表单聚焦时）仅极轻微提亮，克制不喧宾夺主。
 */
export function AmbientAurora({ active, className }: { active: boolean; className?: string }) {
  return (
    <div className={`ag-aurora${active ? ' is-active' : ''}${className ? ` ${className}` : ''}`} aria-hidden>
      <span className="ag-aurora-blob ag-aurora-blob--1" />
      <span className="ag-aurora-blob ag-aurora-blob--2" />
      <span className="ag-aurora-blob ag-aurora-blob--3" />
      <span className="ag-aurora-grain" />
    </div>
  );
}
