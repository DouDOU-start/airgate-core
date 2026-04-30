import { Children, cloneElement, isValidElement, useState, useEffect, useRef, useCallback, type CSSProperties, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import katex from 'katex';
import 'katex/dist/katex.min.css';
import { cssVar } from '@airgate/theme';
import { api, chatCompletion, chatCompletionsStream, editImage as requestImageEdit } from './api';
import type { ChatMessageContent, Conversation, ImageEditResponse, Message, ModelInfo, PlatformInfo, ReasoningEffort, UserInfo } from './api';
import ImageStudioPage from './ImageStudioPage';

const STUDIO_MODE_STORAGE_KEY = 'airgate.playground.studioMode';
// Feature flag — Image Studio is finished but hidden until we decide on persistence
// (see ImageStudioPage.tsx). Flip to true to expose the entry points again.
const IMAGE_STUDIO_ENABLED = false;

declare global {
  interface Window {
    airgate?: {
      confirm?: (message: string, options?: { title?: string; danger?: boolean }) => Promise<boolean>;
    };
  }
}

const MOBILE_BREAKPOINT = 960;
const DRAFT_CONVERSATION_ID = -1;
// 仅匹配 markdown image 里的 base64 部分（data:...；不动 http/blob URL）。
// 用 [A-Za-z0-9+/=]+ 严格 base64 字符集，避免在大字符串里贪婪匹配出问题。
const BASE64_DATA_URL_RE = /data:image\/(?:png|jpeg|jpg|webp|gif);base64,[A-Za-z0-9+/=]+/g;
const MARKDOWN_IMAGE_URL_PATTERN = String.raw`data:image\/(?:png|jpeg|jpg|webp|gif);base64,[^)]+|https?:\/\/[^\s)]+|\/api\/v1\/ext-user\/airgate-playground\/assets\/[^\s)]+|\/assets-runtime\/[^\s)]+|blob:[^\s)]+`;
const IMAGE_MARKDOWN_RE = new RegExp(String.raw`!\[[^\]]*\]\((${MARKDOWN_IMAGE_URL_PATTERN})\)`, 'g');
const IMAGE_MARKDOWN_ITEM_RE = new RegExp(String.raw`!\[([^\]]*)\]\((${MARKDOWN_IMAGE_URL_PATTERN})\)`, 'g');
const IMAGE_MARKDOWN_TEST_RE = new RegExp(String.raw`!\[[^\]]*\]\((${MARKDOWN_IMAGE_URL_PATTERN})\)`);
const IMAGE_EDIT_ANNOTATION_RE = /<!--airgate:image-edit:([A-Za-z0-9+/=]+)-->/g;
const DATA_IMAGE_RE = /^data:image\/(png|jpeg|jpg|webp|gif);base64,/i;
const REASONING_MODEL_RE = /(^|[-_])(?:gpt-?5|o[134]|codex)(?:[-_.]|$)/i;
const IMAGE_MODEL_RE = /(^|[-_])(?:gpt[-_]?image|image)(?:[-_.]|\d|$)/i;
const MAX_IMAGE_BYTES = 10 * 1024 * 1024;
const MIN_SELECTION_SIZE = 8;
const DEFAULT_MODEL_ID = 'gpt-5.5';
const ACTIVE_CONVERSATION_STORAGE_KEY = 'airgate.playground.activeConversationId';
const SELECTED_MODEL_STORAGE_KEY = 'airgate.playground.selectedModel';
const IMAGE_PROMPT_PLANNER_PLATFORM = 'openai';
const IMAGE_PROMPT_PLANNER_MODEL = 'gpt-5.4-mini';
const MAX_IMAGE_SHOTS = 4;
// 选项遵循 codex imagegen SKILL 推荐。`auto` 是默认值，提交时 resolveImageSize
// 返回 undefined → 上游侧不带 size 字段，让 image_generation 工具自己挑。
// 其它 5 个固定值都满足 gpt-image-2 硬约束（边长≤3840、16 倍数、≤3:1、像素 ∈[655360,8294400]），
// 不会被网关侧 validateImageSize 挡掉。
const IMAGE_SIZE_AUTO = 'auto';
const IMAGE_SIZE_OPTIONS: Array<{ value: string; label: string }> = [
  { value: IMAGE_SIZE_AUTO, label: 'Auto' },
  { value: '1024x1024', label: '1024×1024 (1K)' },
  { value: '1536x1024', label: '1536×1024 (1K)' },
  { value: '1024x1536', label: '1024×1536 (1K)' },
  { value: '2048x2048', label: '2048×2048 (2K)' },
  { value: '2048x1152', label: '2048×1152 (2K)' },
  { value: '1152x2048', label: '1152×2048 (2K)' },
  { value: '3840x2160', label: '3840×2160 (4K)' },
  { value: '2160x3840', label: '2160×3840 (4K)' },
];
const DEFAULT_IMAGE_SIZE_SETTINGS: ImageSizeSettings = {
  value: IMAGE_SIZE_AUTO,
};
type ImageSizeSettings = {
  value: string;
};
type SelectOption = { value: string; label: string };
type PendingImage = { id: string; name: string; url: string };
type EditImage = PendingImage & { file: File };
type EditSelectionRect = { x: number; y: number; width: number; height: number };
type ImageEditAnnotation = { imageIndex: number; rect: EditSelectionRect };
type PreviewImage = { url: string; alt: string };
type StreamAssistantOptions = {
  conversationID: number;
  requestMessages: Message[];
  model: string;
  groupID: number;
  platform: string;
  isImageRequest?: boolean;
  imageSize?: string;
  supportsReasoning?: boolean;
  reasoningEffort?: ReasoningEffort;
  titleContent?: string;
};
type RetryRequest = Omit<StreamAssistantOptions, 'titleContent'>;
type MessageContentOptions = {
  onImagePreview?: (url: string, alt: string) => void;
  imagePreviewTitle?: string;
  generatedImageAlt?: string;
  imageEditAnnotations?: ImageEditAnnotation[];
  takeImageIndex?: () => number;
  trailingInlineAction?: ReactNode;
  isMobile?: boolean;
};

function clampNumber(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value));
}

// BlobUrlRegistry 维护「blob URL ↔ 原 base64」的反向映射。
// 4K 图片的 base64 (~6-20 MB) 直接进 React state 会让每次 re-render 拷贝大字符串
// 卡顿；改成在收到 base64 时立即转 Blob，用 URL.createObjectURL 拿到短 blob URL
// (~50 字节) 替换写入 state，渲染走 <img src="blob:..."> 浏览器只解码一次。
// 持久化到后端时再用 registry 反查回原 base64（后端存的是真图，不是 blob 引用）。
type BlobUrlRegistry = Map<string, string>;

function dataUrlToBlob(dataUrl: string): Blob | null {
  const commaIdx = dataUrl.indexOf(',');
  if (commaIdx < 0) return null;
  const meta = dataUrl.slice(5, commaIdx); // 去掉 "data:" 前缀
  const mimeMatch = /^([^;]+)/.exec(meta);
  const mime = mimeMatch?.[1] || 'application/octet-stream';
  const b64 = dataUrl.slice(commaIdx + 1);
  try {
    const binary = atob(b64);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    return new Blob([bytes], { type: mime });
  } catch {
    return null;
  }
}

function replaceBase64WithBlobUrls(content: string, registry: BlobUrlRegistry): string {
  if (!content || !content.includes('data:image/')) return content;
  return content.replace(BASE64_DATA_URL_RE, (dataUrl) => {
    const blob = dataUrlToBlob(dataUrl);
    if (!blob) return dataUrl; // 解码失败保留原值，避免显示空白
    const url = URL.createObjectURL(blob);
    registry.set(url, dataUrl);
    return url;
  });
}

function replaceBlobUrlsWithBase64(content: string, registry: BlobUrlRegistry): string {
  if (!content || !content.includes('blob:')) return content;
  return content.replace(/blob:[^\s)"']+/g, (url) => registry.get(url) || url);
}

function revokeBlobRegistry(registry: BlobUrlRegistry) {
  registry.forEach((_, url) => URL.revokeObjectURL(url));
  registry.clear();
}

function selectionRectFromPoints(start: { x: number; y: number }, end: { x: number; y: number }): EditSelectionRect {
  const x = Math.min(start.x, end.x);
  const y = Math.min(start.y, end.y);
  return {
    x,
    y,
    width: Math.abs(end.x - start.x),
    height: Math.abs(end.y - start.y),
  };
}

function isUsableSelection(rect: EditSelectionRect | null) {
  return Boolean(rect && rect.width >= MIN_SELECTION_SIZE && rect.height >= MIN_SELECTION_SIZE);
}

// 'auto' → undefined 让上游 image_generation 工具按默认（gpt-image-2 → 1024×1024）出图；
// 其它值都已在 IMAGE_SIZE_OPTIONS 里预校验过 gpt-image-2 硬约束。
function resolveImageSize(settings: ImageSizeSettings) {
  return settings.value === IMAGE_SIZE_AUTO ? undefined : settings.value;
}

function imageSizeSummary(settings: ImageSizeSettings) {
  return settings.value === IMAGE_SIZE_AUTO ? 'Auto' : settings.value;
}

function stripImageEditAnnotations(content: string) {
  return content.replace(IMAGE_EDIT_ANNOTATION_RE, '');
}

function stripImageMarkdown(content: string) {
  return stripImageEditAnnotations(content).replace(IMAGE_MARKDOWN_RE, '[Image generated]').trim() || '[Image generated]';
}

function copyableMessageText(content: string) {
  return stripImageEditAnnotations(content).replace(IMAGE_MARKDOWN_RE, '[Image]').trim() || '[Image]';
}

function messageHasGeneratedImage(content: string) {
  return IMAGE_MARKDOWN_TEST_RE.test(content);
}

function encodeImageEditAnnotation(annotation: ImageEditAnnotation) {
  return `<!--airgate:image-edit:${btoa(encodeURIComponent(JSON.stringify(annotation)))}-->`;
}

function parseImageEditAnnotations(content: string): ImageEditAnnotation[] {
  const annotations: ImageEditAnnotation[] = [];
  let match: RegExpExecArray | null;
  IMAGE_EDIT_ANNOTATION_RE.lastIndex = 0;

  while ((match = IMAGE_EDIT_ANNOTATION_RE.exec(content)) !== null) {
    try {
      const value = JSON.parse(decodeURIComponent(atob(match[1])));
      if (
        value &&
        Number.isInteger(value.imageIndex) &&
        value.rect &&
        [value.rect.x, value.rect.y, value.rect.width, value.rect.height].every(item => typeof item === 'number')
      ) {
        annotations.push(value as ImageEditAnnotation);
      }
    } catch { /* ignore malformed annotation */ }
  }

  IMAGE_EDIT_ANNOTATION_RE.lastIndex = 0;
  return annotations;
}

function firstGeneratedImage(content: string): PreviewImage | null {
  IMAGE_MARKDOWN_ITEM_RE.lastIndex = 0;
  const match = IMAGE_MARKDOWN_ITEM_RE.exec(content);
  IMAGE_MARKDOWN_ITEM_RE.lastIndex = 0;
  if (!match) return null;
  return { alt: match[1], url: match[2] };
}

function hasCopyableMessageText(content: string) {
  return stripImageEditAnnotations(content).replace(IMAGE_MARKDOWN_RE, '').trim().length > 0;
}

function escapeMarkdownAlt(text: string) {
  return text.replace(/[\]\\]/g, '');
}

function imageExtensionFromUrl(url: string) {
  const dataMatch = url.match(DATA_IMAGE_RE);
  if (dataMatch) return dataMatch[1].toLowerCase() === 'jpeg' ? 'jpg' : dataMatch[1].toLowerCase();

  try {
    const pathname = new URL(url).pathname;
    const extMatch = pathname.match(/\.([a-z0-9]{2,5})$/i);
    return extMatch ? extMatch[1].toLowerCase() : 'png';
  } catch {
    return 'png';
  }
}

function imageFilename(alt: string, url: string) {
  const base = (alt || 'generated-image')
    .replace(/\.[a-z0-9]{2,5}$/i, '')
    .replace(/[\\/:*?"<>|]+/g, '-')
    .trim()
    .slice(0, 80) || 'generated-image';
  return `${base}.${imageExtensionFromUrl(url)}`;
}

function clickDownload(href: string, filename: string) {
  const link = document.createElement('a');
  link.href = href;
  link.download = filename;
  link.rel = 'noreferrer';
  document.body.appendChild(link);
  link.click();
  link.remove();
}

async function downloadImage(url: string, alt: string) {
  const filename = imageFilename(alt, url);
  if (DATA_IMAGE_RE.test(url)) {
    clickDownload(url, filename);
    return;
  }

  try {
    const resp = await fetch(url);
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    const objectUrl = URL.createObjectURL(await resp.blob());
    try {
      clickDownload(objectUrl, filename);
    } finally {
      URL.revokeObjectURL(objectUrl);
    }
  } catch {
    clickDownload(url, filename);
  }
}

async function copyText(text: string) {
  if (navigator.clipboard?.writeText && window.isSecureContext) {
    await navigator.clipboard.writeText(text);
    return;
  }

  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.style.position = 'fixed';
  textarea.style.opacity = '0';
  textarea.style.pointerEvents = 'none';
  document.body.appendChild(textarea);
  textarea.select();
  const copied = document.execCommand('copy');
  textarea.remove();
  if (!copied) throw new Error('copy failed');
}

function fileToDataURL(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ''));
    reader.onerror = () => reject(reader.error || new Error('Failed to read image'));
    reader.readAsDataURL(file);
  });
}

function canvasToBlob(canvas: HTMLCanvasElement) {
  return new Promise<Blob>((resolve, reject) => {
    canvas.toBlob(blob => {
      if (blob) {
        resolve(blob);
        return;
      }
      reject(new Error('Failed to create mask'));
    }, 'image/png');
  });
}

function loadImageElement(src: string) {
  return new Promise<HTMLImageElement>((resolve, reject) => {
    const image = new Image();
    image.onload = () => resolve(image);
    image.onerror = () => reject(new Error('Failed to load image'));
    image.src = src;
  });
}

function messageContentWithImages(text: string, images: PendingImage[], annotations: ImageEditAnnotation[] = []) {
  const body = text.trim();
  const imageMarkdown = images.map(image => `![${escapeMarkdownAlt(image.name)}](${image.url})`).join('\n');
  const annotationMarkdown = annotations.map(encodeImageEditAnnotation).join('\n');
  return [body, imageMarkdown, annotationMarkdown].filter(Boolean).join('\n\n');
}

async function imagesFromFiles(files: File[]) {
  const images = files.filter(file => file.type.startsWith('image/'));
  if (images.some(file => file.size > MAX_IMAGE_BYTES)) {
    throw new Error('Images must be 10MB or smaller');
  }

  return Promise.all(images.map(async file => ({
    id: `${file.name}-${file.lastModified}-${file.size}`,
    name: file.name || 'pasted-image',
    url: await fileToDataURL(file),
  })));
}

async function editImageFromFile(file: File): Promise<EditImage> {
  if (!file.type.startsWith('image/')) {
    throw new Error('Select an image file');
  }
  if (file.size > MAX_IMAGE_BYTES) {
    throw new Error('Images must be 10MB or smaller');
  }
  return {
    id: `${file.name}-${file.lastModified}-${file.size}`,
    name: file.name || 'source-image',
    url: await fileToDataURL(file),
    file,
  };
}

async function editImageFromUrl(url: string, alt: string): Promise<EditImage> {
  let resp: Response;
  try {
    resp = await fetch(url, url.startsWith('data:') ? undefined : { credentials: 'omit' });
  } catch {
    throw new Error('Failed to fetch image — it may be hosted on a server that blocks cross-origin reads.');
  }
  if (!resp.ok) throw new Error(`Failed to fetch image (HTTP ${resp.status})`);
  const blob = await resp.blob();
  const type = blob.type && blob.type.startsWith('image/') ? blob.type : `image/${imageExtensionFromUrl(url) === 'jpg' ? 'jpeg' : imageExtensionFromUrl(url)}`;
  const filename = imageFilename(alt || 'generated-image', url);
  const file = new File([blob], filename, { type });
  return editImageFromFile(file);
}

function generatedImageUrlFromEdit(response: ImageEditResponse) {
  const image = response.data?.[0];
  if (!image) return '';
  if (image.url) return image.url;
  if (image.b64_json) return `data:image/png;base64,${image.b64_json}`;
  return '';
}

function imageEditAssistantContent(response: ImageEditResponse, fallbackAlt: string) {
  const url = generatedImageUrlFromEdit(response);
  if (!url) return '';
  const revisedPrompt = response.data?.[0]?.revised_prompt?.trim();
  return [revisedPrompt, `![${escapeMarkdownAlt(fallbackAlt)}](${url})`].filter(Boolean).join('\n\n');
}

function imageEditUsage(response: ImageEditResponse) {
  return {
    input_tokens: response.usage?.prompt_tokens || response.usage?.input_tokens || 0,
    output_tokens: response.usage?.completion_tokens || response.usage?.output_tokens || 0,
    cost: response.usage?.cost || 0,
  };
}

function appendImageContent(content: string, nextImageContent: string) {
  return [content.trim(), nextImageContent.trim()].filter(Boolean).join('\n\n');
}

function normalizeImageShotPrompts(prompts: string[]) {
  return prompts
    .map(prompt => prompt.trim())
    .filter(Boolean)
    .slice(0, MAX_IMAGE_SHOTS)
    .map((prompt, index, prompts) => [
      `Shot ${index + 1} of ${prompts.length}. Generate exactly one standalone image for this shot.`,
      'Do not create a collage, grid, contact sheet, split-screen, infographic, or multi-panel layout.',
      prompt,
    ].join(' '));
}

function parseImageShotPlan(content?: string) {
  if (!content) return [];
  try {
    const parsed = JSON.parse(content) as { shots?: unknown };
    if (!Array.isArray(parsed.shots)) return [];
    return normalizeImageShotPrompts(parsed.shots.map(item => typeof item === 'string' ? item : ''));
  } catch {
    return [];
  }
}

function contentWithImageShotPrompt(content: string, prompt: string) {
  IMAGE_MARKDOWN_RE.lastIndex = 0;
  const match = IMAGE_MARKDOWN_RE.exec(content);
  IMAGE_MARKDOWN_RE.lastIndex = 0;
  if (!match) return prompt;
  return [prompt, content.slice(match.index).trim()].filter(Boolean).join('\n\n');
}

