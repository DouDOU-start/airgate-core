import QRCode from 'qrcode';

/**
 * 邀请海报卡片渲染：深色渐变背景 + 品牌区 + 白底二维码面板 + 邀请码胶囊，
 * 输出 PNG dataURL，弹窗预览与保存的是同一张图（所见即所得）。
 */
export interface InviteCardOptions {
  shareLink: string;
  inviteCode: string;
  siteName: string;
  tagline: string;
  codeLabel: string;
  /** 管理员配置的邀请描述文案，绘制在标语下方（最多两行，超长截断） */
  description?: string;
  /** 品牌 logo 地址（自定义 site_logo 或内置默认 logo），绘制在二维码中央；加载失败回退首字母徽章 */
  logoUrl?: string;
}

// 画布尺寸（3:4 竖版，1080 宽保证保存后清晰）
const CARD_W = 1080;
const CARD_H = 1400;

const FONT_SANS = '-apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", "Helvetica Neue", sans-serif';
const FONT_MONO = 'ui-monospace, SFMono-Regular, Consolas, "Liberation Mono", monospace';

// 品牌蓝（与前端主题主色一致的具体色值；canvas 无法取 CSS 变量的 oklch 原值）
const BRAND_BLUE = '#4c8dff';
const BRAND_BLUE_DEEP = '#2f6bef';
const CARD_NAVY = '#0b1220';

function roundRectPath(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

function loadImage(src: string, crossOrigin?: boolean): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    // 跨域 logo 需匿名加载，否则画布被污染后无法导出 PNG
    if (crossOrigin) img.crossOrigin = 'anonymous';
    img.onload = () => resolve(img);
    img.onerror = reject;
    img.src = src;
  });
}

// 按像素宽度折行（逐字符，兼容中英文混排），超出 maxLines 时末行截断加省略号。
function wrapLines(ctx: CanvasRenderingContext2D, text: string, maxWidth: number, maxLines: number): string[] {
  const lines: string[] = [];
  let current = '';
  let overflow = false;
  for (const ch of Array.from(text.replace(/\s+/g, ' ').trim())) {
    const candidate = current + ch;
    if (ctx.measureText(candidate).width > maxWidth && current !== '') {
      if (lines.length === maxLines - 1) {
        overflow = true;
        break;
      }
      lines.push(current);
      current = ch.trim();
    } else {
      current = candidate;
    }
  }
  if (current) lines.push(current);
  if (overflow) {
    let last = lines[lines.length - 1];
    while (last && ctx.measureText(`${last}…`).width > maxWidth) last = last.slice(0, -1);
    lines[lines.length - 1] = `${last}…`;
  }
  return lines;
}

