import { Children, cloneElement, isValidElement, useState, useEffect, useRef, useCallback, type CSSProperties, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { cssVar } from '@airgate/theme';
import { api, chatCompletion, chatCompletionsStream, editImage as requestImageEdit } from './api';
import type { ChatMessageContent, Conversation, ImageEditResponse, Message, ModelInfo, PlatformInfo, ReasoningEffort, UserInfo } from './api';

declare global {
  interface Window {
    airgate?: {
      confirm?: (message: string, options?: { title?: string; danger?: boolean }) => Promise<boolean>;
    };
  }
}

const MOBILE_BREAKPOINT = 960;
const DRAFT_CONVERSATION_ID = -1;
const IMAGE_MARKDOWN_RE = /!\[[^\]]*\]\((data:image\/(?:png|jpeg|jpg|webp|gif);base64,[^)]+|https?:\/\/[^\s)]+)\)/g;
const IMAGE_MARKDOWN_ITEM_RE = /!\[([^\]]*)\]\((data:image\/(?:png|jpeg|jpg|webp|gif);base64,[^)]+|https?:\/\/[^\s)]+)\)/g;
const IMAGE_MARKDOWN_TEST_RE = /!\[[^\]]*\]\((data:image\/(?:png|jpeg|jpg|webp|gif);base64,[^)]+|https?:\/\/[^\s)]+)\)/;
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
const IMAGE_SIZE_ALIGNMENT = 16;
const MAX_IMAGE_EDGE = 3840;
const MAX_IMAGE_SHOTS = 4;
const BASE_RESOLUTION_OPTIONS: Array<{ value: BaseResolution; label: string }> = [
  { value: 1024, label: '1K' },
  { value: 2048, label: '2K' },
  { value: 3840, label: '4K' },
];
const IMAGE_RATIO_OPTIONS: Array<{ value: ImageRatio; label: string }> = [
  { value: '1:1', label: '1:1' },
  { value: '3:2', label: '3:2' },
  { value: '2:3', label: '2:3' },
  { value: '16:9', label: '16:9' },
  { value: '9:16', label: '9:16' },
  { value: '4:3', label: '4:3' },
  { value: '3:4', label: '3:4' },
  { value: '21:9', label: '21:9' },
];
const DEFAULT_IMAGE_SIZE_SETTINGS: ImageSizeSettings = {
  mode: 'auto',
  baseResolution: 1024,
  ratio: '1:1',
};
type ImageSizeMode = 'auto' | 'ratio';
type BaseResolution = 1024 | 2048 | 3840;
type ImageRatio = '1:1' | '3:2' | '2:3' | '16:9' | '9:16' | '4:3' | '3:4' | '21:9';
type ImageSizeSettings = {
  mode: ImageSizeMode;
  baseResolution: BaseResolution;
  ratio: ImageRatio;
};
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
};

function clampNumber(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value));
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

function cloneImageSizeSettings(settings: ImageSizeSettings): ImageSizeSettings {
  return { ...settings };
}

function parseRatioValue(value: string) {
  const [w, h] = value.split(':').map(part => Number.parseInt(part, 10));
  return w > 0 && h > 0 ? { width: w, height: h } : null;
}

function alignImageDimension(value: number) {
  return Math.max(IMAGE_SIZE_ALIGNMENT, Math.floor(value / IMAGE_SIZE_ALIGNMENT) * IMAGE_SIZE_ALIGNMENT);
}

function clampImageSize(width: number, height: number) {
  if (width <= MAX_IMAGE_EDGE && height <= MAX_IMAGE_EDGE) return { width, height };
  if (width >= height) {
    return { width: MAX_IMAGE_EDGE, height: alignImageDimension(height * MAX_IMAGE_EDGE / width) };
  }
  return { width: alignImageDimension(width * MAX_IMAGE_EDGE / height), height: MAX_IMAGE_EDGE };
}

function formatImageSize(width: number, height: number) {
  const clamped = clampImageSize(alignImageDimension(width), alignImageDimension(height));
  return `${clamped.width}x${clamped.height}`;
}

function resolveImageRatio(settings: ImageSizeSettings) {
  return parseRatioValue(settings.ratio);
}

function resolveImageSize(settings: ImageSizeSettings) {
  if (settings.mode === 'auto') return undefined;

  const ratio = resolveImageRatio(settings);
  if (!ratio) return undefined;
  const base = settings.baseResolution;
  if (ratio.width === ratio.height) return formatImageSize(base, base);
  if (ratio.width > ratio.height) {
    return formatImageSize(base, base * ratio.height / ratio.width);
  }
  return formatImageSize(base * ratio.width / ratio.height, base);
}