function imageShotRequestMessages(messages: Message[], prompt: string) {
  const nextMessages = messages.map(msg => ({ ...msg }));
  const userIndex = nextMessages.map(msg => msg.role).lastIndexOf('user');
  if (userIndex >= 0) {
    nextMessages[userIndex] = {
      ...nextMessages[userIndex],
      content: contentWithImageShotPrompt(nextMessages[userIndex].content, prompt),
    };
  }
  return nextMessages;
}

function titleFromMessageContent(content: string) {
  const title = stripImageEditAnnotations(content).replace(IMAGE_MARKDOWN_RE, '[Image]').trim() || '[Image]';
  return title.slice(0, 30) + (title.length > 30 ? '...' : '');
}

function toChatMessageContent(role: string, content: string): ChatMessageContent {
  const cleanContent = stripImageEditAnnotations(content);
  if (role !== 'user') return stripImageMarkdown(cleanContent);

  const parts: Exclude<ChatMessageContent, string> = [];
  let lastIndex = 0;
  let match: RegExpExecArray | null;
  IMAGE_MARKDOWN_RE.lastIndex = 0;

  while ((match = IMAGE_MARKDOWN_RE.exec(cleanContent)) !== null) {
    const text = cleanContent.slice(lastIndex, match.index).trim();
    if (text) parts.push({ type: 'text', text });
    parts.push({ type: 'image_url', image_url: { url: match[1] } });
    lastIndex = match.index + match[0].length;
  }

  const tail = cleanContent.slice(lastIndex).trim();
  if (tail) parts.push({ type: 'text', text: tail });

  return parts.length ? parts : cleanContent;
}

function isImageModelIdentifier(id?: string, name?: string) {
  return Boolean((id && IMAGE_MODEL_RE.test(id)) || (name && IMAGE_MODEL_RE.test(name)));
}

function isImageModel(model?: ModelInfo) {
  return Boolean(model && (
    model.image_only ||
    model.capabilities?.includes('image_generation') ||
    isImageModelIdentifier(model.id, model.name)
  ));
}

function supportsReasoning(model?: ModelInfo) {
  if (!model || isImageModel(model)) return false;
  return Boolean(model.capabilities?.includes('reasoning') || model.capabilities?.includes('thinking') || REASONING_MODEL_RE.test(model.id));
}

function modelOptionValue(model: ModelInfo) {
  return `${encodeURIComponent(model.platform || '')}:${encodeURIComponent(model.id)}`;
}

function normalizeModelName(value?: string) {
  return (value || '').toLowerCase().replace(/[-_\s]/g, '');
}

function defaultModelOptionValue(models: ModelInfo[]) {
  const target = normalizeModelName(DEFAULT_MODEL_ID);
  const preferred = models.find(model => normalizeModelName(model.id) === target || normalizeModelName(model.name) === target);
  return preferred ? modelOptionValue(preferred) : (models[0] ? modelOptionValue(models[0]) : '');
}

function getStoredActiveConversationId() {
  if (typeof window === 'undefined') return null;
  const raw = window.localStorage.getItem(ACTIVE_CONVERSATION_STORAGE_KEY);
  if (!raw) return null;
  const value = Number(raw);
  return Number.isInteger(value) && value > 0 ? value : null;
}

function getStoredSelectedModel() {
  if (typeof window === 'undefined') return '';
  return window.localStorage.getItem(SELECTED_MODEL_STORAGE_KEY) || '';
}

function platformDisplayName(platforms: PlatformInfo[], name?: string) {
  return platforms.find(item => item.name === name)?.display_name || name || '';
}

function isSafeLinkUrl(url: string) {
  return /^(https?:|mailto:|#)/i.test(url);
}

function isSafeImageUrl(url: string) {
  // blob: 是同源临时 URL，由 URL.createObjectURL 创建，作为 base64 的内存替身使用——
  // 与 data: 等价的安全级别。
  return /^(data:image\/(?:png|jpeg|jpg|webp|gif);base64,|https?:|blob:|\/assets-runtime\/|\/api\/v1\/ext-user\/airgate-playground\/assets\/)/i.test(url);
}

function pushTextWithBreaks(nodes: ReactNode[], text: string, keyPrefix: string) {
  const parts = text.split('\n');
  parts.forEach((part, index) => {
    if (index > 0) {
      nodes.push(<br key={`${keyPrefix}-br-${index}`} />);
    }
    if (part) {
      nodes.push(part);
    }
  });
}

function renderGeneratedImage(key: string, url: string, alt: string, options: MessageContentOptions) {
  const imageIndex = options.takeImageIndex?.() ?? -1;
  const annotation = options.imageEditAnnotations?.find(item => item.imageIndex === imageIndex);
  const image = <img src={url} alt={alt} style={styles.generatedImage} loading="lazy" />;
  const annotatedImage = annotation ? (
    <span style={styles.generatedImageOverlayWrap}>
      {image}
      <span style={styles.generatedImageDimOverlay} />
      <span
        style={{
          ...styles.generatedImageSelection,
          left: `${annotation.rect.x * 100}%`,
          top: `${annotation.rect.y * 100}%`,
          width: `${annotation.rect.width * 100}%`,
          height: `${annotation.rect.height * 100}%`,
        }}
      />
    </span>
  ) : image;
  const previewTitle = options.imagePreviewTitle || 'Preview image';
  const previewableImage = options.onImagePreview ? (
    <button
      type="button"
      style={styles.generatedImagePreviewBtn}
      title={previewTitle}
      aria-label={previewTitle}
      onClick={() => options.onImagePreview?.(url, alt)}
    >
      {annotatedImage}
    </button>
  ) : annotatedImage;

  return (
    <span key={key} style={{ ...styles.generatedImageFrame, ...(options.isMobile ? styles.generatedImageFrameMobile : null) }}>
      {previewableImage}
    </span>
  );
}

function renderInlineMarkdown(text: string, keyPrefix: string, options: MessageContentOptions = {}) {
  const nodes: ReactNode[] = [];
  const inlineRe = /(`([^`]+)`|\\\(([\s\S]*?)\\\)|(?<!\\)\$(?!\s)([^\n$]*?\S)(?<!\\)\$|!\[([^\]]*)\]\(([^)\s]+)\)|\[([^\]]+)\]\(([^)\s]+)\)|\*\*([^*]+)\*\*|__([^_]+)__|\*([^*]+)\*|_([^_]+)_)/g;
  let lastIndex = 0;
  let match: RegExpExecArray | null;

  while ((match = inlineRe.exec(text)) !== null) {
    if (match.index > lastIndex) {
      pushTextWithBreaks(nodes, text.slice(lastIndex, match.index), `${keyPrefix}-text-${lastIndex}`);
    }

    const key = `${keyPrefix}-${match.index}`;
    const inlineCode = match[2];
    const parenMath = match[3];
    const dollarMath = match[4];
    const imageAlt = match[5];
    const imageUrl = match[6];
    const linkText = match[7];
    const linkUrl = match[8];
    const boldText = match[9] || match[10];
    const italicText = match[11] || match[12];

    if (inlineCode) {
      nodes.push(<code key={key} style={styles.markdownInlineCode}>{inlineCode}</code>);
    } else if (parenMath || dollarMath) {
      nodes.push(renderMath(parenMath || dollarMath, key, false));
    } else if (imageUrl && isSafeImageUrl(imageUrl)) {
      nodes.push(renderGeneratedImage(key, imageUrl, imageAlt || options.generatedImageAlt || 'Generated image', options));
    } else if (linkUrl && isSafeLinkUrl(linkUrl)) {
      nodes.push(
        <a key={key} href={linkUrl} style={styles.markdownLink} target="_blank" rel="noreferrer">
          {renderInlineMarkdown(linkText, `${key}-link`, options)}
        </a>,
      );
    } else if (boldText) {
      nodes.push(<strong key={key}>{renderInlineMarkdown(boldText, `${key}-bold`, options)}</strong>);
    } else if (italicText) {
      nodes.push(<em key={key}>{renderInlineMarkdown(italicText, `${key}-em`, options)}</em>);
    } else {
      nodes.push(match[0]);
    }

    lastIndex = match.index + match[0].length;
  }

  if (lastIndex < text.length) {
    pushTextWithBreaks(nodes, text.slice(lastIndex), `${keyPrefix}-text-${lastIndex}`);
  }

  return nodes.length > 0 ? nodes : text;
}

function renderMath(tex: string, key: string, displayMode: boolean) {
  const html = katex.renderToString(tex, {
    displayMode,
    throwOnError: false,
    strict: 'ignore',
    trust: false,
  });

  const Tag = displayMode ? 'div' : 'span';
  return <Tag key={key} style={displayMode ? styles.markdownBlockMath : styles.markdownInlineMath} dangerouslySetInnerHTML={{ __html: html }} />;
}

function renderHeading(level: number, text: string, key: string, options: MessageContentOptions = {}) {
  const content = renderInlineMarkdown(text, `${key}-inline`, options);
  if (level === 1) return <h1 key={key} style={styles.markdownH1}>{content}</h1>;
  if (level === 2) return <h2 key={key} style={styles.markdownH2}>{content}</h2>;
  if (level === 3) return <h3 key={key} style={styles.markdownH3}>{content}</h3>;
  return <h4 key={key} style={styles.markdownH4}>{content}</h4>;
}

function renderImageGroup(text: string, key: string, options: MessageContentOptions = {}) {
  const images = parseImageGroupImages(text);
  if (!images) return null;

  return renderImageGallery(images, key, options);
}

function parseImageGroupImages(text: string) {
  const images: Array<{ alt: string; url: string }> = [];
  let match: RegExpExecArray | null;
  IMAGE_MARKDOWN_ITEM_RE.lastIndex = 0;

  while ((match = IMAGE_MARKDOWN_ITEM_RE.exec(text)) !== null) {
    images.push({ alt: match[1], url: match[2] });
  }

  const remainder = text.replace(IMAGE_MARKDOWN_ITEM_RE, '').trim();
  if (!images.length || remainder) return null;

  return images;
}

function renderImageGallery(images: Array<{ alt: string; url: string }>, key: string, options: MessageContentOptions = {}) {
  return (
    <div key={key} style={{ ...styles.imageGroup, ...(options.isMobile ? styles.imageGroupMobile : null) }}>
      {images.map((image, index) => renderGeneratedImage(`${key}-${index}`, image.url, image.alt || options.generatedImageAlt || 'Generated image', options))}
    </div>
  );
}

const INLINE_ACTION_TARGETS = new Set(['p', 'h1', 'h2', 'h3', 'h4', 'blockquote', 'li']);

function appendTrailingInlineActionToNode(node: ReactNode, action: ReactNode): ReactNode | null {
  if (!isValidElement<{ children?: ReactNode }>(node) || typeof node.type !== 'string') return null;

  if (INLINE_ACTION_TARGETS.has(node.type)) {
    return cloneElement(node, undefined, ...Children.toArray(node.props.children), action);
  }

  if (node.type === 'ol' || node.type === 'ul') {
    const children: ReactNode[] = Children.toArray(node.props.children);
    for (let index = children.length - 1; index >= 0; index--) {
      const childWithAction = appendTrailingInlineActionToNode(children[index], action);
      if (childWithAction) {
        const nextChildren: ReactNode[] = [...children];
        nextChildren[index] = childWithAction;
        return cloneElement(node, undefined, ...nextChildren);
      }
    }
  }

  return null;
}

function appendTrailingInlineAction(nodes: ReactNode[], action?: ReactNode) {
  if (!action) return nodes;

  for (let index = nodes.length - 1; index >= 0; index--) {
    const nodeWithAction = appendTrailingInlineActionToNode(nodes[index], action);
    if (nodeWithAction) {
      const nextNodes = [...nodes];
      nextNodes[index] = nodeWithAction;
      return nextNodes;
    }
  }

  return nodes;
}

function renderMessageContent(content: string, options: MessageContentOptions = {}) {
  const cleanContent = stripImageEditAnnotations(content);
  let imageIndex = -1;
  const renderOptions: MessageContentOptions = {
    ...options,
    imageEditAnnotations: options.imageEditAnnotations || parseImageEditAnnotations(content),
    takeImageIndex: () => {
      imageIndex += 1;
      return imageIndex;
    },
  };
  const lines = cleanContent.replace(/\r\n?/g, '\n').split('\n');
  const nodes: ReactNode[] = [];
  let paragraph: string[] = [];
  let quote: string[] = [];
  let listItems: Array<{ text: string; ordered: boolean }> = [];
  let codeLines: string[] = [];
  let mathLines: string[] = [];
  let pendingImageGroup: Array<{ alt: string; url: string }> = [];
  let inCodeBlock = false;
  let inMathBlock: '$$' | '\\]' | null = null;
  let nodeIndex = 0;

  const nextKey = (type: string) => `${type}-${nodeIndex++}`;
  const flushPendingImageGroup = () => {
    if (!pendingImageGroup.length) return;
    nodes.push(renderImageGallery(pendingImageGroup, nextKey('images'), renderOptions));
    pendingImageGroup = [];
  };
  const flushParagraph = () => {
    if (!paragraph.length) return;
    const text = paragraph.join('\n');
    const images = parseImageGroupImages(text);
    if (images) {
      pendingImageGroup.push(...images);
    } else {
      flushPendingImageGroup();
      const key = nextKey('p');
      nodes.push(<p key={key} style={styles.markdownParagraph}>{renderInlineMarkdown(text, key, renderOptions)}</p>);
    }
    paragraph = [];
  };
  const flushQuote = () => {
    if (!quote.length) return;
    flushPendingImageGroup();
    const key = nextKey('quote');
    nodes.push(<blockquote key={key} style={styles.markdownBlockquote}>{renderInlineMarkdown(quote.join('\n'), key, renderOptions)}</blockquote>);
    quote = [];
  };
  const flushList = () => {
    if (!listItems.length) return;
    flushPendingImageGroup();
    const key = nextKey('list');
    const children = listItems.map((item, index) => (
      <li key={`${key}-${index}`} style={styles.markdownListItem}>{renderInlineMarkdown(item.text, `${key}-${index}`, renderOptions)}</li>
    ));
    nodes.push(listItems[0].ordered ? <ol key={key} style={styles.markdownList}>{children}</ol> : <ul key={key} style={styles.markdownList}>{children}</ul>);
    listItems = [];
  };
  const flushBlocks = () => {
    flushParagraph();
    flushQuote();
    flushList();
  };
  const flushAllBlocks = () => {
    flushBlocks();
    flushPendingImageGroup();
  };
  const flushCodeBlock = () => {
    flushPendingImageGroup();
    const key = nextKey('code');
    nodes.push(<pre key={key} style={styles.markdownCodeBlock}><code>{codeLines.join('\n')}</code></pre>);
    codeLines = [];
  };
  const flushMathBlock = () => {
    flushPendingImageGroup();
    const key = nextKey('math');
    nodes.push(renderMath(mathLines.join('\n').trim(), key, true));
    mathLines = [];
  };

  for (const line of lines) {
    if (inMathBlock) {
      const closingIndex = inMathBlock === '$$' ? line.indexOf('$$') : line.indexOf('\\]');
      if (closingIndex >= 0) {
        const delimiterLength = inMathBlock.length;
        mathLines.push(line.slice(0, closingIndex));
        flushMathBlock();
        inMathBlock = null;
        const rest = line.slice(closingIndex + delimiterLength).trim();
        if (rest) paragraph.push(rest);
      } else {
        mathLines.push(line);
      }
      continue;
    }

    const fenceMatch = line.match(/^```/);
    if (fenceMatch) {
      if (inCodeBlock) {
        flushCodeBlock();
        inCodeBlock = false;
      } else {
        flushAllBlocks();
        inCodeBlock = true;
      }
      continue;
    }

    if (inCodeBlock) {
      codeLines.push(line);
      continue;
    }

    if (!line.trim()) {
      flushBlocks();
      continue;
    }

    const trimmedLine = line.trim();
    const dollarBlockMatch = trimmedLine.match(/^\$\$([\s\S]*?)\$\$$/);
    const bracketBlockMatch = trimmedLine.match(/^\\\[([\s\S]*?)\\\]$/);
    if (dollarBlockMatch || bracketBlockMatch) {
      flushAllBlocks();
      nodes.push(renderMath((dollarBlockMatch?.[1] || bracketBlockMatch?.[1] || '').trim(), nextKey('math'), true));
      continue;
    }

    if (trimmedLine.startsWith('$$') || trimmedLine.startsWith('\\[')) {
      flushAllBlocks();
      const isDollarMath = trimmedLine.startsWith('$$');
      const openingDelimiter = isDollarMath ? '$$' : '\\[';
      const closingDelimiter = isDollarMath ? '$$' : '\\]';
      const afterOpening = trimmedLine.slice(openingDelimiter.length);
      const closingIndex = afterOpening.indexOf(closingDelimiter);
      if (closingIndex >= 0) {
        nodes.push(renderMath(afterOpening.slice(0, closingIndex).trim(), nextKey('math'), true));
      } else {
        mathLines.push(afterOpening);
        inMathBlock = closingDelimiter as '$$' | '\\]';
      }
      continue;
    }

    const headingMatch = line.match(/^(#{1,6})\s+(.+)$/);
    if (headingMatch) {
      flushAllBlocks();
      nodes.push(renderHeading(Math.min(headingMatch[1].length, 4), headingMatch[2].trim(), nextKey('heading'), renderOptions));
      continue;
    }

    if (/^\s*([-*_])\s*(\1\s*){2,}$/.test(line)) {
      flushAllBlocks();
      nodes.push(<hr key={nextKey('hr')} style={styles.markdownDivider} />);
      continue;
    }

    const quoteMatch = line.match(/^>\s?(.*)$/);
    if (quoteMatch) {
      flushParagraph();
      flushList();
      quote.push(quoteMatch[1]);
      continue;
    }

    const unorderedMatch = line.match(/^\s*[-*+]\s+(.+)$/);
    const orderedMatch = line.match(/^\s*\d+[.)]\s+(.+)$/);
    if (unorderedMatch || orderedMatch) {
      flushParagraph();
      flushQuote();
      const ordered = Boolean(orderedMatch);
      if (listItems.length && listItems[0].ordered !== ordered) flushList();
      listItems.push({ ordered, text: (orderedMatch?.[1] || unorderedMatch?.[1] || '').trim() });
      continue;
    }

    flushQuote();
    flushList();
    paragraph.push(line);
  }

  if (inCodeBlock) flushCodeBlock();
  if (inMathBlock) flushMathBlock();
  flushBlocks();
  flushPendingImageGroup();

  const renderedNodes = appendTrailingInlineAction(nodes, options.trailingInlineAction);
  return renderedNodes.length > 0 ? renderedNodes : cleanContent;
}