export async function renderInviteCard(opts: InviteCardOptions): Promise<string> {
  const canvas = document.createElement('canvas');
  canvas.width = CARD_W;
  canvas.height = CARD_H;
  const ctx = canvas.getContext('2d');
  if (!ctx) throw new Error('canvas 2d context unavailable');

  // 背景：深海军蓝纵向渐变
  const bg = ctx.createLinearGradient(0, 0, 0, CARD_H);
  bg.addColorStop(0, '#0a101f');
  bg.addColorStop(0.55, '#0d1528');
  bg.addColorStop(1, '#111b33');
  ctx.fillStyle = bg;
  ctx.fillRect(0, 0, CARD_W, CARD_H);

  // 氛围光：右上品牌蓝 + 左下紫，低透明度
  const glowTop = ctx.createRadialGradient(CARD_W * 0.85, 60, 0, CARD_W * 0.85, 60, 620);
  glowTop.addColorStop(0, 'rgba(76,141,255,0.28)');
  glowTop.addColorStop(1, 'rgba(76,141,255,0)');
  ctx.fillStyle = glowTop;
  ctx.fillRect(0, 0, CARD_W, CARD_H);

  const glowBottom = ctx.createRadialGradient(CARD_W * 0.08, CARD_H * 0.98, 0, CARD_W * 0.08, CARD_H * 0.98, 700);
  glowBottom.addColorStop(0, 'rgba(139,92,246,0.20)');
  glowBottom.addColorStop(1, 'rgba(139,92,246,0)');
  ctx.fillStyle = glowBottom;
  ctx.fillRect(0, 0, CARD_W, CARD_H);

  // 细网格点阵：低调的科技感肌理
  ctx.fillStyle = 'rgba(255,255,255,0.045)';
  for (let gx = 60; gx < CARD_W; gx += 60) {
    for (let gy = 60; gy < CARD_H; gy += 60) {
      ctx.beginPath();
      ctx.arc(gx, gy, 1.6, 0, Math.PI * 2);
      ctx.fill();
    }
  }

  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';

  // 站名 + 标语（logo 移至二维码中央，顶部纯文字品牌区）
  ctx.fillStyle = '#f4f7ff';
  ctx.font = `700 68px ${FONT_SANS}`;
  ctx.fillText(opts.siteName, CARD_W / 2, 186);

  ctx.fillStyle = 'rgba(214,226,255,0.58)';
  ctx.font = `400 31px ${FONT_SANS}`;
  ctx.fillText(opts.tagline, CARD_W / 2, 252);

  // 邀请描述：管理员配置的推广文案，最多两行
  const description = opts.description?.trim() ?? '';
  if (description) {
    ctx.fillStyle = 'rgba(226,236,255,0.88)';
    ctx.font = `500 34px ${FONT_SANS}`;
    const descLines = wrapLines(ctx, description, 880, 2);
    const descLineHeight = 52;
    const descStartY = 336 - ((descLines.length - 1) * descLineHeight) / 2;
    descLines.forEach((line, i) => {
      ctx.fillText(line, CARD_W / 2, descStartY + i * descLineHeight);
    });
  }

  // 二维码白底面板（padding 兼作二维码静区）
  const panelSize = 600;
  const panelX = CARD_W / 2 - panelSize / 2;
  const panelY = 430;
  ctx.save();
  ctx.shadowColor = 'rgba(0,0,0,0.5)';
  ctx.shadowBlur = 60;
  ctx.shadowOffsetY = 20;
  roundRectPath(ctx, panelX, panelY, panelSize, panelSize, 44);
  ctx.fillStyle = '#ffffff';
  ctx.fill();
  ctx.restore();

  // 纠错级别 H（容忍约 30% 遮挡），为中央 logo 徽章预留冗余
  const qrSize = 512;
  const qrDataUrl = await QRCode.toDataURL(opts.shareLink, {
    width: qrSize,
    margin: 0,
    color: { dark: CARD_NAVY, light: '#ffffff' },
    errorCorrectionLevel: 'H',
  });
  const qrImg = await loadImage(qrDataUrl);
  ctx.drawImage(qrImg, CARD_W / 2 - qrSize / 2, panelY + (panelSize - qrSize) / 2, qrSize, qrSize);

  // 二维码中央品牌徽章：白底圆角衬板 + logo（遮挡约 6% 模块，远低于 H 级冗余）
  const badgeSize = 124;
  const badgeX = CARD_W / 2 - badgeSize / 2;
  const badgeY = panelY + panelSize / 2 - badgeSize / 2;
  roundRectPath(ctx, badgeX, badgeY, badgeSize, badgeSize, 28);
  ctx.fillStyle = '#ffffff';
  ctx.fill();

  let logoImg: HTMLImageElement | null = null;
  if (opts.logoUrl) {
    logoImg = await loadImage(opts.logoUrl, true).catch(() => null);
  }
  if (logoImg && logoImg.width > 0 && logoImg.height > 0) {
    // 按比例 contain 到徽章内，四周留白与二维码隔开
    const logoBox = 92;
    const scale = Math.min(logoBox / logoImg.width, logoBox / logoImg.height);
    const drawW = logoImg.width * scale;
    const drawH = logoImg.height * scale;
    ctx.drawImage(logoImg, CARD_W / 2 - drawW / 2, badgeY + (badgeSize - drawH) / 2, drawW, drawH);
  } else {
    // 无 logo 兜底：蓝色渐变圆角块 + 站名首字母
    const markSize = 92;
    const markX = CARD_W / 2 - markSize / 2;
    const markY = badgeY + (badgeSize - markSize) / 2;
    const markGradient = ctx.createLinearGradient(markX, markY, markX + markSize, markY + markSize);
    markGradient.addColorStop(0, BRAND_BLUE);
    markGradient.addColorStop(1, BRAND_BLUE_DEEP);
    roundRectPath(ctx, markX, markY, markSize, markSize, 22);
    ctx.fillStyle = markGradient;
    ctx.fill();
    ctx.fillStyle = '#ffffff';
    ctx.font = `700 48px ${FONT_SANS}`;
    ctx.fillText((opts.siteName[0] ?? 'A').toUpperCase(), CARD_W / 2, markY + markSize / 2 + 2);
  }

  // 邀请码胶囊：标签 + 等宽邀请码
  const codeText = opts.inviteCode;
  ctx.font = `600 38px ${FONT_MONO}`;
  const codeWidth = ctx.measureText(codeText).width;
  ctx.font = `500 30px ${FONT_SANS}`;
  const labelWidth = ctx.measureText(opts.codeLabel).width;
  const chipPadX = 44;
  const chipGap = 26;
  const chipW = chipPadX * 2 + labelWidth + chipGap + codeWidth;
  const chipH = 92;
  const chipX = CARD_W / 2 - chipW / 2;
  const chipY = panelY + panelSize + 96;
  roundRectPath(ctx, chipX, chipY, chipW, chipH, chipH / 2);
  ctx.fillStyle = 'rgba(76,141,255,0.14)';
  ctx.fill();
  ctx.strokeStyle = 'rgba(76,141,255,0.42)';
  ctx.lineWidth = 2;
  ctx.stroke();

  ctx.textAlign = 'left';
  ctx.fillStyle = 'rgba(214,226,255,0.6)';
  ctx.font = `500 30px ${FONT_SANS}`;
  ctx.fillText(opts.codeLabel, chipX + chipPadX, chipY + chipH / 2 + 2);
  ctx.fillStyle = BRAND_BLUE;
  ctx.font = `600 38px ${FONT_MONO}`;
  ctx.fillText(codeText, chipX + chipPadX + labelWidth + chipGap, chipY + chipH / 2 + 2);

  // 底部：完整邀请链接
  ctx.textAlign = 'center';
  ctx.fillStyle = 'rgba(214,226,255,0.34)';
  ctx.font = `400 24px ${FONT_MONO}`;
  ctx.fillText(opts.shareLink, CARD_W / 2, CARD_H - 64);

  return canvas.toDataURL('image/png');
}