function imageSizeSummary(settings: ImageSizeSettings) {
  if (settings.mode === 'auto') return 'Auto';
  return resolveImageSize(settings) || 'Invalid size';
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
  return /^(data:image\/(?:png|jpeg|jpg|webp|gif);base64,|https?:)/i.test(url);
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
    <span key={key} style={styles.generatedImageFrame}>
      {previewableImage}
    </span>
  );
}

function renderInlineMarkdown(text: string, keyPrefix: string, options: MessageContentOptions = {}) {
  const nodes: ReactNode[] = [];
  const inlineRe = /(!\[([^\]]*)\]\(([^)\s]+)\)|\[([^\]]+)\]\(([^)\s]+)\)|`([^`]+)`|\*\*([^*]+)\*\*|__([^_]+)__|\*([^*]+)\*|_([^_]+)_)/g;
  let lastIndex = 0;
  let match: RegExpExecArray | null;

  while ((match = inlineRe.exec(text)) !== null) {
    if (match.index > lastIndex) {
      pushTextWithBreaks(nodes, text.slice(lastIndex, match.index), `${keyPrefix}-text-${lastIndex}`);
    }

    const key = `${keyPrefix}-${match.index}`;
    const imageAlt = match[2];
    const imageUrl = match[3];
    const linkText = match[4];
    const linkUrl = match[5];
    const inlineCode = match[6];
    const boldText = match[7] || match[8];
    const italicText = match[9] || match[10];

    if (imageUrl && isSafeImageUrl(imageUrl)) {
      nodes.push(renderGeneratedImage(key, imageUrl, imageAlt || options.generatedImageAlt || 'Generated image', options));
    } else if (linkUrl && isSafeLinkUrl(linkUrl)) {
      nodes.push(
        <a key={key} href={linkUrl} style={styles.markdownLink} target="_blank" rel="noreferrer">
          {renderInlineMarkdown(linkText, `${key}-link`, options)}
        </a>,
      );
    } else if (inlineCode) {
      nodes.push(<code key={key} style={styles.markdownInlineCode}>{inlineCode}</code>);
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
    <div key={key} style={styles.imageGroup}>
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
  let pendingImageGroup: Array<{ alt: string; url: string }> = [];
  let inCodeBlock = false;
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

  for (const line of lines) {
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
  const [messages, setMessages] = useState<Message[]>([]);
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
  const [reasoningEffort, setReasoningEffort] = useState<ReasoningEffort>('medium');
  const [imageSizeSettings, setImageSizeSettings] = useState<ImageSizeSettings>(() => cloneImageSizeSettings(DEFAULT_IMAGE_SIZE_SETTINGS));
  const [userInfo, setUserInfo] = useState<UserInfo | null>(null);
  const [error, setError] = useState('');
  const [retryRequest, setRetryRequest] = useState<RetryRequest | null>(null);
  const [interactionNotice, setInteractionNotice] = useState('');
  const [hoveredCopyTarget, setHoveredCopyTarget] = useState<string | null>(null);
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [isMobile, setIsMobile] = useState(() => (
    typeof window !== 'undefined' ? window.innerWidth <= MOBILE_BREAKPOINT : false
  ));

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
  }, [activeId]);

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
      const maxHeight = isMobile ? 220 : 260;
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
  const displayedImageSize = imageSizeSummary(imageSizeSettings);

  const selectedPlatform = selectedModelPlatform;

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
        messages: requestMessages.map(msg => ({ role: msg.role, content: toChatMessageContent(msg.role, msg.content) })),
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
        const persisted = await api.persistMessage({
          conversation_id: conversationID,
          role: 'assistant',
          content: accumulated,
          reasoning: accumulatedReasoning,
          platform,
          model: usage.model || model,
          group_id: groupID,
          input_tokens: usage.input_tokens,
          output_tokens: usage.output_tokens,
          cost: usage.cost,
        });
        if (activeIdRef.current === conversationID) {
          setMessages(prev => [...prev, persisted]);
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
                content: toChatMessageContent(msg.role, msg.content),
              })),
              n: 1,
            },
            {
              onData: (text) => {
                localContent += text;
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
            accumulated += text;
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

  const createEditMaskBlob = useCallback(async () => {
    const canvas = editCanvasRef.current;
    if (!canvas || !editSource || !editSelection) throw new Error('Selection required');

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
      Math.floor(editSelection.x * scaleX),
      Math.floor(editSelection.y * scaleY),
      Math.ceil(editSelection.width * scaleX),
      Math.ceil(editSelection.height * scaleY),
    );
    return canvasToBlob(maskCanvas);
  }, [editSelection, editSource]);

  const submitImageEdit = useCallback(async () => {
    if (!activeId || isStreaming || isEditingImage) return;
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
      setError(t('playground.select_edit_area_first'));
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
    const editAnnotation = canvas ? {
      imageIndex: 0,
      rect: {
        x: editSelection.x / canvas.width,
        y: editSelection.y / canvas.height,
        width: editSelection.width / canvas.width,
        height: editSelection.height / canvas.height,
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
      const maskBlob = await createEditMaskBlob();
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

  return (
    <div data-full-bleed style={styles.layout}>
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

      {/* ── Sidebar ── */}
      {sidebarOpen && (
        <div style={{ ...styles.sidebar, ...(isMobile ? styles.sidebarMobile : null) }}>
          <div style={styles.sidebarHeader}>
            <span style={styles.sidebarTitle}>{t('playground.conversations')}</span>
            <button
              style={styles.newBtn}
              onClick={createConversation}
              title={t('playground.new_conversation')}
            >
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round">
                <path d="M7 1v12M1 7h12" />
              </svg>
            </button>
          </div>

          <div style={styles.convList}>
            {conversations.map(c => {
              const isActive = c.id === activeId;
              return (
                <div
                  key={c.id}
                  style={{
                    ...styles.convItem,
                    background: isActive ? cssVar('primarySubtle') : 'transparent',
                    borderColor: isActive ? cssVar('borderFocus') : 'transparent',
                  }}
                  onClick={() => openConversation(c.id)}
                >
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke={isActive ? cssVar('primary') : cssVar('textTertiary')} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, marginTop: 2 }}>
                    <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
                  </svg>
                  <span style={{
                    ...styles.convTitle,
                    color: isActive ? cssVar('text') : cssVar('textSecondary'),
                  }}>
                    {c.title || t('playground.new_conversation')}
                  </span>
                  <button
                    style={styles.deleteBtn}
                    onClick={(e) => { e.stopPropagation(); deleteConversation(c.id); }}
                    title={t('playground.delete_conversation')}
                  >
                    <svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
                      <path d="M2 2l8 8M10 2l-8 8" />
                    </svg>
                  </button>
                </div>
              );
            })}
            {conversations.length === 0 && (
              <div style={styles.emptyConvList}>
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke={cssVar('textTertiary')} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" style={{ opacity: 0.5 }}>
                  <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
                </svg>
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
      )}

      {/* ── Main ── */}
      <div style={styles.main}>
        {/* Top bar */}
        <div style={{ ...styles.topBar, ...(isMobile ? styles.topBarMobile : null) }}>
          <div style={{ ...styles.topBarLeft, ...(isMobile ? styles.topBarLeftMobile : null) }}>
            <button style={styles.toggleBtn} onClick={() => setSidebarOpen(!sidebarOpen)}>
              <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
                {sidebarOpen
                  ? <><path d="M6 2v12" /><path d="M2 2h12v12H2z" /><path d="M10 6l-2 2 2 2" /></>
                  : <><path d="M6 2v12" /><path d="M2 2h12v12H2z" /><path d="M8 6l2 2-2 2" /></>
                }
              </svg>
            </button>

            <div style={{ ...styles.selectors, ...(isMobile ? styles.selectorsMobile : null) }}>
              <div style={{ ...styles.selectorGroup, ...(isMobile ? styles.selectorGroupMobile : null) }}>
                <label style={styles.selectorLabel}>{t('playground.model')}</label>
                <select
                  style={{ ...styles.select, ...(isMobile ? styles.selectMobile : null) }}
                  value={selectedModel}
                  onChange={e => setSelectedModel(e.target.value)}
                >
                  {models.map(m => (
                    <option key={modelOptionValue(m)} value={modelOptionValue(m)}>
                      {m.name || m.id} · {platformDisplayName(platforms, m.platform)}{isImageModel(m) ? ' · image' : supportsReasoning(m) ? ' · reasoning' : ''}
                    </option>
                  ))}
                </select>
              </div>

              {selectedModelIsImage && (
                <>
                  {!isMobile && <div style={styles.selectorDivider} />}
                  <div style={{ ...styles.selectorGroup, ...(isMobile ? styles.selectorGroupMobile : null) }}>
                    <label style={styles.selectorLabel}>Size</label>
                    <div style={{ ...styles.imageSizeInlineControls, ...(isMobile ? styles.imageSizeInlineControlsMobile : null) }}>
                      <select
                        style={{ ...styles.imageSizeMiniSelect, ...(isMobile ? styles.imageSizeMiniSelectMobile : null) }}
                        value={imageSizeSettings.mode}
                        onChange={e => updateImageSizeSettings({ mode: e.target.value as ImageSizeMode })}
                        aria-label="Image size mode"
                      >
                        <option value="auto">Auto</option>
                        <option value="ratio">Ratio</option>
                      </select>

                      {imageSizeSettings.mode === 'ratio' && (
                        <>
                          <select
                            style={{ ...styles.imageSizeMiniSelect, ...(isMobile ? styles.imageSizeMiniSelectMobile : null) }}
                            value={imageSizeSettings.baseResolution}
                            onChange={e => updateImageSizeSettings({ baseResolution: Number(e.target.value) as BaseResolution })}
                            aria-label="Base resolution"
                          >
                            {BASE_RESOLUTION_OPTIONS.map(option => (
                              <option key={option.value} value={option.value}>{option.label}</option>
                            ))}
                          </select>
                          <select
                            style={{ ...styles.imageSizeMiniSelect, ...(isMobile ? styles.imageSizeMiniSelectMobile : null) }}
                            value={imageSizeSettings.ratio}
                            onChange={e => updateImageSizeSettings({ ratio: e.target.value as ImageRatio })}
                            aria-label="Image ratio"
                          >
                            {IMAGE_RATIO_OPTIONS.map(option => (
                              <option key={option.value} value={option.value}>{option.label}</option>
                            ))}
                          </select>
                        </>
                      )}

                      <span style={styles.imageSizeInlinePreview}>{displayedImageSize}</span>
                    </div>
                  </div>
                </>
              )}

              {selectedModelSupportsReasoning && (
                <>
                  {!isMobile && <div style={styles.selectorDivider} />}
                  <div style={{ ...styles.selectorGroup, ...(isMobile ? styles.selectorGroupMobile : null) }}>
                    <label style={styles.selectorLabel}>Effort</label>
                    <select
                      style={{ ...styles.select, ...(isMobile ? styles.selectMobile : null) }}
                      value={reasoningEffort}
                      onChange={e => setReasoningEffort(e.target.value as ReasoningEffort)}
                    >
                      <option value="minimal">Minimal</option>
                      <option value="low">Low</option>
                      <option value="medium">Medium</option>
                      <option value="high">High</option>
                      <option value="xhigh">XHigh</option>
                    </select>
                  </div>
                </>
              )}
            </div>
          </div>

          {activeConv && (
            <span style={{ ...styles.topBarTitle, ...(isMobile ? styles.topBarTitleMobile : null) }}>
              {activeConv.title || t('playground.new_conversation')}
            </span>
          )}
        </div>

        {/* Messages */}
        <div style={styles.messagesArea}>
          {!activeId && (
            <div style={{ ...styles.emptyState, ...(isMobile ? styles.emptyStateMobile : null) }}>
              <div style={styles.emptyIcon}>
                <svg width="48" height="48" viewBox="0 0 48 48" fill="none">
                  <rect x="4" y="4" width="40" height="40" rx="20" fill={cssVar('primarySubtle')} />
                  <path d="M24 16v6m0 0v6m0-6h6m-6 0h-6" stroke={cssVar('primary')} strokeWidth="2" strokeLinecap="round" />
                </svg>
              </div>
              <div style={styles.emptyTitle}>{t('playground.empty_title')}</div>
              <div style={styles.emptyDesc}>{t('playground.empty_description')}</div>
              <button style={styles.emptyBtn} onClick={createConversation}>
                <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
                  <path d="M7 1v12M1 7h12" />
                </svg>
                {t('playground.new_conversation')}
              </button>
            </div>
          )}

          {activeId && messages.map((msg, messageIndex) => (
            <div key={msg.id} style={{ ...styles.messageRow, ...(isMobile ? styles.messageRowMobile : null) }}>
              <div style={msg.role === 'user' ? styles.avatarUser : styles.avatarAssistant}>
                {msg.role === 'user' ? (
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" />
                    <circle cx="12" cy="7" r="4" />
                  </svg>
                ) : (
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M12 2a4 4 0 0 1 4 4v2a4 4 0 0 1-8 0V6a4 4 0 0 1 4-4z" />
                    <path d="M6 12h12l2 8H4l2-8z" />
                  </svg>
                )}
              </div>
              <div style={styles.messageBody}>
                <div style={styles.messageHeader}>
                  <div style={styles.messageRole}>
                    {msg.role === 'user' ? t('playground.you') : t('playground.assistant')}
                  </div>
                </div>
                {msg.role === 'assistant' && msg.reasoning && (
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
                {msg.role === 'assistant' && (messageHasGeneratedImage(msg.content) || msg.model) && (() => {
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
          ))}

          {isActiveConversationStreaming && streamContent && (
            <div style={{ ...styles.messageRow, ...(isMobile ? styles.messageRowMobile : null) }}>
              <div style={styles.avatarAssistant}>
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M12 2a4 4 0 0 1 4 4v2a4 4 0 0 1-8 0V6a4 4 0 0 1 4-4z" />
                  <path d="M6 12h12l2 8H4l2-8z" />
                </svg>
              </div>
              <div style={styles.messageBody}>
                <div style={styles.messageHeader}>
                  <div style={styles.messageRole}>{t('playground.assistant')}</div>
                </div>
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
            <div style={{ ...styles.messageRow, ...(isMobile ? styles.messageRowMobile : null) }}>
              <div style={styles.avatarAssistant}>
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M12 2a4 4 0 0 1 4 4v2a4 4 0 0 1-8 0V6a4 4 0 0 1 4-4z" />
                  <path d="M6 12h12l2 8H4l2-8z" />
                </svg>
              </div>
              <div style={styles.messageBody}>
                <div style={styles.messageHeader}>
                  <div style={styles.messageRole}>{t('playground.assistant')}</div>
                </div>
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
            <div style={{ ...styles.inputWrapper, ...(isActiveConversationStreaming ? styles.inputWrapperStreaming : null) }}>
              {selectedModelIsImage && isEditPanelOpen && (
                <div style={{ ...styles.imageEditPanel, ...(isMobile ? styles.imageEditPanelMobile : null) }}>
                  <div style={{ ...styles.imageEditHeader, ...(isMobile ? styles.imageEditHeaderMobile : null) }}>
                    <div style={styles.imageEditTitleWrap}>
                      <span style={styles.imageEditTitle}>{t('playground.edit_image_region')}</span>
                      <span style={styles.imageEditSubtitle}>{editSource ? t('playground.edit_image_region_hint') : t('playground.choose_source_image_region_hint')}</span>
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
                        disabled={isActiveConversationStreaming}
                        aria-label="Close image editor"
                      >
                        ×
                      </button>
                    </div>
                  </div>

                  {editSource ? (
                    <div style={{ ...styles.imageEditBody, ...(isMobile ? styles.imageEditBodyMobile : null) }}>
                      <div ref={editCanvasContainerRef} style={styles.imageEditStageWrap}>
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
                      <div style={styles.imageEditSidePanel}>
                        <div style={styles.imageEditBadge}>{editSelection ? t('playground.region_selected') : t('playground.drag_to_select')}</div>
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
                </div>
              )}

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
                <span style={{ ...styles.inputHint, ...(isMobile ? styles.inputHintMobile : null) }}>{t('playground.input_hint')}</span>
                <div style={{ ...styles.inputButtonGroup, ...(isMobile ? styles.inputButtonGroupMobile : null) }}>
                  {selectedModelIsImage && (
                    <button
                      type="button"
                      style={{
                        ...styles.attachBtn,
                        ...(isEditPanelOpen ? styles.attachBtnActive : null),
                        ...(isMobile ? styles.actionBtnMobile : null),
                      }}
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
                    <button style={{ ...styles.stopBtn, ...(isMobile ? styles.actionBtnMobile : null) }} onClick={stopStreaming}>
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
    padding: '18px 16px 14px',
  },
  sidebarTitle: {
    fontSize: 11,
    fontWeight: 600,
    textTransform: 'uppercase' as const,
    letterSpacing: '0.08em',
    color: cssVar('textTertiary'),
  },
  newBtn: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 28,
    height: 28,
    border: `1px solid ${cssVar('border')}`,
    borderRadius: cssVar('radiusSm'),
    background: cssVar('bgSurface'),
    color: cssVar('textSecondary'),
    cursor: 'pointer',
    transition: cssVar('transition'),
  },
  convList: {
    flex: 1,
    overflowY: 'auto',
    padding: '4px 8px',
  },
  convItem: {
    display: 'flex',
    alignItems: 'flex-start',
    gap: 8,
    padding: '10px 10px',
    borderRadius: cssVar('radiusSm'),
    cursor: 'pointer',
    transition: cssVar('transition'),
    border: '1px solid transparent',
    marginBottom: 2,
  },
  convTitle: {
    flex: 1,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    fontSize: 13,
    lineHeight: '18px',
  },
  deleteBtn: {
    background: 'none',
    border: 'none',
    color: cssVar('textTertiary'),
    cursor: 'pointer',
    padding: '2px',
    lineHeight: 1,
    flexShrink: 0,
    opacity: 0.5,
    transition: cssVar('transition'),
    marginTop: 1,
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
  balanceBar: {
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'center',
    padding: '12px 16px',
    borderTop: `1px solid ${cssVar('borderSubtle')}`,
  },
  balanceLabel: {
    fontSize: 11,
    fontWeight: 500,
    textTransform: 'uppercase' as const,
    letterSpacing: '0.06em',
    color: cssVar('textTertiary'),
  },
  balanceValue: {
    fontSize: 13,
    fontWeight: 600,
    fontFamily: cssVar('fontMono'),
    color: cssVar('primary'),
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

  // ── Top bar ──
  topBar: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 16,
    padding: '8px 20px',
    borderBottom: `1px solid ${cssVar('borderSubtle')}`,
    background: cssVar('bg'),
    flexShrink: 0,
    minHeight: 52,
  },
  topBarMobile: {
    alignItems: 'flex-start',
    flexDirection: 'column',
    padding: '8px 12px',
    gap: 8,
  },
  topBarLeft: {
    display: 'flex',
    alignItems: 'center',
    gap: 12,
  },
  topBarLeftMobile: {
    width: '100%',
    alignItems: 'flex-start',
    gap: 8,
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
  selectors: {
    display: 'flex',
    alignItems: 'center',
    gap: 0,
    background: cssVar('bgSurface'),
    borderRadius: cssVar('radiusSm'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    overflow: 'hidden',
  },
  selectorsMobile: {
    flex: 1,
    minWidth: 0,
    display: 'grid',
    gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
    alignItems: 'stretch',
    gap: 6,
    padding: 6,
    overflow: 'visible',
  },
  selectorGroup: {
    display: 'flex',
    alignItems: 'center',
    gap: 6,
    padding: '6px 12px',
    minWidth: 0,
  },
  selectorGroupMobile: {
    minWidth: 0,
    padding: 0,
    flexDirection: 'column',
    alignItems: 'stretch',
    gap: 3,
  },
  selectorLabel: {
    fontSize: 10,
    fontWeight: 600,
    textTransform: 'uppercase' as const,
    letterSpacing: '0.08em',
    color: cssVar('textTertiary'),
    whiteSpace: 'nowrap',
  },
  selectorDivider: {
    width: 1,
    height: 24,
    background: cssVar('borderSubtle'),
    flexShrink: 0,
  },
  select: {
    padding: '2px 4px',
    border: 'none',
    background: 'transparent',
    color: cssVar('text'),
    fontSize: 13,
    fontWeight: 500,
    outline: 'none',
    cursor: 'pointer',
    fontFamily: cssVar('fontSans'),
    minWidth: 0,
  },
  selectMobile: {
    width: '100%',
    minHeight: 30,
    borderRadius: cssVar('radiusSm'),
    padding: '5px 7px',
    background: cssVar('bgDeep'),
    fontSize: 12,
  },
  imageSizeInlineControls: {
    display: 'flex',
    alignItems: 'center',
    gap: 6,
    minWidth: 0,
  },
  imageSizeInlineControlsMobile: {
    flexWrap: 'wrap',
  },
  imageSizeMiniSelect: {
    height: 28,
    maxWidth: 96,
    padding: '3px 6px',
    border: `1px solid ${cssVar('borderSubtle')}`,
    borderRadius: cssVar('radiusSm'),
    background: cssVar('bgDeep'),
    color: cssVar('text'),
    fontSize: 12,
    fontWeight: 600,
    outline: 'none',
    fontFamily: cssVar('fontSans'),
  },
  imageSizeMiniSelectMobile: {
    flex: '1 1 74px',
    maxWidth: 'none',
  },
  imageSizeInlinePreview: {
    minWidth: 82,
    padding: '3px 7px',
    borderRadius: cssVar('radiusSm'),
    background: cssVar('primarySubtle'),
    color: cssVar('primary'),
    fontSize: 12,
    fontWeight: 700,
    fontFamily: cssVar('fontMono'),
    whiteSpace: 'nowrap',
  },
  topBarTitle: {
    fontSize: 12,
    color: cssVar('textTertiary'),
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  },
  topBarTitleMobile: {
    width: '100%',
    display: 'none',
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
  emptyState: {
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    justifyContent: 'center',
    flex: 1,
    gap: 12,
    padding: 40,
    animation: 'pg-fadein 0.4s ease-out',
  },
  emptyStateMobile: {
    padding: '32px 20px',
  },
  emptyIcon: {
    marginBottom: 8,
  },
  emptyTitle: {
    fontSize: 18,
    fontWeight: 600,
    color: cssVar('text'),
    letterSpacing: '-0.02em',
  },
  emptyDesc: {
    fontSize: 13,
    color: cssVar('textSecondary'),
    maxWidth: 300,
    textAlign: 'center',
    lineHeight: 1.5,
  },
  emptyBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 8,
    padding: '10px 22px',
    border: 'none',
    borderRadius: cssVar('radiusMd'),
    background: cssVar('primary'),
    color: cssVar('textInverse'),
    fontSize: 14,
    fontWeight: 600,
    cursor: 'pointer',
    transition: cssVar('transition'),
    marginTop: 8,
    fontFamily: cssVar('fontSans'),
  },

  // ── Message row ──
  messageRow: {
    display: 'flex',
    gap: 14,
    padding: '20px 28px',
    animation: 'pg-fadein 0.25s ease-out',
    borderBottom: `1px solid ${cssVar('borderSubtle')}`,
  },
  messageRowMobile: {
    gap: 10,
    padding: '16px 14px',
  },
  avatarUser: {
    width: 30,
    height: 30,
    borderRadius: '50%',
    background: cssVar('primary'),
    color: cssVar('textInverse'),
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    flexShrink: 0,
  },
  avatarAssistant: {
    width: 30,
    height: 30,
    borderRadius: '50%',
    background: cssVar('bgSurface'),
    border: `1px solid ${cssVar('border')}`,
    color: cssVar('textSecondary'),
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    flexShrink: 0,
  },
  messageBody: {
    flex: 1,
    minWidth: 0,
  },
  messageHeader: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    minHeight: 24,
    marginBottom: 4,
  },
  messageRole: {
    fontSize: 12,
    fontWeight: 600,
    color: cssVar('text'),
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
    color: '#cfd6e6',
  },
  markdownParagraph: {
    margin: '0 0 11px',
  },
  markdownH1: {
    margin: '2px 0 14px',
    fontSize: 21,
    lineHeight: 1.25,
    color: '#eef4ff',
    letterSpacing: '-0.02em',
  },
  markdownH2: {
    margin: '18px 0 10px',
    fontSize: 17,
    lineHeight: 1.3,
    color: '#e8eefb',
    letterSpacing: '-0.01em',
  },
  markdownH3: {
    margin: '16px 0 8px',
    fontSize: 15,
    lineHeight: 1.35,
    color: '#dfe7f6',
  },
  markdownH4: {
    margin: '14px 0 8px',
    fontSize: 14,
    lineHeight: 1.4,
    color: '#d8e0ef',
  },
  markdownList: {
    margin: '0 0 12px',
    paddingLeft: 20,
    color: '#cfd6e6',
  },
  markdownListItem: {
    margin: '4px 0',
  },
  markdownBlockquote: {
    margin: '0 0 12px',
    padding: '9px 13px',
    borderLeft: '3px solid rgba(62, 207, 180, 0.48)',
    borderRadius: '0 10px 10px 0',
    background: 'rgba(62, 207, 180, 0.055)',
    color: '#aeb8ca',
  },
  markdownCodeBlock: {
    margin: '4px 0 14px',
    padding: '13px 15px',
    borderRadius: cssVar('radiusSm'),
    background: 'linear-gradient(180deg, rgba(17, 23, 36, 0.92), rgba(10, 14, 24, 0.92))',
    border: '1px solid rgba(148, 175, 225, 0.075)',
    color: '#d5deef',
    fontFamily: cssVar('fontMono'),
    fontSize: 12.5,
    lineHeight: 1.72,
    overflowX: 'auto',
    whiteSpace: 'pre',
    boxShadow: 'inset 0 1px 0 rgba(255, 255, 255, 0.025)',
  },
  markdownInlineCode: {
    padding: '1px 5px 2px',
    borderRadius: 6,
    background: 'rgba(125, 211, 252, 0.08)',
    border: '1px solid rgba(125, 211, 252, 0.11)',
    color: '#b8e7ff',
    fontFamily: cssVar('fontMono'),
    fontSize: '0.9em',
  },
  markdownLink: {
    color: '#6ee7d1',
    textDecoration: 'underline',
    textDecorationColor: 'rgba(110, 231, 209, 0.28)',
    textUnderlineOffset: 3,
  },
  markdownDivider: {
    height: 1,
    border: 0,
    background: 'linear-gradient(90deg, transparent, rgba(148, 175, 225, 0.14), transparent)',
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
    color: '#9ea9bd',
  },
  imageGroup: {
    display: 'flex',
    flexWrap: 'nowrap',
    alignItems: 'flex-start',
    gap: 16,
    maxWidth: '100%',
    margin: '10px 0 6px',
    paddingBottom: 8,
    overflowX: 'auto',
    overflowY: 'hidden',
    overscrollBehaviorX: 'contain',
    scrollSnapType: 'x proximity',
  },
  generatedImageFrame: {
    display: 'inline-flex',
    flexDirection: 'column',
    alignItems: 'flex-start',
    gap: 8,
    flex: '0 0 clamp(220px, 32vw, 420px)',
    maxWidth: 'min(100%, 420px)',
    scrollSnapAlign: 'start',
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
    background: 'rgba(10, 14, 24, 0.9)',
    border: `1px solid ${cssVar('borderSubtle')}`,
    color: cssVar('textSecondary'),
    fontSize: 12,
    boxShadow: '0 10px 28px rgba(0, 0, 0, 0.22)',
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
    padding: '16px 28px 20px',
    borderTop: `1px solid ${cssVar('borderSubtle')}`,
    background: cssVar('bg'),
    flexShrink: 0,
  },
  inputAreaMobile: {
    padding: '10px 12px 12px',
  },
  inputWrapper: {
    display: 'flex',
    flexDirection: 'column',
    gap: 8,
    border: `1px solid ${cssVar('border')}`,
    borderRadius: cssVar('radiusMd'),
    background: cssVar('bgSurface'),
    paddingTop: 10,
    paddingRight: 12,
    paddingBottom: 8,
    paddingLeft: 12,
    transition: cssVar('transition'),
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
    background: 'linear-gradient(180deg, rgba(19, 28, 43, 0.72), rgba(10, 15, 26, 0.72))',
    boxShadow: 'inset 0 1px 0 rgba(255, 255, 255, 0.035)',
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
    color: '#edf4ff',
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
    alignItems: 'center',
    gap: 8,
  },
  inputButtonGroup: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
  },
  inputButtonGroupMobile: {
    flex: 1,
    minWidth: 0,
    justifyContent: 'flex-end',
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
    padding: '6px 12px',
    border: `1px solid ${cssVar('border')}`,
    borderRadius: cssVar('radiusSm'),
    background: cssVar('bgSurface'),
    color: cssVar('textSecondary'),
    fontSize: 13,
    fontWeight: 600,
    cursor: 'pointer',
    transition: cssVar('transition'),
    fontFamily: cssVar('fontSans'),
  },
  attachBtnActive: {
    borderColor: cssVar('borderFocus'),
    background: cssVar('primarySubtle'),
    color: cssVar('primary'),
  },
  sendBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    padding: '6px 16px',
    border: 'none',
    borderRadius: cssVar('radiusSm'),
    background: cssVar('primary'),
    color: cssVar('textInverse'),
    fontSize: 13,
    fontWeight: 600,
    cursor: 'pointer',
    transition: cssVar('transition'),
    fontFamily: cssVar('fontSans'),
  },
  stopBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    padding: '6px 16px',
    border: 'none',
    borderRadius: cssVar('radiusSm'),
    background: cssVar('danger'),
    color: cssVar('textInverse'),
    fontSize: 13,
    fontWeight: 600,
    cursor: 'pointer',
    fontFamily: cssVar('fontSans'),
  },
  actionBtnMobile: {
    flex: 1,
    minWidth: 0,
    justifyContent: 'center',
  },
};