export default function PlaygroundPage() {
  const { t } = useTranslation();

  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [conversationsLoaded, setConversationsLoaded] = useState(false);
  const [activeId, setActiveId] = useState<number | null>(null);
  const [messages, setMessagesRaw] = useState<Message[]>([]);
  const [streamConversationId, setStreamConversationId] = useState<number | null>(null);
  const [streamContent, setStreamContent] = useState('');
  const [streamReasoning, setStreamReasoning] = useState('');
  const [isStreaming, setIsStreaming] = useState(false);
  const [input, setInput] = useState('');
  const [pendingImages, setPendingImages] = useState<PendingImage[]>([]);
  const [editSource, setEditSource] = useState<EditImage | null>(null);
  const [isEditPanelOpen, setIsEditPanelOpen] = useState(false);
  const [isEditingImage, setIsEditingImage] = useState(false);
  const [editSelection, setEditSelection] = useState<EditSelectionRect | null>(null);
  const [draftEditSelection, setDraftEditSelection] = useState<EditSelectionRect | null>(null);
  const [editStageSize, setEditStageSize] = useState<{ width: number; height: number } | null>(null);
  const [previewImage, setPreviewImage] = useState<PreviewImage | null>(null);

  const [platforms, setPlatforms] = useState<PlatformInfo[]>([]);
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [selectedModel, setSelectedModel] = useState('');
  const [openSelectId, setOpenSelectId] = useState<string | null>(null);
  const [reasoningEffort, setReasoningEffort] = useState<ReasoningEffort>('medium');
  const [imageSizeSettings, setImageSizeSettings] = useState<ImageSizeSettings>(() => ({ ...DEFAULT_IMAGE_SIZE_SETTINGS }));
  const [userInfo, setUserInfo] = useState<UserInfo | null>(null);
  const [error, setError] = useState('');
  const [retryRequest, setRetryRequest] = useState<RetryRequest | null>(null);
  const [interactionNotice, setInteractionNotice] = useState('');
  const [hoveredCopyTarget, setHoveredCopyTarget] = useState<string | null>(null);
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [isMobile, setIsMobile] = useState(() => (
    typeof window !== 'undefined' ? window.innerWidth <= MOBILE_BREAKPOINT : false
  ));
  const [studioMode, setStudioMode] = useState(() => {
    if (typeof window === 'undefined') return false;
    return localStorage.getItem(STUDIO_MODE_STORAGE_KEY) === '1';
  });

  useEffect(() => {
    if (typeof window === 'undefined') return;
    if (studioMode) localStorage.setItem(STUDIO_MODE_STORAGE_KEY, '1');
    else localStorage.removeItem(STUDIO_MODE_STORAGE_KEY);
  }, [studioMode]);

  const messagesEndRef = useRef<HTMLDivElement>(null);
  const abortRef = useRef<AbortController | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const editFileInputRef = useRef<HTMLInputElement>(null);
  const editCanvasRef = useRef<HTMLCanvasElement>(null);
  const editCanvasContainerRef = useRef<HTMLDivElement>(null);
  const selectionStartRef = useRef<{ x: number; y: number } | null>(null);
  const activeIdRef = useRef<number | null>(null);
  const streamContextRef = useRef<{ conversationId: number; model: string } | null>(null);
  const skipNextMessagesLoadRef = useRef<number | null>(null);
  // 用户主动点了发送 / 停止 / Enter 后，标记一下；流式结束后把焦点送回 textarea。
  // 否则 textarea 在流式期间 disabled，焦点丢失，用户每次都要重新点输入框，烦死。
  const pendingRefocusRef = useRef(false);
  // base64 → blob URL 的反向映射，详见 BlobUrlRegistry 注释。
  // 切对话时全部 revoke（下面 useEffect [activeId] 的 cleanup），unmount 同理。
  const blobUrlRegistryRef = useRef<BlobUrlRegistry>(new Map());

  // setMessages 包装：所有写入 messages 的内容自动把 base64 data URL 转成 blob URL，
  // 原 base64 进 registry。这样 messages state 里都是短字符串，React re-render
  // 不再拷贝 ~10MB 文本。replaceBase64WithBlobUrls 对已是 blob URL 的 content 是 no-op，
  // 所以 setMessages(prev => prev.map(...)) 这种"操作内部数据"的调用也安全。
  const setMessages = useCallback<typeof setMessagesRaw>((arg) => {
    const transform = (msgs: Message[]) =>
      msgs.map(msg => {
        if (!msg.content || !msg.content.includes('data:image/')) return msg;
        return { ...msg, content: replaceBase64WithBlobUrls(msg.content, blobUrlRegistryRef.current) };
      });
    if (typeof arg === 'function') {
      setMessagesRaw(prev => transform(arg(prev)));
    } else {
      setMessagesRaw(transform(arg));
    }
  }, []);

  useEffect(() => {
    if (!isStreaming && !isEditingImage && pendingRefocusRef.current) {
      pendingRefocusRef.current = false;
      // 等下一帧让 disabled=false 先生效再 focus
      requestAnimationFrame(() => {
        inputRef.current?.focus();
      });
    }
  }, [isStreaming, isEditingImage]);

  useEffect(() => {
    api.listConversations().then(nextConversations => {
      setConversations(nextConversations);
      setConversationsLoaded(true);
      const storedActiveId = getStoredActiveConversationId();
      if (storedActiveId && nextConversations.some(item => item.id === storedActiveId)) {
        setActiveId(storedActiveId);
      } else {
        window.localStorage.removeItem(ACTIVE_CONVERSATION_STORAGE_KEY);
      }
    }).catch(() => {});
    api.getUserInfo().then(setUserInfo).catch(() => {});
    let cancelled = false;
    api.listPlatforms().then(async nextPlatforms => {
      if (cancelled) return;
      setPlatforms(nextPlatforms);
      const modelLists = await Promise.all(nextPlatforms.map(platform => api.listModels(platform.name).catch(() => [])));
      if (cancelled) return;
      const nextModels = modelLists.flat();
      const storedModel = getStoredSelectedModel();
      setModels(nextModels);
      setSelectedModel(current => {
        const existingValue = current || storedModel;
        return nextModels.some(item => modelOptionValue(item) === existingValue) ? existingValue : defaultModelOptionValue(nextModels);
      });
    }).catch(e => {
      if (cancelled) return;
      setModels([]);
      setSelectedModel('');
      setRetryRequest(null);
      setError(e instanceof Error ? e.message : 'Failed to load models');
    });
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    activeIdRef.current = activeId;
    if (typeof window === 'undefined' || !conversationsLoaded) return;
    if (activeId && activeId !== DRAFT_CONVERSATION_ID) {
      window.localStorage.setItem(ACTIVE_CONVERSATION_STORAGE_KEY, String(activeId));
    } else {
      window.localStorage.removeItem(ACTIVE_CONVERSATION_STORAGE_KEY);
    }
  }, [activeId, conversationsLoaded]);

  useEffect(() => {
    if (typeof window === 'undefined' || models.length === 0) return;
    if (selectedModel && models.some(item => modelOptionValue(item) === selectedModel)) {
      window.localStorage.setItem(SELECTED_MODEL_STORAGE_KEY, selectedModel);
    } else {
      window.localStorage.removeItem(SELECTED_MODEL_STORAGE_KEY);
    }
  }, [models, selectedModel]);

  useEffect(() => {
    if (!activeId || activeId === DRAFT_CONVERSATION_ID) { setMessages([]); return; }
    if (skipNextMessagesLoadRef.current === activeId) {
      skipNextMessagesLoadRef.current = null;
      return;
    }
    api.listMessages(activeId).then(setMessages).catch(() => {});
  }, [activeId, setMessages]);

  // blob URL 只在组件 unmount 时统一回收。
  // 不能跟着 activeId cleanup —— DRAFT → 真实 conv.id 切换会触发 cleanup，把流式
  // 中间产生的 blob URL 全 revoke 掉，多 shot 后续 finishAssistantResponse 反查
  // registry 找不到原 base64，结果存了一堆失效 blob: URL 进库，刷新就破图。
  // 代价：单次 session 旧对话的 blob 不再用但占内存到关页面，几 MB 可接受。
  useEffect(() => {
    const registry = blobUrlRegistryRef.current;
    return () => {
      revokeBlobRegistry(registry);
    };
  }, []);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages, streamContent, streamReasoning]);

  useEffect(() => {
    if (typeof window === 'undefined') return;

    const mediaQuery = window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT}px)`);
    const updateViewport = (event?: MediaQueryListEvent) => {
      setIsMobile(event ? event.matches : mediaQuery.matches);
    };

    updateViewport();

    if (mediaQuery.addEventListener) {
      mediaQuery.addEventListener('change', updateViewport);
      return () => mediaQuery.removeEventListener('change', updateViewport);
    }

    mediaQuery.addListener(updateViewport);
    return () => mediaQuery.removeListener(updateViewport);
  }, []);

  useEffect(() => {
    setSidebarOpen(!isMobile);
  }, [isMobile]);

  useEffect(() => {
    if (!interactionNotice) return;
    const timer = window.setTimeout(() => setInteractionNotice(''), 1400);
    return () => window.clearTimeout(timer);
  }, [interactionNotice]);

  useEffect(() => {
    if (!editSource || !isEditPanelOpen) return;
    let cancelled = false;

    const syncCanvas = async () => {
      const canvas = editCanvasRef.current;
      const container = editCanvasContainerRef.current;
      if (!canvas || !container) return;

      const image = await loadImageElement(editSource.url);
      if (cancelled) return;

      const maxWidth = container.clientWidth || image.naturalWidth;
      const viewportHeight = typeof window !== 'undefined' ? window.innerHeight : 800;
      const maxHeight = isMobile
        ? Math.min(viewportHeight * 0.45, 360)
        : Math.min(viewportHeight * 0.62, 720);
      const scale = Math.min(1, maxWidth / image.naturalWidth, maxHeight / image.naturalHeight);
      const width = Math.max(1, Math.round(image.naturalWidth * scale));
      const height = Math.max(1, Math.round(image.naturalHeight * scale));
      canvas.width = width;
      canvas.height = height;
      canvas.style.width = `${width}px`;
      canvas.style.height = `${height}px`;
      setEditStageSize({ width, height });

      const ctx = canvas.getContext('2d');
      if (!ctx) return;
      ctx.clearRect(0, 0, width, height);
      setEditSelection(null);
      setDraftEditSelection(null);
      selectionStartRef.current = null;
    };

    void syncCanvas().catch(err => setError(err instanceof Error ? err.message : 'Failed to load image'));
    if (typeof ResizeObserver === 'undefined') {
      return () => { cancelled = true; };
    }

    const observer = new ResizeObserver(() => {
      void syncCanvas().catch(err => setError(err instanceof Error ? err.message : 'Failed to load image'));
    });
    if (editCanvasContainerRef.current) observer.observe(editCanvasContainerRef.current);
    return () => {
      cancelled = true;
      observer.disconnect();
    };
  }, [editSource, isEditPanelOpen, isMobile]);

  const resolveGroupID = useCallback(() => 0, []);

  const updateImageSizeSettings = useCallback((patch: Partial<ImageSizeSettings>) => {
    setImageSizeSettings(current => ({ ...current, ...patch }));
  }, []);

  const selectedModelInfo = models.find(item => modelOptionValue(item) === selectedModel);
  const selectedModelID = selectedModelInfo?.id || '';
  const selectedModelPlatform = selectedModelInfo?.platform || '';
  const selectedModelIsImage = isImageModel(selectedModelInfo);
  const selectedModelSupportsReasoning = supportsReasoning(selectedModelInfo);
  const resolvedImageSize = resolveImageSize(imageSizeSettings);

  const selectedPlatform = selectedModelPlatform;
  const modelOptions = models.map(model => ({
    value: modelOptionValue(model),
    label: `${model.name || model.id} · ${platformDisplayName(platforms, model.platform)}`,
  }));

  const renderCustomSelect = ({
    id,
    value,
    options,
    onChange,
    ariaLabel,
    variant = 'chip',
  }: {
    id: string;
    value: string;
    options: SelectOption[];
    onChange: (value: string) => void;
    ariaLabel: string;
    variant?: 'model' | 'chip';
  }) => {
    const selectedOption = options.find(option => option.value === value);
    const isOpen = openSelectId === id;
    const buttonStyle = variant === 'model'
      ? { ...styles.selectTrigger, ...styles.modelSelectTrigger, ...(isMobile ? styles.modelSelectTriggerMobile : null) }
      : { ...styles.selectTrigger, ...styles.chipSelectTrigger, ...(isMobile ? styles.chipSelectTriggerMobile : null) };
    const listbox = (
      <div
        style={{
          ...styles.selectPopover,
          ...(variant === 'model' ? styles.selectPopoverModel : styles.selectPopoverChip),
          ...(isMobile ? styles.selectPopoverMobile : null),
        }}
        role="listbox"
        aria-label={ariaLabel}
      >
        {options.map(option => {
          const optionSelected = option.value === value;
          return (
            <button
              key={option.value}
              type="button"
              style={{
                ...styles.selectOption,
                ...(optionSelected ? styles.selectOptionActive : null),
                ...(variant === 'model' ? styles.selectOptionModel : null),
              }}
              role="option"
              aria-selected={optionSelected}
              onClick={() => {
                onChange(option.value);
                setOpenSelectId(null);
              }}
            >
              {option.label}
            </button>
          );
        })}
      </div>
    );

    return (
      <div style={{ ...styles.selectWrap, ...(variant === 'model' ? styles.modelSelectWrap : null) }}>
        <button
          type="button"
          style={{ ...buttonStyle, ...(isOpen ? styles.selectTriggerOpen : null) }}
          aria-label={ariaLabel}
          aria-haspopup="listbox"
          aria-expanded={isOpen}
          onClick={() => setOpenSelectId(current => current === id ? null : id)}
        >
          <span style={styles.selectTriggerText}>{selectedOption?.label || ariaLabel}</span>
          <span aria-hidden="true" style={styles.selectTriggerCaret}>v</span>
        </button>
        {isOpen && (
          isMobile ? (
            <div style={styles.selectOverlay} onClick={() => setOpenSelectId(null)}>
              <div style={styles.selectSheet} onClick={event => event.stopPropagation()}>
                <div style={styles.selectSheetHeader}>{ariaLabel}</div>
                {listbox}
              </div>
            </div>
          ) : listbox
        )}
      </div>
    );
  };

  const createConversation = useCallback(() => {
    const now = new Date().toISOString();
    const draft: Conversation = {
      id: DRAFT_CONVERSATION_ID,
      user_id: userInfo?.id || 0,
      title: '',
      group_id: resolveGroupID(),
      platform: selectedPlatform,
      model: selectedModelID,
      created_at: now,
      updated_at: now,
    };
    setConversations(prev => [draft, ...prev.filter(c => c.id !== DRAFT_CONVERSATION_ID)]);
    setActiveId(DRAFT_CONVERSATION_ID);
    setMessages([]);
    setPendingImages([]);
    setEditSource(null);
    setIsEditPanelOpen(false);
    setEditSelection(null);
    setDraftEditSelection(null);
    setEditStageSize(null);
    setError('');
    setRetryRequest(null);
    if (isMobile) {
      setSidebarOpen(false);
    }
  }, [isMobile, resolveGroupID, selectedPlatform, selectedModelID, userInfo?.id]);

  const deleteConversation = useCallback(async (id: number) => {
    const confirmed = await window.airgate?.confirm?.(t('playground.delete_conversation_confirm'), {
      title: t('playground.delete_conversation'),
      danger: true,
    });
    if (!confirmed) return;

    if (id === DRAFT_CONVERSATION_ID) {
      setConversations(prev => prev.filter(c => c.id !== id));
      if (activeId === id) {
        setActiveId(null);
        setMessages([]);
      }
      return;
    }

    try {
      await api.deleteConversation(id);
      setConversations(prev => prev.filter(c => c.id !== id));
      if (activeId === id) {
        setActiveId(null);
        setMessages([]);
      }
    } catch { /* ignore */ }
  }, [activeId, t]);

  const streamAssistantResponse = useCallback(async ({
    conversationID,
    requestMessages,
    model,
    groupID,
    platform,
    isImageRequest,
    imageSize,
    supportsReasoning: requestSupportsReasoning,
    reasoningEffort: requestReasoningEffort,
    titleContent,
  }: StreamAssistantOptions) => {
    const nextRetryRequest: RetryRequest = {
      conversationID,
      requestMessages: requestMessages.map(msg => ({ ...msg })),
      model,
      groupID,
      platform,
      isImageRequest,
      imageSize,
      supportsReasoning: requestSupportsReasoning,
      reasoningEffort: requestReasoningEffort,
    };
    setError('');
    setRetryRequest(null);
    setIsStreaming(true);
    setStreamConversationId(conversationID);
    streamContextRef.current = { conversationId: conversationID, model };
    setStreamContent('');
    setStreamReasoning('');

    try {
      const abort = new AbortController();
      abortRef.current = abort;

      let accumulated = '';
      let accumulatedReasoning = '';
      const baseRequest = {
        model,
        // user message 可能含 blob URL（参考图被前端转过），上游需要原 base64，
        // 调 toChatMessageContent 之前先反转。
        messages: requestMessages.map(msg => ({
          role: msg.role,
          content: toChatMessageContent(msg.role, replaceBlobUrlsWithBase64(msg.content, blobUrlRegistryRef.current)),
        })),
        stream: true as const,
        ...(isImageRequest && imageSize ? { size: imageSize } : {}),
        ...(requestSupportsReasoning ? { reasoning_effort: requestReasoningEffort ?? reasoningEffort } : {}),
      };
      const finishAssistantResponse = async (usage: { input_tokens: number; output_tokens: number; model: string; cost: number }) => {
        if (!accumulated) {
          if (activeIdRef.current === conversationID) {
            setError(t('playground.no_response'));
            setRetryRequest(nextRetryRequest);
          }
          setStreamContent('');
          setStreamReasoning('');
          setStreamConversationId(null);
          streamContextRef.current = null;
          setIsStreaming(false);
          return;
        }
        // accumulated 流式期间已经把 base64 转成 blob URL；后端要存原图，
        // 在 persistMessage 调用前反查 registry 把 blob URL 还原回 base64。
        const persistedBase64 = replaceBlobUrlsWithBase64(accumulated, blobUrlRegistryRef.current);
        const persisted = await api.persistMessage({
          conversation_id: conversationID,
          role: 'assistant',
          content: persistedBase64,
          reasoning: accumulatedReasoning,
          platform,
          model: usage.model || model,
          group_id: groupID,
          input_tokens: usage.input_tokens,
          output_tokens: usage.output_tokens,
          cost: usage.cost,
        });
        if (activeIdRef.current === conversationID) {
          // 关键：用 setMessagesRaw（不走包装），content 用流式期间已转好的 accumulated，
          // 复用同一组 blob URL。否则包装会把 persisted.content 的 base64 再次转新
          // blob URL，旧的 accumulated 那批就泄漏在 registry 里了。
          setMessagesRaw(prev => [...prev, { ...persisted, content: accumulated }]);
        }
        setRetryRequest(null);
        if (titleContent) {
          setConversations(prev => prev.map(c =>
            c.id === conversationID && !c.title
              ? { ...c, title: titleFromMessageContent(titleContent), updated_at: new Date().toISOString() }
              : c
          ));
        }
        setStreamContent('');
        setStreamReasoning('');
        setStreamConversationId(null);
        streamContextRef.current = null;
        setIsStreaming(false);
      };

      if (isImageRequest) {
        const lastUserMessage = requestMessages.filter(msg => msg.role === 'user').at(-1);
        const userPrompt = lastUserMessage ? stripImageMarkdown(stripImageEditAnnotations(lastUserMessage.content)).trim() : '';
        let finalShotPrompts = [userPrompt || 'Generate the requested image.'];
        const planResponse = await chatCompletion(
          IMAGE_PROMPT_PLANNER_PLATFORM,
          {
            model: IMAGE_PROMPT_PLANNER_MODEL,
            messages: [
              {
                role: 'system',
                content: [
                  `Analyze the user's intent and convert the image-generation request into 1 to ${MAX_IMAGE_SHOTS} standalone image prompts.`,
                  'Return only JSON with a shots array of strings.',
                  'The number of strings in shots is the number of images to generate: one string means one image, multiple strings mean multiple separate images.',
                  'Use one shot only when the user wants one final image/composite. Use multiple shots when the user wants multiple deliverables, separate scenes, separate assets, variants, product angles, lifestyle scenes, feature diagrams, packaging views, story frames, or a set/series of images.',
                  'Do not merge separate requested images into one collage, grid, contact sheet, split-screen, infographic, poster, or multi-panel layout unless the user explicitly asks for a single combined image.',
                  'Each shot must be a complete prompt for exactly one standalone image.',
                ].join(' '),
              },
              { role: 'user', content: userPrompt || 'Generate the requested image.' },
            ],
            stream: false,
            response_format: { type: 'json_object' },
          },
          abort.signal,
        );
        if (abort.signal.aborted) return;
        const plannerContent = planResponse.choices?.[0]?.message?.content;
        const shotPrompts = parseImageShotPlan(plannerContent);
        finalShotPrompts = shotPrompts.length ? shotPrompts : finalShotPrompts;
        console.info('[image-planner]', {
          raw: plannerContent,
          parsedCount: shotPrompts.length,
          parsedShots: shotPrompts,
          finalCount: finalShotPrompts.length,
          finalShots: finalShotPrompts,
        });
        const planUsage = {
          input_tokens: planResponse.usage?.prompt_tokens || planResponse.usage?.input_tokens || 0,
          output_tokens: planResponse.usage?.completion_tokens || planResponse.usage?.output_tokens || 0,
          model: planResponse.model || IMAGE_PROMPT_PLANNER_MODEL,
          cost: planResponse.usage?.cost || 0,
        };
        const requests = finalShotPrompts.map(prompt => new Promise<{ input_tokens: number; output_tokens: number; model: string; cost: number }>((resolve, reject) => {
          let localContent = '';
          let appended = false;
          const appendLocalContent = () => {
            if (appended || !localContent.trim()) return;
            accumulated = appendImageContent(accumulated, localContent);
            setStreamContent(accumulated);
            appended = true;
          };

          void chatCompletionsStream(
            platform,
            {
              ...baseRequest,
              messages: imageShotRequestMessages(requestMessages, prompt).map(msg => ({
                role: msg.role,
                // user message 可能含 blob URL（参考图被前端转过），上游需要原 base64，
                // 调 toChatMessageContent 之前先反转。assistant message 在 stripImageMarkdown
                // 时图片已被剥掉，反转一次也无害（幂等）。
                content: toChatMessageContent(msg.role, replaceBlobUrlsWithBase64(msg.content, blobUrlRegistryRef.current)),
              })),
              n: 1,
            },
            {
              onData: (text) => {
                // 同 L1500 的注释：流式 chunk 里的 base64 立刻转 blob URL，
                // 否则 localContent / accumulated 的大字符串会拖慢 React state。
                localContent += replaceBase64WithBlobUrls(text, blobUrlRegistryRef.current);
                if (messageHasGeneratedImage(localContent)) appendLocalContent();
              },
              onReasoning: (text) => {
                accumulatedReasoning += text;
                setStreamReasoning(accumulatedReasoning);
              },
              onDone: (usage) => {
                appendLocalContent();
                resolve(usage);
              },
              onError: (err) => reject(new Error(err)),
            },
            abort.signal,
          ).catch(reject);
        }));

        const results = await Promise.allSettled(requests);
        if (abort.signal.aborted) return;
        const fulfilledUsages = results.flatMap(result => result.status === 'fulfilled' ? [result.value] : []);
        const failedCount = results.length - fulfilledUsages.length;
        if (failedCount && !accumulated) {
          const failure = results.find(result => result.status === 'rejected');
          throw failure?.status === 'rejected' && failure.reason instanceof Error ? failure.reason : new Error('stream failed');
        }
        const usage = fulfilledUsages.reduce((total, item) => ({
          input_tokens: total.input_tokens + item.input_tokens,
          output_tokens: total.output_tokens + item.output_tokens,
          model: item.model || total.model,
          cost: total.cost + item.cost,
        }), planUsage);
        await finishAssistantResponse(usage);
        if (failedCount && activeIdRef.current === conversationID) {
          setError(`${failedCount} image${failedCount === 1 ? '' : 's'} failed to generate`);
        }
        return;
      }

      await chatCompletionsStream(
        platform,
        {
          ...baseRequest,
        },
        {
          onData: (text) => {
            // 4K 图：text 一帧可能含 ~6-20 MB base64 markdown，先转 blob URL 再累加，
            // 否则 accumulated 含大字符串 → 每次 setStreamContent 都 React state 拷贝，卡爆。
            accumulated += replaceBase64WithBlobUrls(text, blobUrlRegistryRef.current);
            setStreamContent(accumulated);
          },
          onReasoning: (text) => {
            accumulatedReasoning += text;
            setStreamReasoning(accumulatedReasoning);
          },
          onDone: finishAssistantResponse,
          onError: (err) => {
            if (activeIdRef.current === conversationID) {
              setError(err);
              setRetryRequest(nextRetryRequest);
            }
            setIsStreaming(false);
            setStreamContent('');
            setStreamReasoning('');
            setStreamConversationId(null);
            streamContextRef.current = null;
          },
        },
        abort.signal,
      );
    } catch (e) {
      if (activeIdRef.current === conversationID) {
        setError(e instanceof Error ? e.message : 'stream failed');
        setRetryRequest(nextRetryRequest);
      }
      setIsStreaming(false);
      setStreamContent('');
      setStreamReasoning('');
      setStreamConversationId(null);
      streamContextRef.current = null;
    }
  }, [reasoningEffort, t]);

  const sendMessage = useCallback(async () => {
    if ((!input.trim() && pendingImages.length === 0) || isStreaming || !activeId) return;

    pendingRefocusRef.current = true;
    const content = messageContentWithImages(input, pendingImages);
    const groupID = resolveGroupID();
    let conversationID = activeId;
    const requestMessages = [...messages, {
      id: Date.now(),
      conversation_id: activeId,
      role: 'user',
      content,
      reasoning_effort: selectedModelSupportsReasoning ? reasoningEffort : undefined,
      platform: selectedPlatform,
      model: selectedModelID,
      group_id: groupID,
      input_tokens: 0,
      output_tokens: 0,
      cost: 0,
      created_at: new Date().toISOString(),
    }];

    setInput('');
    setPendingImages([]);
    if (inputRef.current) {
      inputRef.current.style.height = '24px';
    }
    if (fileInputRef.current) {
      fileInputRef.current.value = '';
    }
    setError('');
    setRetryRequest(null);
    setMessages(requestMessages);
    setIsStreaming(true);
    setStreamConversationId(conversationID);
    streamContextRef.current = { conversationId: conversationID, model: selectedModelID };
    setStreamContent('');
    setStreamReasoning('');

    try {
      if (!selectedPlatform || !selectedModelID) {
        throw new Error('Model required');
      }
      if (conversationID === DRAFT_CONVERSATION_ID) {
        const conv = await api.createConversation({
          title: '',
          group_id: groupID,
          platform: selectedPlatform,
          model: selectedModelID,
        });
        conversationID = conv.id;
        if (activeIdRef.current === DRAFT_CONVERSATION_ID) {
          activeIdRef.current = conv.id;
          skipNextMessagesLoadRef.current = conv.id;
          setActiveId(conv.id);
          setMessages(prev => prev.map(msg => ({ ...msg, conversation_id: conv.id })));
        }
        setConversations(prev => [conv, ...prev.filter(c => c.id !== DRAFT_CONVERSATION_ID)]);
      }
      await api.persistMessage({
        conversation_id: conversationID,
        role: 'user',
        content,
        reasoning_effort: selectedModelSupportsReasoning ? reasoningEffort : undefined,
        platform: selectedPlatform,
        model: selectedModelID,
        group_id: groupID,
      });

      await streamAssistantResponse({
        conversationID,
        requestMessages: requestMessages.map(msg => ({ ...msg, conversation_id: conversationID })),
        model: selectedModelID,
        groupID,
        platform: selectedPlatform,
        isImageRequest: selectedModelIsImage,
        imageSize: selectedModelIsImage ? resolvedImageSize : undefined,
        supportsReasoning: selectedModelSupportsReasoning,
        reasoningEffort,
        titleContent: content,
      });
    } catch (e) {
      if (activeIdRef.current === conversationID) {
        setError(e instanceof Error ? e.message : 'stream failed');
      }
      setIsStreaming(false);
      setStreamContent('');
      setStreamReasoning('');
      setStreamConversationId(null);
      streamContextRef.current = null;
    }
  }, [activeId, input, isStreaming, messages, pendingImages, reasoningEffort, resolveGroupID, resolvedImageSize, selectedPlatform, selectedModelID, selectedModelIsImage, selectedModelSupportsReasoning, streamAssistantResponse]);

  const addImageFiles = useCallback(async (files: File[]) => {
    if (!files.length) return;

    try {
      const nextImages = await imagesFromFiles(files);
      if (!nextImages.length) return;
      setPendingImages(prev => [...prev, ...nextImages]);
      setError('');
      setRetryRequest(null);
    } catch (err) {
      setRetryRequest(null);
      setError(err instanceof Error ? err.message : 'Failed to read image');
    }
  }, []);

  const selectEditImage = useCallback(async (file?: File) => {
    if (!file) return;
    try {
      const nextSource = await editImageFromFile(file);
      setEditSource(nextSource);
      setIsEditPanelOpen(true);
      setEditSelection(null);
      setDraftEditSelection(null);
      setEditStageSize(null);
      setError('');
      setRetryRequest(null);
    } catch (err) {
      setRetryRequest(null);
      setError(err instanceof Error ? err.message : 'Failed to read image');
    }
  }, []);

  const handleImageChange = useCallback(async (e: React.ChangeEvent<HTMLInputElement>) => {
    await addImageFiles(Array.from(e.target.files || []));
    e.target.value = '';
  }, [addImageFiles]);

  const handleEditImageChange = useCallback(async (e: React.ChangeEvent<HTMLInputElement>) => {
    await selectEditImage(e.target.files?.[0]);
    e.target.value = '';
  }, [selectEditImage]);

  const editGeneratedImage = useCallback(async (
    url: string,
    alt: string,
    sourceModel?: string,
    sourcePlatform?: string,
  ) => {
    if (isStreaming) return;
    try {
      const nextSource = await editImageFromUrl(url, alt);

      // Ensure an image-only model is selected so the edit panel renders and submit is enabled.
      if (!selectedModelIsImage) {
        const fromAssistant = sourceModel
          ? models.find(m => m.id === sourceModel
              && (!sourcePlatform || m.platform === sourcePlatform)
              && isImageModel(m))
          : undefined;
        const fallback = fromAssistant || models.find(isImageModel);
        if (fallback) {
          setSelectedModel(modelOptionValue(fallback));
        } else {
          setError(t('playground.no_image_model_available', {
            defaultValue: 'No image model available — pick one to edit this image.',
          }));
          return;
        }
      }

      setEditSource(nextSource);
      setIsEditPanelOpen(true);
      setEditSelection(null);
      setDraftEditSelection(null);
      setEditStageSize(null);
      setError('');
      setRetryRequest(null);
      pendingRefocusRef.current = true;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load image');
    }
  }, [isStreaming, models, selectedModelIsImage, t]);

  const triggerEditImagePicker = useCallback(() => {
    editFileInputRef.current?.click();
  }, []);

  const clearEditSelection = useCallback(() => {
    setEditSelection(null);
    setDraftEditSelection(null);
    selectionStartRef.current = null;
  }, []);

  const closeEditPanel = useCallback(() => {
    setIsEditPanelOpen(false);
    setEditSource(null);
    setEditSelection(null);
    setDraftEditSelection(null);
    setEditStageSize(null);
    selectionStartRef.current = null;
  }, []);

  useEffect(() => {
    if (!isEditPanelOpen) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeEditPanel();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [isEditPanelOpen, closeEditPanel]);

  const selectionPointFromEvent = useCallback((event: React.PointerEvent<HTMLCanvasElement>) => {
    const canvas = editCanvasRef.current;
    if (!canvas) return null;
    const rect = canvas.getBoundingClientRect();
    const scaleX = canvas.width / rect.width;
    const scaleY = canvas.height / rect.height;
    return {
      x: clampNumber((event.clientX - rect.left) * scaleX, 0, canvas.width),
      y: clampNumber((event.clientY - rect.top) * scaleY, 0, canvas.height),
    };
  }, []);

  const handleSelectionPointerDown = useCallback((event: React.PointerEvent<HTMLCanvasElement>) => {
    event.preventDefault();
    const point = selectionPointFromEvent(event);
    if (!point) return;
    selectionStartRef.current = point;
    setEditSelection(null);
    setDraftEditSelection({ x: point.x, y: point.y, width: 0, height: 0 });
    event.currentTarget.setPointerCapture(event.pointerId);
  }, [selectionPointFromEvent]);

  const handleSelectionPointerMove = useCallback((event: React.PointerEvent<HTMLCanvasElement>) => {
    const start = selectionStartRef.current;
    if (!start) return;
    event.preventDefault();
    const point = selectionPointFromEvent(event);
    if (point) setDraftEditSelection(selectionRectFromPoints(start, point));
  }, [selectionPointFromEvent]);

  const finishSelectionDrag = useCallback((event: React.PointerEvent<HTMLCanvasElement>) => {
    const start = selectionStartRef.current;
    const point = selectionPointFromEvent(event);
    const selection = start && point ? selectionRectFromPoints(start, point) : draftEditSelection;
    selectionStartRef.current = null;
    setDraftEditSelection(null);
    setEditSelection(isUsableSelection(selection) ? selection : null);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
  }, [draftEditSelection, selectionPointFromEvent]);

  const createEditMaskBlob = useCallback(async (selection: EditSelectionRect) => {
    const canvas = editCanvasRef.current;
    if (!canvas || !editSource) throw new Error('Selection required');

    const image = await loadImageElement(editSource.url);
    const maskCanvas = document.createElement('canvas');
    maskCanvas.width = image.naturalWidth;
    maskCanvas.height = image.naturalHeight;
    const ctx = maskCanvas.getContext('2d');
    if (!ctx) throw new Error('Failed to create mask');

    const scaleX = image.naturalWidth / canvas.width;
    const scaleY = image.naturalHeight / canvas.height;
    ctx.fillStyle = '#fff';
    ctx.fillRect(0, 0, maskCanvas.width, maskCanvas.height);
    ctx.clearRect(
      Math.floor(selection.x * scaleX),
      Math.floor(selection.y * scaleY),
      Math.ceil(selection.width * scaleX),
      Math.ceil(selection.height * scaleY),
    );
    return canvasToBlob(maskCanvas);
  }, [editSource]);

  const submitImageEdit = useCallback(async () => {
    if (!activeId || isStreaming || isEditingImage) return;
    pendingRefocusRef.current = true;
    if (!selectedPlatform || !selectedModelID) {
      setRetryRequest(null);
      setError(t('playground.select_model_first'));
      return;
    }
    if (!selectedModelIsImage) {
      setRetryRequest(null);
      setError(t('playground.select_image_model_first'));
      return;
    }
    if (!editSource) {
      setRetryRequest(null);
      setError(t('playground.choose_source_image_first'));
      return;
    }
    if (!editSelection) {
      setRetryRequest(null);
      setError(t('playground.select_image_region_first', { defaultValue: 'Select a region to edit first.' }));
      return;
    }

    const prompt = input.trim();
    if (!prompt) {
      setRetryRequest(null);
      setError(t('playground.describe_image_change_first'));
      return;
    }

    const groupID = resolveGroupID();
    let conversationID = activeId;
    const canvas = editCanvasRef.current;
    const selection = editSelection;
    const editAnnotation = canvas ? {
      imageIndex: 0,
      rect: {
        x: selection.x / canvas.width,
        y: selection.y / canvas.height,
        width: selection.width / canvas.width,
        height: selection.height / canvas.height,
      },
    } : null;
    const userContent = messageContentWithImages(prompt, [editSource], editAnnotation ? [editAnnotation] : []);
    const localUserMessage: Message = {
      id: Date.now(),
      conversation_id: activeId,
      role: 'user',
      content: userContent,
      platform: selectedPlatform,
      model: selectedModelID,
      group_id: groupID,
      input_tokens: 0,
      output_tokens: 0,
      cost: 0,
      created_at: new Date().toISOString(),
    };

    setInput('');
    if (inputRef.current) inputRef.current.style.height = '24px';
    setError('');
    setRetryRequest(null);
    setMessages(prev => [...prev, localUserMessage]);
    setIsStreaming(true);
    setIsEditingImage(true);
    setStreamConversationId(conversationID);
    streamContextRef.current = { conversationId: conversationID, model: selectedModelID };
    setStreamContent('');
    setStreamReasoning('');

    try {
      if (conversationID === DRAFT_CONVERSATION_ID) {
        const conv = await api.createConversation({
          title: '',
          group_id: groupID,
          platform: selectedPlatform,
          model: selectedModelID,
        });
        conversationID = conv.id;
        if (activeIdRef.current === DRAFT_CONVERSATION_ID) {
          activeIdRef.current = conv.id;
          skipNextMessagesLoadRef.current = conv.id;
          setActiveId(conv.id);
          setMessages(prev => prev.map(msg => ({ ...msg, conversation_id: conv.id })));
        }
        setStreamConversationId(conv.id);
        streamContextRef.current = { conversationId: conv.id, model: selectedModelID };
        setConversations(prev => [conv, ...prev.filter(c => c.id !== DRAFT_CONVERSATION_ID)]);
      }

      const persistedUser = await api.persistMessage({
        conversation_id: conversationID,
        role: 'user',
        content: userContent,
        platform: selectedPlatform,
        model: selectedModelID,
        group_id: groupID,
      });
      if (activeIdRef.current === conversationID) {
        setMessages(prev => prev.map(msg => (msg.id === localUserMessage.id ? persistedUser : msg)));
      }

      const abort = new AbortController();
      abortRef.current = abort;
      const maskBlob = await createEditMaskBlob(selection);
      if (abort.signal.aborted) return;

      const form = new FormData();
      form.append('model', selectedModelID);
      form.append('prompt', prompt);
      form.append('image', editSource.file, editSource.name || 'image.png');
      form.append('mask', maskBlob, 'mask.png');
      if (resolvedImageSize) form.append('size', resolvedImageSize);

      const response = await requestImageEdit(selectedPlatform, form, abort.signal);
      if (abort.signal.aborted) return;
      const assistantContent = imageEditAssistantContent(response, 'edited-image');
      if (!assistantContent) throw new Error('No image returned');
      const usage = imageEditUsage(response);
      const persistedAssistant = await api.persistMessage({
        conversation_id: conversationID,
        role: 'assistant',
        content: assistantContent,
        platform: selectedPlatform,
        model: response.model || selectedModelID,
        group_id: groupID,
        input_tokens: usage.input_tokens,
        output_tokens: usage.output_tokens,
        cost: usage.cost,
      });

      if (activeIdRef.current === conversationID) {
        setMessages(prev => [...prev, persistedAssistant]);
      }
      setConversations(prev => prev.map(c =>
        c.id === conversationID && !c.title
          ? { ...c, title: titleFromMessageContent(userContent), updated_at: new Date().toISOString() }
          : c
      ));
      setEditSource(null);
      setIsEditPanelOpen(false);
      setEditSelection(null);
      setDraftEditSelection(null);
      setEditStageSize(null);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      if (activeIdRef.current === conversationID) {
        setError(e instanceof Error ? e.message : 'image edit failed');
      }
    } finally {
      setIsStreaming(false);
      setIsEditingImage(false);
      setStreamContent('');
      setStreamReasoning('');
      setStreamConversationId(null);
      streamContextRef.current = null;
    }
  }, [activeId, createEditMaskBlob, editSelection, editSource, input, isEditingImage, isStreaming, resolveGroupID, resolvedImageSize, selectedModelID, selectedModelIsImage, selectedPlatform, t]);

  const handlePaste = useCallback((e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    const files = Array.from(e.clipboardData.items)
      .filter(item => item.kind === 'file' && item.type.startsWith('image/'))
      .map(item => item.getAsFile())
      .filter((file): file is File => Boolean(file));

    if (files.length) {
      void addImageFiles(files);
    }
  }, [addImageFiles]);

  const removePendingImage = useCallback((id: string) => {
    setPendingImages(prev => prev.filter(image => image.id !== id));
  }, []);

  const stopStreaming = useCallback(() => {
    pendingRefocusRef.current = true;
    abortRef.current?.abort();
    const streamContext = streamContextRef.current;
    if (streamContent || streamReasoning) {
      const conversationId = streamContext?.conversationId;
      if (conversationId && activeIdRef.current === conversationId) {
        setMessages(prev => [...prev, {
          id: Date.now() + 1,
          conversation_id: conversationId,
          role: 'assistant',
          content: streamContent,
          reasoning: streamReasoning,
          platform: '', model: streamContext.model, group_id: 0,
          input_tokens: 0, output_tokens: 0, cost: 0,
          created_at: new Date().toISOString(),
        }]);
      }
    }
    setStreamContent('');
    setStreamReasoning('');
    setStreamConversationId(null);
    streamContextRef.current = null;
    setIsEditingImage(false);
    setIsStreaming(false);
  }, [streamContent, streamReasoning]);

  const activeConv = conversations.find(c => c.id === activeId);
  const lastMessage = messages[messages.length - 1];
  const visibleEditSelection = draftEditSelection || editSelection;
  const isImageEditReady = Boolean(isEditPanelOpen && editSource && editSelection && input.trim() && selectedPlatform && selectedModelID && selectedModelIsImage);
  const hasRecoverableUserMessage = Boolean(activeId && activeId !== DRAFT_CONVERSATION_ID && lastMessage?.role === 'user' && !error && !isStreaming);
  const isActiveConversationStreaming = isStreaming && streamConversationId === activeId;
  const canSendMessage = Boolean((isEditPanelOpen ? isImageEditReady : (input.trim() || pendingImages.length > 0)) && selectedPlatform && selectedModelID) && !isStreaming && !isEditingImage;

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      if (!selectedPlatform || !selectedModelID) {
        setRetryRequest(null);
        setError(t('playground.select_model_first'));
        return;
      }
      if (isEditPanelOpen) {
        void submitImageEdit();
        return;
      }
      sendMessage();
    }
  }, [isEditPanelOpen, selectedModelID, selectedPlatform, sendMessage, submitImageEdit, t]);

  const triggerImagePicker = useCallback(() => {
    fileInputRef.current?.click();
  }, []);

  const autoResize = useCallback((el: HTMLTextAreaElement) => {
    el.style.height = 'auto';
    el.style.height = Math.min(el.scrollHeight, 200) + 'px';
  }, []);

  const openConversation = useCallback((id: number) => {
    setActiveId(id);
    setError('');
    setRetryRequest(null);
    if (isMobile) {
      setSidebarOpen(false);
    }
  }, [isMobile]);

  const regenerateLastResponse = useCallback(() => {
    if (!retryRequest || isStreaming || activeId !== retryRequest.conversationID) return;

    const request = retryRequest;
    setError('');
    setRetryRequest(null);
    void streamAssistantResponse({
      ...request,
      requestMessages: request.requestMessages.map(msg => ({ ...msg })),
    });
  }, [activeId, isStreaming, retryRequest, streamAssistantResponse]);

  const regenerateUnfinishedResponse = useCallback(() => {
    if (isStreaming || !activeId || activeId === DRAFT_CONVERSATION_ID) return;
    const lastUserMessage = messages[messages.length - 1];
    if (lastUserMessage?.role !== 'user') return;

    const requestModel = lastUserMessage.model || activeConv?.model || selectedModelID;
    const requestPlatform = lastUserMessage.platform || activeConv?.platform || selectedPlatform;
    if (!requestModel || !requestPlatform) {
      setError('Model required');
      return;
    }

    const requestModelInfo = models.find(item => item.id === requestModel && item.platform === requestPlatform) || models.find(item => item.id === requestModel);
    const isImageRequest = isImageModel(requestModelInfo) || isImageModelIdentifier(requestModel);
    const shouldUseReasoning = supportsReasoning(requestModelInfo) || Boolean(lastUserMessage.reasoning_effort);
    setError('');
    setRetryRequest(null);
    void streamAssistantResponse({
      conversationID: activeId,
      requestMessages: messages.map(msg => ({ ...msg })),
      model: requestModel,
      groupID: lastUserMessage.group_id || activeConv?.group_id || resolveGroupID(),
      platform: requestPlatform,
      isImageRequest,
      imageSize: isImageRequest ? resolvedImageSize : undefined,
      supportsReasoning: shouldUseReasoning,
      reasoningEffort: lastUserMessage.reasoning_effort || reasoningEffort,
    });
  }, [activeConv, activeId, isStreaming, messages, models, reasoningEffort, resolveGroupID, resolvedImageSize, selectedModelID, selectedPlatform, streamAssistantResponse]);

  const handleImagePreview = useCallback((url: string, alt: string) => {
    setPreviewImage({ url, alt });
  }, []);

  const handleImageDownload = useCallback((url: string, alt: string) => {
    void downloadImage(url, alt)
      .then(() => setInteractionNotice(t('playground.download_started')))
      .catch(() => setInteractionNotice(t('playground.download_failed')));
  }, [t]);

  const regenerateImage = useCallback((messageIndex: number) => {
    if (isStreaming || !activeId || activeId === DRAFT_CONVERSATION_ID) return;

    const sourceIndex = messages.slice(0, messageIndex).map(msg => msg.role).lastIndexOf('user');
    if (sourceIndex < 0) {
      setRetryRequest(null);
      setError(t('playground.no_image_prompt'));
      return;
    }

    const sourceMessages = messages.slice(0, sourceIndex + 1);
    const sourceMessage = messages[sourceIndex];
    const assistantMessage = messages[messageIndex];
    const requestModel = assistantMessage.model || selectedModelID;
    const requestPlatform = assistantMessage.platform || sourceMessage.platform || selectedPlatform;
    const requestModelInfo = models.find(item => item.id === requestModel && item.platform === requestPlatform) || models.find(item => item.id === requestModel);
    void streamAssistantResponse({
      conversationID: activeId,
      requestMessages: sourceMessages,
      model: requestModel,
      groupID: assistantMessage.group_id || sourceMessage.group_id || resolveGroupID(),
      platform: requestPlatform,
      isImageRequest: true,
      imageSize: resolvedImageSize,
      supportsReasoning: supportsReasoning(requestModelInfo),
    });
  }, [activeId, isStreaming, messages, models, resolveGroupID, resolvedImageSize, selectedPlatform, selectedModelID, streamAssistantResponse, t]);

  const handleMessageCopy = useCallback((content: string) => {
    void copyText(content)
      .then(() => setInteractionNotice('Message copied'))
      .catch(() => setInteractionNotice('Copy failed'));
  }, []);

  const interactiveMessageOptions = {
    onImagePreview: handleImagePreview,
    imagePreviewTitle: t('playground.preview_image'),
    generatedImageAlt: t('playground.generated_image'),
    isMobile,
  };

  const renderCopyButton = (content: string, label = 'Copy message', preventToggle = false, buttonStyle: CSSProperties = {}) => (
    <button
      type="button"
      style={{ ...styles.messageCopyBtn, ...buttonStyle }}
      title={label}
      aria-label={label}
      onClick={(event) => {
        if (preventToggle) {
          event.preventDefault();
          event.stopPropagation();
        }
        handleMessageCopy(content);
      }}
    >
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
        <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
      </svg>
    </button>
  );

  const renderCopyableMessageContent = (targetID: string, content: string) => {
    const copyVisible = isMobile || hoveredCopyTarget === targetID;
    const copyableText = copyableMessageText(content);
    const showCopyButton = hasCopyableMessageText(content);
    const trailingInlineAction = showCopyButton ? (
      <span style={{
        ...styles.messageCopyAfterText,
        ...(copyVisible ? styles.messageCopyAfterTextVisible : null),
      }}>
        {renderCopyButton(copyableText, 'Copy message', false, styles.messageCopyAfterTextBtn)}
      </span>
    ) : undefined;

    return (
      <div
        style={styles.messageContent}
        onMouseEnter={() => setHoveredCopyTarget(targetID)}
        onMouseLeave={() => setHoveredCopyTarget(current => (current === targetID ? null : current))}
        onFocus={() => setHoveredCopyTarget(targetID)}
        onBlur={event => {
          if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
            setHoveredCopyTarget(current => (current === targetID ? null : current));
          }
        }}
      >
        {renderMessageContent(content, {
          ...interactiveMessageOptions,
          trailingInlineAction,
        })}
      </div>
    );
  };

  if (IMAGE_STUDIO_ENABLED && studioMode) {
    return (
      <ImageStudioPage
        onExit={() => setStudioMode(false)}
        userInfo={userInfo}
        onUserInfoChange={setUserInfo}
      />
    );
  }

  return (
    <div data-full-bleed data-pg-aesthetic style={styles.layout}>
      {sidebarOpen && isMobile && (
        <div
          style={styles.sidebarBackdrop}
          onClick={() => setSidebarOpen(false)}
        />
      )}

      {previewImage && (
        <div
          style={styles.imagePreviewOverlay}
          role="dialog"
          aria-modal="true"
          aria-label={previewImage.alt || t('playground.image_preview')}
          onClick={() => setPreviewImage(null)}
        >
          <div style={styles.imagePreviewModal} onClick={(event) => event.stopPropagation()}>
            <img src={previewImage.url} alt={previewImage.alt} style={styles.imagePreviewLarge} />
            <button
              type="button"
              style={styles.imagePreviewCloseBtn}
              onClick={() => setPreviewImage(null)}
              aria-label={t('playground.close_image_preview')}
            >
              ×
            </button>
          </div>
        </div>
      )}

      {selectedModelIsImage && isEditPanelOpen && (
        <div
          style={styles.editModalOverlay}
          role="dialog"
          aria-modal="true"
          aria-label={t('playground.edit_image_region')}
          onClick={closeEditPanel}
        >
          <div
            style={{ ...styles.editModalCard, ...(isMobile ? styles.editModalCardMobile : null) }}
            onClick={event => event.stopPropagation()}
          >
            <div style={styles.editModalHeader}>
              <div style={styles.imageEditTitleWrap}>
                <span style={styles.imageEditTitle}>{t('playground.edit_image_region')}</span>
                <span style={styles.imageEditSubtitle}>
                  {editSource
                    ? t('playground.edit_image_modal_hint', { defaultValue: 'Drag a region on the image, then describe the change for that area.' })
                    : t('playground.choose_source_image_region_hint')}
                </span>
              </div>
              <div style={styles.imageEditHeaderActions}>
                <button
                  type="button"
                  style={styles.imageEditGhostBtn}
                  onClick={triggerEditImagePicker}
                  disabled={isActiveConversationStreaming}
                >
                  {editSource ? t('playground.replace_source') : t('playground.choose_source')}
                </button>
                <button
                  type="button"
                  style={styles.imageEditIconBtn}
                  onClick={closeEditPanel}
                  aria-label={t('playground.close_image_preview', { defaultValue: 'Close' })}
                >
                  ×
                </button>
              </div>
            </div>

            {editSource ? (
              <div style={{ ...styles.editModalBody, ...(isMobile ? styles.editModalBodyMobile : null) }}>
                <div ref={editCanvasContainerRef} style={styles.editModalStageWrap}>
                  <div style={{
                    ...styles.imageEditStage,
                    ...(editStageSize ? { width: editStageSize.width, height: editStageSize.height } : null),
                  }}>
                    <img src={editSource.url} alt={editSource.name} style={styles.imageEditSource} draggable={false} />
                    {visibleEditSelection && (
                      <div
                        style={{
                          ...styles.imageEditSelection,
                          left: visibleEditSelection.x,
                          top: visibleEditSelection.y,
                          width: visibleEditSelection.width,
                          height: visibleEditSelection.height,
                        }}
                      />
                    )}
                    <canvas
                      ref={editCanvasRef}
                      style={styles.imageEditCanvas}
                      onPointerDown={handleSelectionPointerDown}
                      onPointerMove={handleSelectionPointerMove}
                      onPointerUp={finishSelectionDrag}
                      onPointerCancel={finishSelectionDrag}
                      aria-label="Box-select image edit region"
                    />
                  </div>
                </div>
                <div style={styles.editModalSide}>
                  <div style={styles.imageEditBadge}>
                    {editSelection ? t('playground.region_selected') : t('playground.drag_to_select')}
                  </div>
                  <div style={styles.imageEditFilename}>{editSource.name}</div>
                  <button
                    type="button"
                    style={{ ...styles.imageEditGhostBtn, opacity: editSelection ? 1 : 0.5 }}
                    onClick={clearEditSelection}
                    disabled={!editSelection || isActiveConversationStreaming}
                  >
                    {t('playground.clear_selection')}
                  </button>
                </div>
              </div>
            ) : (
              <button
                type="button"
                style={styles.imageEditEmptyBtn}
                onClick={triggerEditImagePicker}
                disabled={isActiveConversationStreaming}
              >
                {t('playground.choose_source_image_for_regional_editing')}
              </button>
            )}

            <div style={styles.editModalFooter}>
              {isActiveConversationStreaming && (
                <div style={styles.editModalStatus}>
                  <span style={styles.streamingDot} />
                  <span>{t('playground.edit_modal_generating_bg', { defaultValue: 'Generating edit — this can take 10–30 seconds. You can close this dialog; the result will appear in chat when ready.' })}</span>
                </div>
              )}
              {error && !isActiveConversationStreaming && (
                <div style={styles.editModalError}>
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <circle cx="12" cy="12" r="10" />
                    <path d="M12 8v4m0 4h.01" />
                  </svg>
                  <span>{error}</span>
                </div>
              )}
              <textarea
                style={styles.editModalPrompt}
                value={input}
                onChange={e => setInput(e.target.value)}
                onKeyDown={handleKeyDown}
                placeholder={t('playground.edit_prompt_placeholder', { defaultValue: 'Describe the change you want — e.g. "make the sky overcast" or "remove the person on the left".' })}
                rows={3}
                disabled={isActiveConversationStreaming}
                autoFocus
              />
              <div style={styles.editModalActions}>
                <span style={styles.editModalHint}>
                  {editSelection
                    ? t('playground.edit_modal_region_hint', { defaultValue: 'Region edit · only the selected area will change' })
                    : t('playground.edit_modal_full_hint', { defaultValue: 'Select a region before generating an edit' })}
                </span>
                <div style={styles.editModalBtnGroup}>
                  <button
                    type="button"
                    style={styles.imageEditGhostBtn}
                    onClick={closeEditPanel}
                  >
                    {isActiveConversationStreaming
                      ? t('playground.edit_modal_run_in_background', { defaultValue: 'Run in background' })
                      : t('playground.cancel', { defaultValue: 'Cancel' })}
                  </button>
                  {isActiveConversationStreaming ? (
                    <button
                      type="button"
                      style={styles.editModalSubmitBtn}
                      onClick={stopStreaming}
                    >
                      <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                        <rect x="2" y="2" width="8" height="8" rx="1" />
                      </svg>
                      {t('playground.stop')}
                    </button>
                  ) : (
                    <button
                      type="button"
                      style={{ ...styles.editModalSubmitBtn, opacity: isImageEditReady ? 1 : 0.4 }}
                      onClick={() => void submitImageEdit()}
                      disabled={!isImageEditReady}
                    >
                      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <path d="M22 2L11 13" />
                        <path d="M22 2l-7 20-4-9-9-4 20-7z" />
                      </svg>
                      {t('playground.edit_modal_submit', { defaultValue: 'Generate region edit' })}
                    </button>
                  )}
                </div>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* ── Sidebar ── */}
      {sidebarOpen ? (
        <div style={{ ...styles.sidebar, ...(isMobile ? styles.sidebarMobile : null) }}>
          <div style={styles.sidebarHeader}>
            <div style={styles.sidebarTitleGroup}>
              <button style={styles.toggleBtn} onClick={() => setSidebarOpen(false)} aria-label="Collapse conversations">
                <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
                  <path d="M6 2v12" /><path d="M2 2h12v12H2z" /><path d="M10 6l-2 2 2 2" />
                </svg>
              </button>
              <span style={styles.sidebarTitle}>{t('playground.conversations')}</span>
            </div>
            <button
              style={styles.newBtn}
              onClick={createConversation}
              title={t('playground.new_conversation')}
              aria-label={t('playground.new_conversation')}
            >
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round">
                <path d="M7 1v12M1 7h12" />
              </svg>
            </button>
          </div>

          {IMAGE_STUDIO_ENABLED && (
            <div style={styles.modeSwitcher}>
              <button
                type="button"
                style={{ ...styles.modeSwitcherItem, ...styles.modeSwitcherItemActive }}
                aria-pressed={true}
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
                </svg>
                <span>{t('playground.mode_chat', { defaultValue: 'Chat' })}</span>
              </button>
              <button
                type="button"
                style={styles.modeSwitcherItem}
                onClick={() => setStudioMode(true)}
                title={t('playground.mode_studio_hint', { defaultValue: 'Open Image Studio' })}
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <rect x="3" y="3" width="18" height="18" rx="2" />
                  <circle cx="8.5" cy="8.5" r="1.5" />
                  <path d="M21 15l-5-5L5 21" />
                </svg>
                <span>{t('playground.mode_studio', { defaultValue: 'Studio' })}</span>
              </button>
            </div>
          )}

          <div style={styles.convList}>
            {conversations.map(c => {
              const isActive = c.id === activeId;
              return (
                <div
                  key={c.id}
                  className={`pg-conv-item${isActive ? ' is-active' : ''}`}
                  style={{
                    ...styles.convItem,
                    background: isActive ? cssVar('bgHover') : 'transparent',
                  }}
                  onClick={() => openConversation(c.id)}
                >
                  <span style={{
                    ...styles.convTitle,
                    color: isActive ? cssVar('text') : cssVar('textSecondary'),
                    fontWeight: isActive ? 500 : 400,
                  }}>
                    {c.title || t('playground.new_conversation')}
                  </span>
                  <button
                    type="button"
                    className="pg-conv-delete"
                    style={styles.deleteBtn}
                    onClick={(e) => { e.stopPropagation(); deleteConversation(c.id); }}
                    title={t('playground.delete_conversation')}
                    aria-label={t('playground.delete_conversation')}
                  >
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <path d="M3 6h18" />
                      <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                      <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
                      <path d="M10 11v6" />
                      <path d="M14 11v6" />
                    </svg>
                  </button>
                </div>
              );
            })}
            {conversations.length === 0 && (
              <div style={styles.emptyConvList}>
                <span>{t('playground.no_conversations')}</span>
              </div>
            )}
          </div>

          {userInfo && (
            <div style={styles.balanceBar}>
              <span style={styles.balanceLabel}>{t('playground.balance')}</span>
              <span style={styles.balanceValue}>${userInfo.balance.toFixed(4)}</span>
            </div>
          )}
        </div>
      ) : (
        <div style={{ ...styles.sidebarRail, ...(isMobile ? styles.sidebarRailMobile : null) }}>
          <button style={styles.toggleBtn} onClick={() => setSidebarOpen(true)} aria-label="Expand conversations">
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
              <path d="M6 2v12" /><path d="M2 2h12v12H2z" /><path d="M8 6l2 2-2 2" />
            </svg>
          </button>
          {IMAGE_STUDIO_ENABLED && (
          <button
            style={{ ...styles.toggleBtn, marginTop: 4 }}
            onClick={() => setStudioMode(true)}
            aria-label={t('playground.mode_studio_hint', { defaultValue: 'Open Image Studio' })}
            title={t('playground.mode_studio_hint', { defaultValue: 'Open Image Studio' })}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <rect x="3" y="3" width="18" height="18" rx="2" />
              <circle cx="8.5" cy="8.5" r="1.5" />
              <path d="M21 15l-5-5L5 21" />
            </svg>
          </button>
          )}
        </div>
      )}

      {/* ── Main ── */}
      <div style={styles.main}>
        {/* Messages */}
        <div style={styles.messagesArea}>
          {!activeId && (
            <div style={{ ...styles.emptyState, ...(isMobile ? styles.emptyStateMobile : null) }}>
              <div style={styles.emptyTitle}>{t('playground.empty_title')}</div>
              <div style={styles.emptyDesc}>{t('playground.empty_description')}</div>
              <button style={styles.emptyBtn} onClick={createConversation}>
                {t('playground.new_conversation')}
              </button>
            </div>
          )}

          {activeId && messages.map((msg, messageIndex) => {
            const isUser = msg.role === 'user';
            return (
            <div
              key={msg.id}
              style={{
                ...styles.messageRow,
                ...(isMobile ? styles.messageRowMobile : null),
                ...(isUser ? styles.messageRowUser : styles.messageRowAssistant),
              }}
            >
              <div style={isUser ? { ...styles.userBubble, ...(isMobile ? styles.userBubbleMobile : null) } : styles.assistantBlock}>
                {!isUser && msg.reasoning && (
                  <details style={styles.reasoningBox} open>
                    <summary style={styles.reasoningSummary}>
                      <span>Thinking</span>
                      {renderCopyButton(msg.reasoning, 'Copy thinking', true)}
                    </summary>
                    <div style={styles.reasoningContent}>
                      {renderMessageContent(msg.reasoning, interactiveMessageOptions)}
                    </div>
                  </details>
                )}
                {renderCopyableMessageContent(`message-${msg.id}`, msg.content)}
                {!isUser && (messageHasGeneratedImage(msg.content) || msg.model) && (() => {
                  const generatedImage = firstGeneratedImage(msg.content);
                  return (
                    <div style={messageHasGeneratedImage(msg.content) ? styles.imageMessageActions : styles.messageMeta}>
                      {generatedImage && (
                        <button
                          type="button"
                          style={styles.imageDownloadBtn}
                          title={t('playground.download_image')}
                          aria-label={t('playground.download_image')}
                          onClick={() => handleImageDownload(generatedImage.url, generatedImage.alt || t('playground.generated_image'))}
                        >
                          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                            <path d="M7 10l5 5 5-5" />
                            <path d="M12 15V3" />
                          </svg>
                        </button>
                      )}
                      {generatedImage && (
                        <button
                          type="button"
                          style={{ ...styles.regenerateImageBtn, opacity: isStreaming ? 0.5 : 1 }}
                          onClick={() => editGeneratedImage(generatedImage.url, generatedImage.alt || t('playground.generated_image'), msg.model, msg.platform)}
                          disabled={isStreaming}
                          title={t('playground.edit_generated_image', { defaultValue: 'Edit this image' })}
                          aria-label={t('playground.edit_generated_image', { defaultValue: 'Edit this image' })}
                        >
                          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                            <path d="M12 20h9" />
                            <path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4 12.5-12.5z" />
                          </svg>
                        </button>
                      )}
                      {generatedImage && (
                        <button
                          type="button"
                          style={{ ...styles.regenerateImageBtn, opacity: isStreaming ? 0.5 : 1 }}
                          onClick={() => regenerateImage(messageIndex)}
                          disabled={isStreaming}
                          title={t('playground.retry_image')}
                          aria-label={t('playground.retry_image')}
                        >
                          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                            <path d="M21 12a9 9 0 0 1-15.6 6" />
                            <path d="M3 12a9 9 0 0 1 15.6-6" />
                            <path d="M19 2v4h-4" />
                            <path d="M5 22v-4h4" />
                          </svg>
                        </button>
                      )}
                      {msg.model && <span style={styles.metaBadge}>{msg.model}</span>}
                    </div>
                  );
                })()}
              </div>
            </div>
            );
          })}

          {isActiveConversationStreaming && streamContent && (
            <div style={{
              ...styles.messageRow,
              ...(isMobile ? styles.messageRowMobile : null),
              ...styles.messageRowAssistant,
            }}>
              <div style={styles.assistantBlock}>
                {streamReasoning && (
                  <details style={styles.reasoningBox} open>
                    <summary style={styles.reasoningSummary}>
                      <span>Thinking</span>
                      {renderCopyButton(streamReasoning, 'Copy thinking', true)}
                    </summary>
                    <div style={styles.reasoningContent}>
                      {renderMessageContent(streamReasoning, interactiveMessageOptions)}
                    </div>
                  </details>
                )}
                {renderCopyableMessageContent(`stream-${streamConversationId || 'active'}`, streamContent)}
                <div style={styles.messageMeta}>
                  <span style={styles.streamingDot} />
                  <span>{t('playground.streaming')}</span>
                </div>
              </div>
            </div>
          )}

          {isActiveConversationStreaming && !streamContent && (
            <div style={{
              ...styles.messageRow,
              ...(isMobile ? styles.messageRowMobile : null),
              ...styles.messageRowAssistant,
            }}>
              <div style={styles.assistantBlock}>
                {streamReasoning ? (
                  <details style={styles.reasoningBox} open>
                    <summary style={styles.reasoningSummary}>
                      <span>Thinking</span>
                      {renderCopyButton(streamReasoning, 'Copy thinking', true)}
                    </summary>
                    <div style={styles.reasoningContent}>
                      {renderMessageContent(streamReasoning, interactiveMessageOptions)}
                    </div>
                  </details>
                ) : (
                  <div style={{ ...styles.messageContent, opacity: 0.5 }}>
                    <span style={styles.thinkingDots}>{t('playground.thinking')}</span>
                  </div>
                )}
              </div>
            </div>
          )}

          {hasRecoverableUserMessage && (
            <div style={{ ...styles.errorBar, ...styles.recoverableBar, ...(isMobile ? styles.errorBarMobile : null) }}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="12" cy="12" r="10" />
                <path d="M12 8v4m0 4h.01" />
              </svg>
              <span style={styles.errorMessage}>{t('playground.response_unfinished', { defaultValue: 'Response was interrupted before the assistant replied.' })}</span>
              <button
                type="button"
                style={styles.recoverableRetryBtn}
                onClick={regenerateUnfinishedResponse}
                title={t('playground.regenerate')}
                aria-label={t('playground.regenerate')}
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M21 12a9 9 0 0 1-15.6 6" />
                  <path d="M3 12a9 9 0 0 1 15.6-6" />
                  <path d="M19 2v4h-4" />
                  <path d="M5 22v-4h4" />
                </svg>
                {t('playground.regenerate')}
              </button>
            </div>
          )}

          {error && (
            <div style={{ ...styles.errorBar, ...(isMobile ? styles.errorBarMobile : null) }}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="12" cy="12" r="10" />
                <path d="M12 8v4m0 4h.01" />
              </svg>
              <span style={styles.errorMessage}>{error}</span>
              {retryRequest && retryRequest.conversationID === activeId && !isStreaming && (
                <button
                  type="button"
                  style={styles.errorRetryBtn}
                  onClick={regenerateLastResponse}
                  title={t('playground.regenerate')}
                  aria-label={t('playground.regenerate')}
                >
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M21 12a9 9 0 0 1-15.6 6" />
                    <path d="M3 12a9 9 0 0 1 15.6-6" />
                    <path d="M19 2v4h-4" />
                    <path d="M5 22v-4h4" />
                  </svg>
                  {t('playground.regenerate')}
                </button>
              )}
            </div>
          )}

          {interactionNotice && (
            <div style={styles.interactionNotice}>{interactionNotice}</div>
          )}

          <div ref={messagesEndRef} />
        </div>

        {/* Input */}
        {activeId && (
          <div style={{ ...styles.inputArea, ...(isMobile ? styles.inputAreaMobile : null) }}>
            <div style={{
              ...styles.inputWrapper,
              ...(isMobile ? styles.inputWrapperMobile : null),
              ...(isActiveConversationStreaming ? styles.inputWrapperStreaming : null),
            }}>
              {pendingImages.length > 0 && (
                <div style={styles.imagePreviewList}>
                  {pendingImages.map(image => (
                    <div key={image.id} style={styles.imagePreviewItem}>
                      <img src={image.url} alt={image.name} style={styles.imagePreview} />
                      <button
                        type="button"
                        style={styles.removeImageBtn}
                        onClick={() => removePendingImage(image.id)}
                        aria-label={`Remove ${image.name}`}
                      >
                        ×
                      </button>
                    </div>
                  ))}
                </div>
              )}
              <textarea
                ref={inputRef}
                style={styles.textarea}
                value={input}
                onChange={e => {
                  setInput(e.target.value);
                  autoResize(e.target);
                }}
                onPaste={handlePaste}
                onKeyDown={handleKeyDown}
                placeholder={t('playground.input_placeholder')}
                rows={1}
                disabled={isActiveConversationStreaming}
              />
              <input
                ref={fileInputRef}
                type="file"
                accept="image/*"
                multiple
                style={styles.fileInput}
                onChange={handleImageChange}
                disabled={isActiveConversationStreaming}
              />
              <input
                ref={editFileInputRef}
                type="file"
                accept="image/*"
                style={styles.fileInput}
                onChange={handleEditImageChange}
                disabled={isActiveConversationStreaming}
              />
              <div style={{ ...styles.inputActions, ...(isMobile ? styles.inputActionsMobile : null) }}>
                <div style={{ ...styles.selectors, ...(isMobile ? styles.selectorsMobile : null) }}>
                  {renderCustomSelect({
                    id: 'model',
                    value: selectedModel,
                    options: modelOptions,
                    onChange: setSelectedModel,
                    ariaLabel: t('playground.model'),
                    variant: 'model',
                  })}

                  {selectedModelIsImage && (
                    <div style={{ ...styles.imageSizeInlineControls, ...(isMobile ? styles.imageSizeInlineControlsMobile : null) }}>
                      {renderCustomSelect({
                        id: 'image-size',
                        value: imageSizeSettings.value,
                        options: IMAGE_SIZE_OPTIONS,
                        onChange: value => updateImageSizeSettings({ value }),
                        ariaLabel: 'Image size',
                      })}
                      {!isMobile && imageSizeSettings.value === IMAGE_SIZE_AUTO && (
                        <span style={styles.imageSizeInlinePreview}>upstream default</span>
                      )}
                    </div>
                  )}

                  {selectedModelSupportsReasoning && renderCustomSelect({
                    id: 'reasoning-effort',
                    value: reasoningEffort,
                    options: [
                      { value: 'minimal', label: 'Minimal' },
                      { value: 'low', label: 'Low' },
                      { value: 'medium', label: 'Medium' },
                      { value: 'high', label: 'High' },
                      { value: 'xhigh', label: 'XHigh' },
                    ],
                    onChange: value => setReasoningEffort(value as ReasoningEffort),
                    ariaLabel: 'Reasoning effort',
                  })}
                </div>
                <div style={{ ...styles.inputButtonGroup, ...(isMobile ? styles.inputButtonGroupMobile : null) }}>
                  {selectedModelIsImage && (
                    <button
                      type="button"
                      style={{
                        ...styles.attachBtn,
                        ...(isEditPanelOpen ? styles.attachBtnActive : null),
                        ...(isMobile ? styles.actionBtnMobile : null),
                      }}
                      onMouseDown={e => e.preventDefault()}
                      onClick={() => {
                        if (editSource) {
                          setIsEditPanelOpen(current => !current);
                          return;
                        }
                        triggerEditImagePicker();
                      }}
                      disabled={isActiveConversationStreaming}
                      title={t('playground.edit_image_region')}
                    >
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <path d="M12 20h9" />
                        <path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4 12.5-12.5z" />
                      </svg>
                      {t('playground.edit')}
                    </button>
                  )}
                  <button
                    type="button"
                    style={{ ...styles.attachBtn, ...(isMobile ? styles.actionBtnMobile : null) }}
                    onMouseDown={e => e.preventDefault()}
                    onClick={triggerImagePicker}
                    disabled={isActiveConversationStreaming || isEditPanelOpen}
                    title={t('playground.attach_images')}
                  >
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <rect x="3" y="3" width="18" height="18" rx="2" />
                      <circle cx="8.5" cy="8.5" r="1.5" />
                      <path d="M21 15l-5-5L5 21" />
                    </svg>
                    {t('playground.image')}
                  </button>
                  {isActiveConversationStreaming ? (
                    <button
                      style={{ ...styles.stopBtn, ...(isMobile ? styles.actionBtnMobile : null) }}
                      onMouseDown={e => e.preventDefault()}
                      onClick={stopStreaming}
                    >
                      <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                        <rect x="2" y="2" width="8" height="8" rx="1" />
                      </svg>
                      {t('playground.stop')}
                    </button>
                  ) : (
                    <button
                      style={{
                        ...styles.sendBtn,
                        ...(isMobile ? styles.actionBtnMobile : null),
                        opacity: canSendMessage ? 1 : 0.4,
                      }}
                      onMouseDown={e => e.preventDefault()}
                      onClick={() => {
                        if (isEditPanelOpen) {
                          void submitImageEdit();
                          return;
                        }
                        sendMessage();
                      }}
                      disabled={!canSendMessage}
                      title={selectedPlatform && selectedModelID ? undefined : t('playground.select_model_first')}
                    >
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <path d="M22 2L11 13" />
                        <path d="M22 2l-7 20-4-9-9-4 20-7z" />
                      </svg>
                      {t('playground.send')}
                    </button>
                  )}
                </div>
              </div>
            </div>
          </div>
        )}
      </div>

      <style>{keyframes}</style>
    </div>
  );
}

const keyframes = `
@keyframes pg-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.3; }
}
@keyframes pg-fadein {
  from { opacity: 0; transform: translateY(6px); }
  to { opacity: 1; transform: translateY(0); }
}

/* ── Quiet Modern aesthetic ──
   单一字体（系统 Chinese ladder + 现代 sans for Latin），多权重撑层级，
   完全跟随 SDK 主题色系。没有任何复古/编辑级装饰。 */
[data-pg-aesthetic] {
  font-feature-settings: 'cv11' on, 'ss01' on;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
  text-rendering: optimizeLegibility;
}

/* 删除按钮：hover 才显出，颜色保持中性，hover 时才染 danger */
.pg-conv-delete {
  opacity: 0;
  color: var(--ag-color-textTertiary, #9ca3af);
  transition: opacity 120ms ease, background 120ms ease, color 120ms ease;
}
.pg-conv-item:hover .pg-conv-delete,
.pg-conv-item:focus-within .pg-conv-delete {
  opacity: 1;
}
.pg-conv-delete:hover {
  background: var(--ag-color-dangerSubtle, rgba(239, 68, 68, 0.12));
  color: var(--ag-color-danger, #ef4444);
}
.pg-conv-delete:focus-visible {
  opacity: 1;
  outline: 2px solid var(--ag-color-borderFocus, #3b82f6);
  outline-offset: 1px;
}

.pg-conv-item {
  position: relative;
}
`;

const styles: Record<string, React.CSSProperties> = {
  layout: {
    display: 'flex',
    height: '100%',
    minHeight: 0,
    minWidth: 0,
    position: 'relative',
    isolation: 'isolate',
    background: cssVar('bgDeep'),
    fontFamily: cssVar('fontSans'),
    color: cssVar('text'),
    overflow: 'hidden',
  },

  // ── Sidebar ──
  sidebar: {
    width: 280,
    minWidth: 280,
    display: 'flex',
    flexDirection: 'column',
    minHeight: 0,
    background: cssVar('bg'),
    borderRight: `1px solid ${cssVar('borderSubtle')}`,
    position: 'relative',
    zIndex: 3,
  },
  sidebarRail: {
    width: 48,
    minWidth: 48,
    display: 'flex',
    justifyContent: 'center',
    alignItems: 'flex-start',
    paddingTop: 12,
    background: cssVar('bg'),
    borderRight: `1px solid ${cssVar('borderSubtle')}`,
    flexShrink: 0,
    zIndex: 3,
  },
  sidebarRailMobile: {
    position: 'absolute',
    top: 0,
    left: 0,
    background: 'transparent',
    borderRight: 'none',
    zIndex: 4,
  },
  sidebarBackdrop: {
    position: 'absolute',
    inset: 0,
    background: 'rgba(6, 10, 18, 0.64)',
    zIndex: 2,
  },
  sidebarMobile: {
    position: 'absolute',
    top: 0,
    bottom: 0,
    left: 0,
    width: 'min(84vw, 320px)',
    minWidth: 'min(84vw, 320px)',
    boxShadow: '0 18px 48px rgba(0, 0, 0, 0.32)',
  },
  sidebarHeader: {
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'center',
    gap: 8,
    padding: '20px 12px 14px 8px',
  },
  sidebarTitleGroup: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    minWidth: 0,
  },
  sidebarTitle: {
    fontSize: 13,
    fontWeight: 500,
    color: cssVar('text'),
    letterSpacing: '-0.005em',
  },
  newBtn: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 28,
    height: 28,
    border: 'none',
    borderRadius: 8,
    background: 'transparent',
    color: cssVar('textSecondary'),
    cursor: 'pointer',
    transition: cssVar('transition'),
    flexShrink: 0,
  },
  convList: {
    flex: 1,
    overflowY: 'auto',
    padding: '2px 8px 8px',
  },
  convItem: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    padding: '8px 10px',
    borderRadius: 8,
    cursor: 'pointer',
    transition: cssVar('transition'),
    marginBottom: 1,
  },
  convTitle: {
    flex: 1,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    fontSize: 13,
    lineHeight: '18px',
    letterSpacing: '-0.003em',
  },
  deleteBtn: {
    width: 22,
    height: 22,
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    background: 'transparent',
    border: 'none',
    borderRadius: 6,
    cursor: 'pointer',
    padding: 0,
    lineHeight: 1,
    flexShrink: 0,
    marginTop: 0,
  },
  emptyConvList: {
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    gap: 8,
    padding: '32px 16px',
    color: cssVar('textTertiary'),
    fontSize: 12,
  },
  modeSwitcher: {
    display: 'flex',
    margin: '0 12px 8px',
    padding: 2,
    border: `1px solid ${cssVar('borderSubtle')}`,
    borderRadius: cssVar('radiusSm'),
    background: cssVar('bgDeep'),
    gap: 2,
  },
  modeSwitcherItem: {
    flex: 1,
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    gap: 5,
    padding: '6px 8px',
    border: 'none',
    borderRadius: 4,
    background: 'transparent',
    color: cssVar('textSecondary'),
    cursor: 'pointer',
    fontSize: 12,
    fontFamily: 'inherit',
    transition: cssVar('transition'),
  },
  modeSwitcherItemActive: {
    background: cssVar('bg'),
    color: cssVar('text'),
    fontWeight: 500,
  },
  balanceBar: {
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'baseline',
    padding: '14px 16px 18px',
  },
  balanceLabel: {
    fontSize: 11,
    fontWeight: 500,
    color: cssVar('textTertiary'),
    letterSpacing: '0.02em',
  },
  balanceValue: {
    fontSize: 13,
    fontWeight: 500,
    color: cssVar('text'),
    fontVariantNumeric: 'tabular-nums',
  },

  // ── Main ──
  main: {
    flex: 1,
    display: 'flex',
    flexDirection: 'column',
    minHeight: 0,
    minWidth: 0,
    overflow: 'hidden',
  },

  toggleBtn: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 32,
    height: 32,
    border: 'none',
    borderRadius: cssVar('radiusSm'),
    background: 'transparent',
    color: cssVar('textSecondary'),
    cursor: 'pointer',
    transition: cssVar('transition'),
    flexShrink: 0,
  },
  // 模型/尺寸/Effort 选择器现在嵌在输入卡片内（左侧），跟附件 / 图片 / 发送按钮
  // 同一行。所以 chip 走透明底，避免在 bgSurface 卡片上再叠一层 surface 制造
  // "卡上有卡"的层级噪声。
  selectors: {
    display: 'flex',
    alignItems: 'center',
    gap: 6,
    flexWrap: 'wrap',
    minWidth: 0,
    flex: 1,
  },
  selectorsMobile: {
    width: '100%',
    gap: 4,
    rowGap: 2,
  },
  selectWrap: {
    position: 'relative',
    minWidth: 0,
  },
  modelSelectWrap: {
    flex: '0 1 280px',
  },
  selectTrigger: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 6,
    width: '100%',
    border: 'none',
    borderRadius: 8,
    background: 'transparent',
    color: cssVar('textSecondary'),
    fontFamily: cssVar('fontSans'),
    fontWeight: 500,
    outline: 'none',
    cursor: 'pointer',
    transition: cssVar('transition'),
  },
  selectTriggerOpen: {
    background: cssVar('bgHover'),
    color: cssVar('text'),
  },
  selectTriggerText: {
    minWidth: 0,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  },
  selectTriggerCaret: {
    flexShrink: 0,
    color: cssVar('textTertiary'),
    fontSize: 10,
    lineHeight: 1,
  },
  modelSelectTrigger: {
    maxWidth: 280,
    padding: '4px 8px',
    color: cssVar('text'),
    fontSize: 13,
    letterSpacing: '-0.003em',
  },
  modelSelectTriggerMobile: {
    maxWidth: '100%',
    padding: '3px 6px',
    fontSize: 12,
  },
  chipSelectTrigger: {
    height: 26,
    padding: '2px 8px',
    fontSize: 12,
  },
  chipSelectTriggerMobile: {
    height: 24,
    padding: '1px 6px',
    fontSize: 11,
  },
  selectPopover: {
    position: 'absolute',
    left: 0,
    bottom: 'calc(100% + 8px)',
    zIndex: 20,
    display: 'flex',
    flexDirection: 'column',
    minWidth: '100%',
    maxHeight: 320,
    padding: 6,
    border: `1px solid ${cssVar('border')}`,
    borderRadius: cssVar('radiusSm'),
    background: cssVar('bg'),
    boxShadow: '0 20px 54px rgba(0, 0, 0, 0.34)',
    overflowY: 'auto',
  },
  selectPopoverModel: {
    width: 390,
    maxWidth: 'calc(100vw - 32px)',
  },
  selectPopoverChip: {
    width: 'max-content',
    minWidth: 116,
  },
  selectOverlay: {
    position: 'fixed',
    inset: 0,
    zIndex: 1000,
    display: 'flex',
    alignItems: 'flex-end',
    padding: 10,
    background: 'rgba(4, 7, 13, 0.62)',
  },
  selectSheet: {
    width: '100%',
    maxHeight: '72vh',
    padding: 8,
    border: `1px solid ${cssVar('border')}`,
    borderRadius: '18px 18px 14px 14px',
    background: cssVar('bg'),
    boxShadow: '0 -18px 60px rgba(0, 0, 0, 0.38)',
    overflow: 'hidden',
  },
  selectSheetHeader: {
    padding: '5px 8px 10px',
    color: cssVar('textTertiary'),
    fontSize: 11,
    fontWeight: 700,
    textTransform: 'uppercase',
    letterSpacing: '0.06em',
  },
  selectPopoverMobile: {
    position: 'relative',
    left: 'auto',
    bottom: 'auto',
    zIndex: 'auto',
    width: '100%',
    minWidth: '100%',
    maxWidth: 'none',
    maxHeight: 'calc(72vh - 48px)',
    padding: 0,
    border: 'none',
    borderRadius: 0,
    background: 'transparent',
    boxShadow: 'none',
  },
  selectOption: {
    display: 'block',
    width: '100%',
    padding: '8px 10px',
    border: 'none',
    borderRadius: 8,
    background: 'transparent',
    color: cssVar('textSecondary'),
    fontFamily: cssVar('fontSans'),
    fontSize: 12,
    fontWeight: 500,
    lineHeight: 1.35,
    textAlign: 'left',
    whiteSpace: 'nowrap',
    cursor: 'pointer',
  },
  selectOptionModel: {
    fontSize: 13,
  },
  selectOptionActive: {
    background: cssVar('primarySubtle'),
    color: cssVar('primary'),
  },
  imageSizeInlineControls: {
    display: 'flex',
    alignItems: 'center',
    gap: 6,
    minWidth: 0,
  },
  imageSizeInlineControlsMobile: {
    flex: '0 1 auto',
    flexWrap: 'wrap',
    gap: 4,
  },
  imageSizeInlinePreview: {
    padding: '3px 9px',
    color: cssVar('textTertiary'),
    fontSize: 12,
    fontFamily: cssVar('fontMono'),
    whiteSpace: 'nowrap',
  },

  // ── Messages ──
  messagesArea: {
    flex: 1,
    minHeight: 0,
    overflowY: 'auto',
    display: 'flex',
    flexDirection: 'column',
    position: 'relative',
  },

  // ── Empty state ──
  // 居中、克制、靠层级与留白说话。一个标题、一个描述、一个 primary CTA。
  emptyState: {
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    justifyContent: 'center',
    flex: 1,
    gap: 14,
    padding: '40px 32px',
    maxWidth: 480,
    margin: '0 auto',
    width: '100%',
    textAlign: 'center',
    animation: 'pg-fadein 0.4s ease-out',
  },
  emptyStateMobile: {
    padding: '32px 24px',
    gap: 12,
  },
  emptyTitle: {
    fontSize: 32,
    fontWeight: 500,
    color: cssVar('text'),
    lineHeight: 1.18,
    letterSpacing: '-0.018em',
    margin: 0,
  },
  emptyDesc: {
    fontSize: 14,
    color: cssVar('textTertiary'),
    lineHeight: 1.55,
    margin: 0,
  },
  emptyBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    padding: '10px 20px',
    border: 'none',
    borderRadius: 999,
    background: cssVar('primary'),
    color: cssVar('textInverse'),
    fontSize: 13,
    fontWeight: 500,
    cursor: 'pointer',
    transition: cssVar('transition'),
    marginTop: 12,
  },

  // ── Message row ──
  // ChatGPT 风格：用户消息右对齐圆角气泡（bgSurface），助手消息左对齐无气泡纯
  // 排版。两侧都不显示 avatar 和"你/助手"role label。借助 padding 拉空气，
  // 取消行间分割线。整列居中、限定 768px 宽，营造窄列阅读体验。
  messageRow: {
    display: 'flex',
    width: '100%',
    maxWidth: 768,
    margin: '0 auto',
    padding: '14px 24px',
    animation: 'pg-fadein 0.25s ease-out',
  },
  messageRowMobile: {
    padding: '10px 14px',
  },
  messageRowUser: {
    justifyContent: 'flex-end',
  },
  messageRowAssistant: {
    justifyContent: 'flex-start',
  },
  userBubble: {
    maxWidth: '78%',
    minWidth: 0,
    padding: '11px 16px',
    borderRadius: 18,
    background: cssVar('bgSurface'),
    border: `1px solid ${cssVar('borderSubtle')}`,
  },
  userBubbleMobile: {
    maxWidth: '82%',
    padding: '10px 13px',
    borderRadius: 16,
  },
  assistantBlock: {
    maxWidth: '100%',
    width: '100%',
    minWidth: 0,
  },
  messageCopyBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 24,
    height: 24,
    border: `1px solid ${cssVar('borderSubtle')}`,
    borderRadius: '999px',
    background: 'transparent',
    color: cssVar('textTertiary'),
    cursor: 'pointer',
    transition: cssVar('transition'),
  },
  messageCopyAfterText: {
    display: 'inline-flex',
    verticalAlign: 'text-bottom',
    marginLeft: 6,
    opacity: 0,
    pointerEvents: 'none',
    transform: 'translateY(1px)',
    transition: cssVar('transition'),
  },
  messageCopyAfterTextVisible: {
    opacity: 1,
    pointerEvents: 'auto',
  },
  messageCopyAfterTextBtn: {
    width: 22,
    height: 22,
  },
  messageContent: {
    fontSize: 14,
    lineHeight: 1.72,
    wordBreak: 'break-word',
    color: cssVar('text'),
  },
  markdownParagraph: {
    margin: '0 0 11px',
  },
  markdownH1: {
    margin: '4px 0 14px',
    fontSize: 22,
    fontWeight: 600,
    lineHeight: 1.25,
    letterSpacing: '-0.015em',
    color: cssVar('text'),
  },
  markdownH2: {
    margin: '18px 0 10px',
    fontSize: 18,
    fontWeight: 600,
    lineHeight: 1.3,
    letterSpacing: '-0.01em',
    color: cssVar('text'),
  },
  markdownH3: {
    margin: '16px 0 8px',
    fontSize: 15,
    fontWeight: 600,
    lineHeight: 1.35,
    color: cssVar('text'),
  },
  markdownH4: {
    margin: '14px 0 8px',
    fontSize: 14,
    fontWeight: 600,
    lineHeight: 1.4,
    color: cssVar('text'),
  },
  markdownList: {
    margin: '0 0 12px',
    paddingLeft: 20,
    color: cssVar('text'),
  },
  markdownListItem: {
    margin: '4px 0',
  },
  markdownBlockquote: {
    margin: '0 0 12px',
    padding: '9px 13px',
    borderLeft: `3px solid ${cssVar('primary')}`,
    borderRadius: '0 10px 10px 0',
    background: cssVar('primarySubtle'),
    color: cssVar('textSecondary'),
  },
  markdownCodeBlock: {
    margin: '4px 0 14px',
    padding: '13px 15px',
    borderRadius: cssVar('radiusSm'),
    background: cssVar('bgDeep'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    color: cssVar('text'),
    fontFamily: cssVar('fontMono'),
    fontSize: 12.5,
    lineHeight: 1.72,
    overflowX: 'auto',
    whiteSpace: 'pre',
  },
  markdownInlineCode: {
    padding: '1px 5px 2px',
    borderRadius: 6,
    background: cssVar('bgSurface'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    color: cssVar('primary'),
    fontFamily: cssVar('fontMono'),
    fontSize: '0.9em',
  },
  markdownInlineMath: {
    display: 'inline-block',
    maxWidth: '100%',
    overflowX: 'auto',
    overflowY: 'hidden',
    verticalAlign: '-0.18em',
  },
  markdownBlockMath: {
    margin: '4px 0 14px',
    padding: '12px 14px',
    borderRadius: cssVar('radiusSm'),
    background: cssVar('bgSurface'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    color: cssVar('text'),
    overflowX: 'auto',
    overflowY: 'hidden',
  },
  markdownLink: {
    color: cssVar('primary'),
    textDecoration: 'underline',
    textDecorationColor: cssVar('primary'),
    textUnderlineOffset: 3,
  },
  markdownDivider: {
    height: 1,
    border: 0,
    background: cssVar('border'),
    margin: '16px 0',
  },
  reasoningBox: {
    marginBottom: 10,
    padding: '10px 12px',
    borderRadius: cssVar('radiusSm'),
    background: cssVar('bgSurface'),
    border: `1px solid ${cssVar('borderSubtle')}`,
  },
  reasoningSummary: {
    cursor: 'pointer',
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    fontSize: 12,
    fontWeight: 600,
    color: cssVar('textSecondary'),
    userSelect: 'none',
  },
  reasoningContent: {
    marginTop: 8,
    fontSize: 13,
    lineHeight: 1.6,
    wordBreak: 'break-word',
    color: cssVar('textSecondary'),
  },
  imageGroup: {
    display: 'flex',
    flexWrap: 'wrap',
    alignItems: 'flex-start',
    gap: 12,
    maxWidth: '100%',
    margin: '10px 0 6px',
  },
  imageGroupMobile: {
    gap: 8,
    marginTop: 8,
  },
  generatedImageFrame: {
    display: 'inline-flex',
    flexDirection: 'column',
    alignItems: 'flex-start',
    gap: 8,
    flex: '1 1 180px',
    maxWidth: 'min(100%, 320px)',
    minWidth: 0,
  },
  generatedImageFrameMobile: {
    flex: '1 1 140px',
    maxWidth: 'min(100%, 240px)',
  },
  generatedImagePreviewBtn: {
    display: 'block',
    width: '100%',
    padding: 0,
    border: 'none',
    background: 'transparent',
    cursor: 'zoom-in',
    textAlign: 'left',
    font: 'inherit',
  },
  generatedImageOverlayWrap: {
    position: 'relative',
    display: 'block',
    width: '100%',
    borderRadius: cssVar('radiusMd'),
    overflow: 'hidden',
  },
  generatedImage: {
    display: 'block',
    maxHeight: 420,
    width: '100%',
    height: 'auto',
    borderRadius: cssVar('radiusMd'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    objectFit: 'contain',
  },
  generatedImageDimOverlay: {
    position: 'absolute',
    inset: 0,
    borderRadius: cssVar('radiusMd'),
    background: 'rgba(15, 23, 42, 0.34)',
    pointerEvents: 'none',
  },
  generatedImageSelection: {
    position: 'absolute',
    border: `2px solid ${cssVar('primary')}`,
    background: 'rgba(45, 212, 191, 0.2)',
    boxShadow: '0 0 0 9999px rgba(3, 7, 18, 0.18), 0 0 18px rgba(45, 212, 191, 0.35)',
    pointerEvents: 'none',
  },
  imageDownloadBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 32,
    height: 32,
    padding: 0,
    borderRadius: '999px',
    border: `1px solid ${cssVar('borderSubtle')}`,
    background: cssVar('bgSurface'),
    color: cssVar('textSecondary'),
    cursor: 'pointer',
    transition: cssVar('transition'),
  },
  imageMessageActions: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    marginTop: 0,
  },
  regenerateImageBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 32,
    height: 32,
    padding: 0,
    borderRadius: '999px',
    border: `1px solid ${cssVar('borderSubtle')}`,
    background: cssVar('bgSurface'),
    color: cssVar('textSecondary'),
    cursor: 'pointer',
    transition: cssVar('transition'),
  },
  imagePreviewOverlay: {
    position: 'absolute',
    inset: 0,
    zIndex: 10,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    padding: 24,
    background: 'rgba(4, 7, 13, 0.78)',
    backdropFilter: 'blur(10px)',
  },
  imagePreviewModal: {
    position: 'relative',
    display: 'flex',
    maxWidth: 'min(94vw, 1120px)',
    maxHeight: '90vh',
    width: 'fit-content',
    borderRadius: cssVar('radiusLg'),
    border: `1px solid ${cssVar('border')}`,
    background: cssVar('bgDeep'),
    boxShadow: '0 28px 90px rgba(0, 0, 0, 0.45)',
    overflow: 'hidden',
  },
  imagePreviewCloseBtn: {
    position: 'absolute',
    top: 10,
    right: 10,
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 32,
    height: 32,
    border: `1px solid rgba(255, 255, 255, 0.16)`,
    borderRadius: '999px',
    background: 'rgba(8, 12, 20, 0.72)',
    color: '#edf4ff',
    fontSize: 22,
    lineHeight: 1,
    cursor: 'pointer',
    backdropFilter: 'blur(10px)',
    boxShadow: '0 8px 24px rgba(0, 0, 0, 0.24)',
  },
  imagePreviewLarge: {
    display: 'block',
    maxWidth: 'min(94vw, 1120px)',
    maxHeight: '90vh',
    width: 'auto',
    height: 'auto',
    objectFit: 'contain',
    background: cssVar('bgDeep'),
  },
  interactionNotice: {
    position: 'sticky',
    bottom: 12,
    alignSelf: 'center',
    zIndex: 4,
    padding: '7px 12px',
    borderRadius: '999px',
    background: cssVar('bgElevated'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    color: cssVar('textSecondary'),
    fontSize: 12,
    boxShadow: cssVar('shadowMd'),
  },
  messageMeta: {
    display: 'flex',
    alignItems: 'center',
    gap: 6,
    marginTop: 8,
    fontSize: 11,
    color: cssVar('textTertiary'),
  },
  metaBadge: {
    display: 'inline-flex',
    padding: '2px 8px',
    borderRadius: '999px',
    background: cssVar('bgSurface'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    fontSize: 11,
    fontFamily: cssVar('fontMono'),
    color: cssVar('textSecondary'),
  },
  streamingDot: {
    width: 6,
    height: 6,
    borderRadius: '50%',
    background: cssVar('primary'),
    animation: 'pg-pulse 1.2s ease-in-out infinite',
  },
  thinkingDots: {
    animation: 'pg-pulse 1.5s ease-in-out infinite',
  },

  // ── Error ──
  errorBar: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    margin: '8px 28px',
    padding: '10px 14px',
    borderRadius: cssVar('radiusSm'),
    background: cssVar('dangerSubtle'),
    color: cssVar('danger'),
    fontSize: 13,
    border: `1px solid ${cssVar('danger')}`,
    borderColor: 'rgba(251, 113, 133, 0.2)',
  },
  errorBarMobile: {
    margin: '8px 14px',
  },
  errorMessage: {
    flex: 1,
    minWidth: 0,
  },
  recoverableBar: {
    background: cssVar('primarySubtle'),
    color: cssVar('primary'),
    borderColor: 'rgba(45, 212, 191, 0.22)',
  },
  errorRetryBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    padding: '5px 10px',
    borderRadius: '999px',
    border: '1px solid rgba(251, 113, 133, 0.28)',
    background: 'rgba(251, 113, 133, 0.1)',
    color: cssVar('danger'),
    fontSize: 12,
    fontWeight: 700,
    cursor: 'pointer',
    fontFamily: cssVar('fontSans'),
  },
  recoverableRetryBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    padding: '5px 10px',
    borderRadius: '999px',
    border: '1px solid rgba(45, 212, 191, 0.3)',
    background: 'rgba(45, 212, 191, 0.12)',
    color: cssVar('primary'),
    fontSize: 12,
    fontWeight: 700,
    cursor: 'pointer',
    fontFamily: cssVar('fontSans'),
  },

  // ── Input ──
  inputArea: {
    padding: '12px 28px 20px',
    background: 'transparent',
    flexShrink: 0,
  },
  inputAreaMobile: {
    padding: '8px 10px 10px',
  },
  inputWrapper: {
    display: 'flex',
    flexDirection: 'column',
    gap: 8,
    border: `1px solid ${cssVar('border')}`,
    borderRadius: 22,
    background: cssVar('bgSurface'),
    paddingTop: 10,
    paddingRight: 12,
    paddingBottom: 8,
    paddingLeft: 12,
    transition: cssVar('transition'),
    width: '100%',
    maxWidth: 768,
    margin: '0 auto',
  },
  inputWrapperMobile: {
    gap: 7,
    borderRadius: 20,
    paddingTop: 9,
    paddingRight: 10,
    paddingBottom: 8,
    paddingLeft: 10,
  },
  inputWrapperStreaming: {
    paddingTop: 8,
    paddingBottom: 8,
  },
  imageEditPanel: {
    display: 'flex',
    flexDirection: 'column',
    gap: 12,
    padding: 12,
    borderRadius: cssVar('radiusMd'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    background: cssVar('bgSurface'),
  },
  imageEditPanelMobile: {
    padding: 10,
  },
  imageEditHeader: {
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'flex-start',
    gap: 12,
  },
  imageEditHeaderMobile: {
    flexDirection: 'column',
  },
  imageEditTitleWrap: {
    display: 'flex',
    flexDirection: 'column',
    gap: 3,
    minWidth: 0,
  },
  imageEditTitle: {
    fontSize: 13,
    fontWeight: 700,
    color: cssVar('text'),
  },
  imageEditSubtitle: {
    fontSize: 12,
    color: cssVar('textTertiary'),
  },
  imageEditHeaderActions: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    flexShrink: 0,
  },
  imageEditGhostBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    minHeight: 30,
    padding: '6px 10px',
    borderRadius: cssVar('radiusSm'),
    border: `1px solid ${cssVar('border')}`,
    background: 'rgba(9, 14, 24, 0.5)',
    color: cssVar('textSecondary'),
    fontSize: 12,
    fontWeight: 700,
    cursor: 'pointer',
    transition: cssVar('transition'),
    fontFamily: cssVar('fontSans'),
  },
  imageEditIconBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 30,
    height: 30,
    borderRadius: '999px',
    border: `1px solid ${cssVar('borderSubtle')}`,
    background: 'rgba(9, 14, 24, 0.52)',
    color: cssVar('textSecondary'),
    fontSize: 18,
    lineHeight: 1,
    cursor: 'pointer',
  },
  imageEditBody: {
    display: 'grid',
    gridTemplateColumns: 'minmax(0, 1fr) 150px',
    gap: 12,
    alignItems: 'stretch',
  },
  imageEditBodyMobile: {
    gridTemplateColumns: '1fr',
  },
  imageEditStageWrap: {
    minWidth: 0,
    overflow: 'auto',
    borderRadius: cssVar('radiusMd'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    background: 'rgba(2, 6, 14, 0.44)',
    padding: 8,
  },
  imageEditStage: {
    position: 'relative',
    display: 'inline-flex',
    maxWidth: '100%',
    borderRadius: cssVar('radiusSm'),
    overflow: 'hidden',
    verticalAlign: 'top',
  },
  imageEditSource: {
    display: 'block',
    width: '100%',
    height: '100%',
    objectFit: 'contain',
    userSelect: 'none',
  },
  imageEditCanvas: {
    position: 'absolute',
    inset: 0,
    width: '100%',
    height: '100%',
    touchAction: 'none',
    cursor: 'crosshair',
  },
  imageEditSelection: {
    position: 'absolute',
    zIndex: 1,
    border: '2px solid rgba(45, 212, 191, 0.95)',
    background: 'rgba(45, 212, 191, 0.2)',
    boxShadow: '0 0 0 9999px rgba(2, 6, 14, 0.28), 0 0 18px rgba(45, 212, 191, 0.35)',
    pointerEvents: 'none',
  },
  imageEditSidePanel: {
    display: 'flex',
    flexDirection: 'column',
    gap: 8,
    justifyContent: 'space-between',
    minWidth: 0,
    padding: 10,
    borderRadius: cssVar('radiusSm'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    background: 'rgba(5, 10, 18, 0.38)',
  },
  imageEditBadge: {
    display: 'inline-flex',
    width: 'fit-content',
    padding: '4px 8px',
    borderRadius: '999px',
    background: cssVar('primarySubtle'),
    color: cssVar('primary'),
    fontSize: 11,
    fontWeight: 800,
    fontFamily: cssVar('fontMono'),
  },
  imageEditFilename: {
    color: cssVar('textTertiary'),
    fontSize: 12,
    lineHeight: 1.4,
    wordBreak: 'break-word',
  },
  imageEditEmptyBtn: {
    minHeight: 96,
    borderRadius: cssVar('radiusMd'),
    border: `1px dashed ${cssVar('border')}`,
    background: 'rgba(45, 212, 191, 0.05)',
    color: cssVar('primary'),
    fontSize: 13,
    fontWeight: 700,
    cursor: 'pointer',
    fontFamily: cssVar('fontSans'),
  },
  editModalOverlay: {
    position: 'fixed',
    inset: 0,
    zIndex: 50,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    padding: 24,
    background: 'rgba(4, 7, 13, 0.78)',
    backdropFilter: 'blur(10px)',
  },
  editModalCard: {
    position: 'relative',
    display: 'flex',
    flexDirection: 'column',
    gap: 14,
    width: 'min(96vw, 1080px)',
    maxHeight: '92vh',
    padding: 18,
    borderRadius: cssVar('radiusLg'),
    border: `1px solid ${cssVar('border')}`,
    background: cssVar('bgDeep'),
    boxShadow: '0 28px 90px rgba(0, 0, 0, 0.45)',
    overflow: 'hidden',
  },
  editModalCardMobile: {
    width: '100%',
    maxHeight: '94vh',
    padding: 12,
    gap: 10,
  },
  editModalHeader: {
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'flex-start',
    gap: 12,
  },
  editModalBody: {
    display: 'grid',
    gridTemplateColumns: 'minmax(0, 1fr) 200px',
    gap: 14,
    minHeight: 0,
    flex: 1,
    alignItems: 'stretch',
  },
  editModalBodyMobile: {
    gridTemplateColumns: '1fr',
  },
  editModalStageWrap: {
    minWidth: 0,
    overflow: 'auto',
    borderRadius: cssVar('radiusMd'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    background: 'rgba(2, 6, 14, 0.44)',
    padding: 8,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
  },
  editModalSide: {
    display: 'flex',
    flexDirection: 'column',
    gap: 10,
    padding: 12,
    borderRadius: cssVar('radiusSm'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    background: 'rgba(5, 10, 18, 0.38)',
  },
  editModalFooter: {
    display: 'flex',
    flexDirection: 'column',
    gap: 10,
  },
  editModalPrompt: {
    width: '100%',
    boxSizing: 'border-box',
    padding: '10px 12px',
    borderRadius: cssVar('radiusSm'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    background: cssVar('bgSurface'),
    color: cssVar('text'),
    fontFamily: cssVar('fontSans'),
    fontSize: 13,
    lineHeight: 1.5,
    resize: 'vertical',
    minHeight: 60,
    outline: 'none',
  },
  editModalActions: {
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'center',
    gap: 12,
    flexWrap: 'wrap',
  },
  editModalHint: {
    fontSize: 11,
    color: cssVar('textTertiary'),
    flex: 1,
    minWidth: 0,
  },
  editModalBtnGroup: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    flexShrink: 0,
  },
  editModalSubmitBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    padding: '9px 16px',
    border: 'none',
    borderRadius: cssVar('radiusSm'),
    background: cssVar('primary'),
    color: cssVar('textInverse'),
    fontSize: 13,
    fontWeight: 600,
    fontFamily: cssVar('fontSans'),
    cursor: 'pointer',
    transition: cssVar('transition'),
  },
  editModalStatus: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    padding: '8px 12px',
    borderRadius: cssVar('radiusSm'),
    background: cssVar('primarySubtle'),
    color: cssVar('primary'),
    fontSize: 12,
    fontWeight: 500,
  },
  editModalError: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    padding: '8px 12px',
    borderRadius: cssVar('radiusSm'),
    background: cssVar('dangerSubtle'),
    color: cssVar('danger'),
    fontSize: 12,
    fontWeight: 500,
  },
  imagePreviewList: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: 8,
  },
  imagePreviewItem: {
    position: 'relative',
    width: 76,
    height: 76,
    borderRadius: cssVar('radiusSm'),
    overflow: 'hidden',
    border: `1px solid ${cssVar('borderSubtle')}`,
    background: cssVar('bgHover'),
  },
  imagePreview: {
    width: '100%',
    height: '100%',
    objectFit: 'cover',
    display: 'block',
  },
  removeImageBtn: {
    position: 'absolute',
    top: 4,
    right: 4,
    width: 20,
    height: 20,
    border: 'none',
    borderRadius: 999,
    background: 'rgba(0, 0, 0, 0.62)',
    color: '#fff',
    cursor: 'pointer',
    lineHeight: '20px',
    padding: 0,
    fontSize: 16,
  },
  textarea: {
    width: '100%',
    border: 'none',
    background: 'transparent',
    color: cssVar('text'),
    fontSize: 14,
    fontFamily: cssVar('fontSans'),
    resize: 'none',
    outline: 'none',
    lineHeight: 1.55,
    height: 24,
    minHeight: 24,
    maxHeight: 128,
  },
  inputActions: {
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'center',
    gap: 12,
  },
  inputActionsMobile: {
    alignItems: 'stretch',
    flexDirection: 'column',
    gap: 7,
  },
  inputButtonGroup: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
  },
  inputButtonGroupMobile: {
    width: '100%',
    minWidth: 0,
    justifyContent: 'space-between',
    gap: 6,
  },
  fileInput: {
    display: 'none',
  },
  inputHint: {
    fontSize: 11,
    color: cssVar('textTertiary'),
  },
  inputHintMobile: {
    display: 'none',
  },
  attachBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    padding: '7px 12px',
    border: 'none',
    borderRadius: 999,
    background: 'transparent',
    color: cssVar('textSecondary'),
    fontSize: 12,
    fontWeight: 500,
    cursor: 'pointer',
    transition: cssVar('transition'),
  },
  attachBtnActive: {
    background: cssVar('primarySubtle'),
    color: cssVar('primary'),
  },
  sendBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    padding: '7px 14px',
    border: 'none',
    borderRadius: 999,
    background: cssVar('primary'),
    color: cssVar('textInverse'),
    fontSize: 13,
    fontWeight: 500,
    cursor: 'pointer',
    transition: cssVar('transition'),
  },
  stopBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    padding: '7px 14px',
    border: 'none',
    borderRadius: 999,
    background: cssVar('dangerSubtle'),
    color: cssVar('danger'),
    fontSize: 13,
    fontWeight: 500,
    cursor: 'pointer',
  },
  actionBtnMobile: {
    flex: '0 1 auto',
    minWidth: 44,
    minHeight: 36,
    justifyContent: 'center',
    padding: '8px 11px',
  },
};
