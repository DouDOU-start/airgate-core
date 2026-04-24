import { useState, useEffect, useRef, useCallback, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { cssVar } from '@airgate/theme';
import { api, chatCompletionsStream } from './api';
import type { APIKeyItem, ChatMessageContent, Conversation, GroupItem, Message, ModelInfo, UserInfo } from './api';

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
const DATA_IMAGE_RE = /^data:image\/(png|jpeg|jpg|webp|gif);base64,/i;
const REASONING_MODEL_RE = /(^|[-_])(?:gpt-?5|o[134]|codex)(?:[-_.]|$)/i;
const MAX_IMAGE_BYTES = 10 * 1024 * 1024;
const IMAGE_SIZE_OPTIONS: Array<{ value: ImageSize; label: string }> = [
  { value: '1024x1024', label: 'Square 1024x1024' },
  { value: '1024x1536', label: 'Portrait 1024x1536' },
  { value: '1536x1024', label: 'Landscape 1536x1024' },
];
type ReasoningEffort = 'minimal' | 'low' | 'medium' | 'high' | 'xhigh';
type ImageSize = '1024x1024' | '1024x1536' | '1536x1024';
type PendingImage = { id: string; name: string; url: string };
type MessageContentOptions = {
  onImageDownload?: (url: string, alt: string) => void;
  imageDownloadTitle?: string;
};

function stripImageMarkdown(content: string) {
  return content.replace(IMAGE_MARKDOWN_RE, '[Image generated]').trim() || '[Image generated]';
}

function copyableMessageText(content: string) {
  return content.replace(IMAGE_MARKDOWN_RE, '[Image]').trim() || '[Image]';
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

function messageContentWithImages(text: string, images: PendingImage[]) {
  const body = text.trim();
  const imageMarkdown = images.map(image => `![${escapeMarkdownAlt(image.name)}](${image.url})`).join('\n');
  return [body, imageMarkdown].filter(Boolean).join('\n\n');
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

function titleFromMessageContent(content: string) {
  const title = content.replace(IMAGE_MARKDOWN_RE, '[Image]').trim() || '[Image]';
  return title.slice(0, 30) + (title.length > 30 ? '...' : '');
}

function toChatMessageContent(role: string, content: string): ChatMessageContent {
  if (role !== 'user') return stripImageMarkdown(content);

  const parts: Exclude<ChatMessageContent, string> = [];
  let lastIndex = 0;
  let match: RegExpExecArray | null;
  IMAGE_MARKDOWN_RE.lastIndex = 0;

  while ((match = IMAGE_MARKDOWN_RE.exec(content)) !== null) {
    const text = content.slice(lastIndex, match.index).trim();
    if (text) parts.push({ type: 'text', text });
    parts.push({ type: 'image_url', image_url: { url: match[1] } });
    lastIndex = match.index + match[0].length;
  }

  const tail = content.slice(lastIndex).trim();
  if (tail) parts.push({ type: 'text', text: tail });

  return parts.length ? parts : content;
}

function isImageModel(model?: ModelInfo) {
  return Boolean(model?.image_only || model?.capabilities?.includes('image_generation'));
}

function supportsReasoning(model?: ModelInfo) {
  if (!model || isImageModel(model)) return false;
  return Boolean(model.capabilities?.includes('reasoning') || model.capabilities?.includes('thinking') || REASONING_MODEL_RE.test(model.id));
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
  const image = <img src={url} alt={alt} style={styles.generatedImage} loading="lazy" />;
  if (!options.onImageDownload) {
    return <span key={key} style={styles.generatedImageFrame}>{image}</span>;
  }

  return (
    <span key={key} style={styles.generatedImageFrame}>
      {image}
      <button
        type="button"
        style={styles.imageDownloadBtn}
        title={options.imageDownloadTitle || 'Download image'}
        aria-label={options.imageDownloadTitle || 'Download image'}
        onClick={(event) => {
          event.stopPropagation();
          options.onImageDownload?.(url, alt);
        }}
      >
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
          <path d="M7 10l5 5 5-5" />
          <path d="M12 15V3" />
        </svg>
        <span>Download</span>
      </button>
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
      nodes.push(renderGeneratedImage(key, imageUrl, imageAlt || 'Generated image', options));
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
  const images: Array<{ alt: string; url: string }> = [];
  let match: RegExpExecArray | null;
  IMAGE_MARKDOWN_ITEM_RE.lastIndex = 0;

  while ((match = IMAGE_MARKDOWN_ITEM_RE.exec(text)) !== null) {
    images.push({ alt: match[1], url: match[2] });
  }

  const remainder = text.replace(IMAGE_MARKDOWN_ITEM_RE, '').trim();
  if (!images.length || remainder) return null;

  return (
    <div key={key} style={styles.imageGroup}>
      {images.map((image, index) => renderGeneratedImage(`${key}-${index}`, image.url, image.alt || 'Generated image', options))}
    </div>
  );
}

function renderMessageContent(content: string, options: MessageContentOptions = {}) {
  const lines = content.replace(/\r\n?/g, '\n').split('\n');
  const nodes: ReactNode[] = [];
  let paragraph: string[] = [];
  let quote: string[] = [];
  let listItems: Array<{ text: string; ordered: boolean }> = [];
  let codeLines: string[] = [];
  let inCodeBlock = false;
  let nodeIndex = 0;

  const nextKey = (type: string) => `${type}-${nodeIndex++}`;
  const flushParagraph = () => {
    if (!paragraph.length) return;
    const key = nextKey('p');
    const text = paragraph.join('\n');
    nodes.push(renderImageGroup(text, key, options) || <p key={key} style={styles.markdownParagraph}>{renderInlineMarkdown(text, key, options)}</p>);
    paragraph = [];
  };
  const flushQuote = () => {
    if (!quote.length) return;
    const key = nextKey('quote');
    nodes.push(<blockquote key={key} style={styles.markdownBlockquote}>{renderInlineMarkdown(quote.join('\n'), key, options)}</blockquote>);
    quote = [];
  };
  const flushList = () => {
    if (!listItems.length) return;
    const key = nextKey('list');
    const children = listItems.map((item, index) => (
      <li key={`${key}-${index}`} style={styles.markdownListItem}>{renderInlineMarkdown(item.text, `${key}-${index}`, options)}</li>
    ));
    nodes.push(listItems[0].ordered ? <ol key={key} style={styles.markdownList}>{children}</ol> : <ul key={key} style={styles.markdownList}>{children}</ul>);
    listItems = [];
  };
  const flushBlocks = () => {
    flushParagraph();
    flushQuote();
    flushList();
  };
  const flushCodeBlock = () => {
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
        flushBlocks();
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
      flushBlocks();
      nodes.push(renderHeading(Math.min(headingMatch[1].length, 4), headingMatch[2].trim(), nextKey('heading'), options));
      continue;
    }

    if (/^\s*([-*_])\s*(\1\s*){2,}$/.test(line)) {
      flushBlocks();
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

  return nodes.length > 0 ? nodes : content;
}

export default function PlaygroundPage() {
  const { t } = useTranslation();

  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [activeId, setActiveId] = useState<number | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [streamConversationId, setStreamConversationId] = useState<number | null>(null);
  const [streamContent, setStreamContent] = useState('');
  const [streamReasoning, setStreamReasoning] = useState('');
  const [isStreaming, setIsStreaming] = useState(false);
  const [input, setInput] = useState('');
  const [pendingImages, setPendingImages] = useState<PendingImage[]>([]);

  const [models, setModels] = useState<ModelInfo[]>([]);
  const [selectedModel, setSelectedModel] = useState('');
  const [reasoningEffort, setReasoningEffort] = useState<ReasoningEffort>('medium');
  const [imageSize, setImageSize] = useState<ImageSize>('1024x1024');
  const [userInfo, setUserInfo] = useState<UserInfo | null>(null);
  const [availableKeys, setAvailableKeys] = useState<APIKeyItem[]>([]);
  const [availableGroups, setAvailableGroups] = useState<GroupItem[]>([]);
  const [selectedKeyId, setSelectedKeyId] = useState<number | null>(null);
  const [resolvedAPIKey, setResolvedAPIKey] = useState('');
  const [error, setError] = useState('');
  const [interactionNotice, setInteractionNotice] = useState('');
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [isMobile, setIsMobile] = useState(() => (
    typeof window !== 'undefined' ? window.innerWidth <= MOBILE_BREAKPOINT : false
  ));

  const messagesEndRef = useRef<HTMLDivElement>(null);
  const abortRef = useRef<AbortController | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const activeIdRef = useRef<number | null>(null);
  const streamContextRef = useRef<{ conversationId: number; model: string } | null>(null);
  const skipNextMessagesLoadRef = useRef<number | null>(null);

  useEffect(() => {
    api.listConversations().then(setConversations).catch(() => {});
    api.getUserInfo().then(async info => {
      setUserInfo(info);
      const sessionKey = sessionStorage.getItem('apikey_session_secret') || '';
      if (info.api_key_id && sessionKey) {
        setSelectedKeyId(info.api_key_id);
        setResolvedAPIKey(sessionKey);
      }
    }).catch(() => {});
    api.listAPIKeys().then(resp => setAvailableKeys(resp.list.filter(item => item.status === 'active' && item.group_id != null))).catch(() => {});
    api.listGroups().then(resp => setAvailableGroups(resp.list)).catch(() => {});
  }, []);

  useEffect(() => {
    let cancelled = false;

    const loadModels = async () => {
      const sessionKey = userInfo?.api_key_id && (!selectedKeyId || selectedKeyId === userInfo.api_key_id)
        ? sessionStorage.getItem('apikey_session_secret') || ''
        : '';
      let apiKey = resolvedAPIKey || sessionKey;

      try {
        if (!apiKey && selectedKeyId) {
          const revealed = await api.revealAPIKey(selectedKeyId);
          apiKey = revealed.key || '';
          if (!cancelled) setResolvedAPIKey(apiKey);
        }

        if (!apiKey) {
          if (!cancelled) {
            setModels([]);
            setSelectedModel('');
          }
          return;
        }

        const nextModels = await api.listModelsByAPIKey(apiKey);
        if (cancelled) return;
        setModels(nextModels);
        setSelectedModel(current => (
          nextModels.some(item => item.id === current) ? current : nextModels[0]?.id || ''
        ));
      } catch (e) {
        if (cancelled) return;
        setModels([]);
        setSelectedModel('');
        setError(e instanceof Error ? e.message : 'Failed to load models');
      }
    };

    loadModels();
    return () => { cancelled = true; };
  }, [resolvedAPIKey, selectedKeyId, userInfo?.api_key_id]);

  useEffect(() => {
    activeIdRef.current = activeId;
  }, [activeId]);

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

  const resolveGroupID = useCallback(() => {
    const keyID = selectedKeyId || userInfo?.api_key_id;
    const selectedKey = availableKeys.find(item => item.id === keyID);
    return selectedKey?.group_id || 0;
  }, [availableKeys, selectedKeyId, userInfo]);

  const selectedGroupPlatform = (() => {
    const groupID = resolveGroupID();
    if (groupID) {
      return availableGroups.find(item => item.id === groupID)?.platform || '';
    }
    return userInfo?.api_key_platform || '';
  })();

  const selectedModelInfo = models.find(item => item.id === selectedModel);
  const selectedModelIsImage = isImageModel(selectedModelInfo);
  const selectedModelSupportsReasoning = supportsReasoning(selectedModelInfo);

  const ensureAPIKey = useCallback(async () => {
    if (resolvedAPIKey) return resolvedAPIKey;
    if (userInfo?.api_key_id && (!selectedKeyId || selectedKeyId === userInfo.api_key_id)) {
      const sessionKey = sessionStorage.getItem('apikey_session_secret') || '';
      if (sessionKey) {
        setResolvedAPIKey(sessionKey);
        return sessionKey;
      }
    }
    if (!selectedKeyId) {
      throw new Error('API key required');
    }
    const revealed = await api.revealAPIKey(selectedKeyId);
    if (!revealed.key) {
      throw new Error('Failed to reveal API key');
    }
    setResolvedAPIKey(revealed.key);
    return revealed.key;
  }, [resolvedAPIKey, selectedKeyId, userInfo]);

  const createConversation = useCallback(() => {
    const now = new Date().toISOString();
    const draft: Conversation = {
      id: DRAFT_CONVERSATION_ID,
      user_id: userInfo?.id || 0,
      title: '',
      group_id: resolveGroupID(),
      platform: selectedGroupPlatform,
      model: selectedModel,
      created_at: now,
      updated_at: now,
    };
    setConversations(prev => [draft, ...prev.filter(c => c.id !== DRAFT_CONVERSATION_ID)]);
    setActiveId(DRAFT_CONVERSATION_ID);
    setMessages([]);
    setPendingImages([]);
    setError('');
    if (isMobile) {
      setSidebarOpen(false);
    }
  }, [isMobile, resolveGroupID, selectedGroupPlatform, selectedModel, userInfo?.id]);

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
      platform: selectedGroupPlatform,
      model: selectedModel,
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
    setMessages(requestMessages);
    setIsStreaming(true);
    setStreamConversationId(conversationID);
    streamContextRef.current = { conversationId: conversationID, model: selectedModel };
    setStreamContent('');
    setStreamReasoning('');

    try {
      const apiKey = await ensureAPIKey();
      if (conversationID === DRAFT_CONVERSATION_ID) {
        const conv = await api.createConversation({
          title: '',
          group_id: groupID,
          platform: selectedGroupPlatform,
          model: selectedModel,
        });
        conversationID = conv.id;
        streamContextRef.current = { conversationId: conv.id, model: selectedModel };
        setStreamConversationId(conv.id);
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
        platform: selectedGroupPlatform,
        model: selectedModel,
        group_id: groupID,
      });

      const abort = new AbortController();
      abortRef.current = abort;

      let accumulated = '';
      let accumulatedReasoning = '';
      await chatCompletionsStream(
        apiKey,
        {
          model: selectedModel,
          messages: requestMessages.map(msg => ({ role: msg.role, content: toChatMessageContent(msg.role, msg.content) })),
          stream: true,
          ...(selectedModelIsImage ? { size: imageSize } : {}),
          ...(selectedModelSupportsReasoning ? { reasoning_effort: reasoningEffort } : {}),
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
          onDone: async (usage) => {
            if (!accumulated) {
              if (activeIdRef.current === conversationID) {
                setError(t('playground.no_response'));
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
              platform: selectedGroupPlatform,
              model: usage.model || selectedModel,
              group_id: groupID,
              input_tokens: usage.input_tokens,
              output_tokens: usage.output_tokens,
              cost: usage.cost,
            });
            if (activeIdRef.current === conversationID) {
              setMessages(prev => [...prev, persisted]);
            }
            setConversations(prev => prev.map(c =>
              c.id === conversationID && !c.title
                ? { ...c, title: titleFromMessageContent(content), updated_at: new Date().toISOString() }
                : c
            ));
            setStreamContent('');
            setStreamReasoning('');
            setStreamConversationId(null);
            streamContextRef.current = null;
            setIsStreaming(false);
          },
          onError: (err) => {
            if (activeIdRef.current === conversationID) {
              setError(err);
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
      }
      setIsStreaming(false);
      setStreamContent('');
      setStreamReasoning('');
      setStreamConversationId(null);
      streamContextRef.current = null;
    }
  }, [activeId, ensureAPIKey, imageSize, input, isStreaming, messages, pendingImages, reasoningEffort, resolveGroupID, selectedModel, selectedGroupPlatform, selectedModelIsImage, selectedModelSupportsReasoning, t]);

  const addImageFiles = useCallback(async (files: File[]) => {
    if (!files.length) return;

    try {
      const nextImages = await imagesFromFiles(files);
      if (!nextImages.length) return;
      setPendingImages(prev => [...prev, ...nextImages]);
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to read image');
    }
  }, []);

  const handleImageChange = useCallback(async (e: React.ChangeEvent<HTMLInputElement>) => {
    await addImageFiles(Array.from(e.target.files || []));
    e.target.value = '';
  }, [addImageFiles]);

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
    setIsStreaming(false);
  }, [streamContent, streamReasoning]);

  const activeConv = conversations.find(c => c.id === activeId);
  const isActiveConversationStreaming = isStreaming && streamConversationId === activeId;
  const hasSelectedAPIKey = Boolean(userInfo?.api_key_id || selectedKeyId);
  const canSendMessage = Boolean(input.trim() || pendingImages.length > 0) && hasSelectedAPIKey && !isStreaming;

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      if (!hasSelectedAPIKey) {
        setError('API key required');
        return;
      }
      sendMessage();
    }
  }, [hasSelectedAPIKey, sendMessage]);

  const triggerImagePicker = useCallback(() => {
    fileInputRef.current?.click();
  }, []);

  const autoResize = useCallback((el: HTMLTextAreaElement) => {
    el.style.height = 'auto';
    el.style.height = Math.min(el.scrollHeight, 200) + 'px';
  }, []);

  const openConversation = useCallback((id: number) => {
    setActiveId(id);
    if (isMobile) {
      setSidebarOpen(false);
    }
  }, [isMobile]);

  const handleImageDownload = useCallback((url: string, alt: string) => {
    void downloadImage(url, alt)
      .then(() => setInteractionNotice('Image download started'))
      .catch(() => setInteractionNotice('Image download failed'));
  }, []);

  const handleMessageCopy = useCallback((content: string) => {
    void copyText(content)
      .then(() => setInteractionNotice('Message copied'))
      .catch(() => setInteractionNotice('Copy failed'));
  }, []);

  const interactiveMessageOptions = {
    onImageDownload: handleImageDownload,
    imageDownloadTitle: 'Download image',
  };

  const renderCopyButton = (content: string, label = 'Copy message', preventToggle = false) => (
    <button
      type="button"
      style={styles.messageCopyBtn}
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

  return (
    <div data-full-bleed style={styles.layout}>
      {sidebarOpen && isMobile && (
        <div
          style={styles.sidebarBackdrop}
          onClick={() => setSidebarOpen(false)}
        />
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
                <label style={styles.selectorLabel}>API Key</label>
                <select
                  style={{ ...styles.select, ...(isMobile ? styles.selectMobile : null) }}
                  value={selectedKeyId ?? userInfo?.api_key_id ?? ''}
                  onChange={e => {
                    const nextID = Number(e.target.value || 0);
                    setSelectedKeyId(nextID || null);
                    setResolvedAPIKey('');
                  }}
                >
                  <option value="">Select key</option>
                  {userInfo?.api_key_id && !availableKeys.some(item => item.id === userInfo.api_key_id) && (
                    <option value={userInfo.api_key_id}>{userInfo.api_key_name || 'Current key'}</option>
                  )}
                  {availableKeys.map(item => (
                    <option key={item.id} value={item.id}>{item.name}</option>
                  ))}
                </select>
              </div>

              {!isMobile && <div style={styles.selectorDivider} />}

              <div style={{ ...styles.selectorGroup, ...(isMobile ? styles.selectorGroupMobile : null) }}>
                <label style={styles.selectorLabel}>{t('playground.model')}</label>
                <select
                  style={{ ...styles.select, ...(isMobile ? styles.selectMobile : null) }}
                  value={selectedModel}
                  onChange={e => setSelectedModel(e.target.value)}
                >
                  {models.map(m => (
                    <option key={m.id} value={m.id}>
                      {m.name || m.id}{isImageModel(m) ? ' · image' : supportsReasoning(m) ? ' · reasoning' : ''}
                    </option>
                  ))}
                </select>
              </div>

              {selectedModelIsImage && (
                <>
                  {!isMobile && <div style={styles.selectorDivider} />}
                  <div style={{ ...styles.selectorGroup, ...(isMobile ? styles.selectorGroupMobile : null) }}>
                    <label style={styles.selectorLabel}>Size</label>
                    <select
                      style={{ ...styles.select, ...(isMobile ? styles.selectMobile : null) }}
                      value={imageSize}
                      onChange={e => setImageSize(e.target.value as ImageSize)}
                    >
                      {IMAGE_SIZE_OPTIONS.map(option => (
                        <option key={option.value} value={option.value}>{option.label}</option>
                      ))}
                    </select>
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

          {activeId && messages.map(msg => (
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
                  {renderCopyButton(copyableMessageText(msg.content))}
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
                <div style={styles.messageContent}>
                  {renderMessageContent(msg.content, interactiveMessageOptions)}
                </div>
                {msg.role === 'assistant' && msg.model && (
                  <div style={styles.messageMeta}>
                    <span style={styles.metaBadge}>{msg.model}</span>
                  </div>
                )}
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
                  {renderCopyButton(copyableMessageText(streamContent))}
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
                <div style={styles.messageContent}>
                  {renderMessageContent(streamContent, interactiveMessageOptions)}
                </div>
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

          {error && (
            <div style={{ ...styles.errorBar, ...(isMobile ? styles.errorBarMobile : null) }}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="12" cy="12" r="10" />
                <path d="M12 8v4m0 4h.01" />
              </svg>
              {error}
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
              <div style={{ ...styles.inputActions, ...(isMobile ? styles.inputActionsMobile : null) }}>
                <span style={{ ...styles.inputHint, ...(isMobile ? styles.inputHintMobile : null) }}>{t('playground.input_hint')}</span>
                <div style={{ ...styles.inputButtonGroup, ...(isMobile ? styles.inputButtonGroupMobile : null) }}>
                  <button
                    type="button"
                    style={{ ...styles.attachBtn, ...(isMobile ? styles.actionBtnMobile : null) }}
                    onClick={triggerImagePicker}
                    disabled={isActiveConversationStreaming}
                    title="Attach images"
                  >
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <rect x="3" y="3" width="18" height="18" rx="2" />
                      <circle cx="8.5" cy="8.5" r="1.5" />
                      <path d="M21 15l-5-5L5 21" />
                    </svg>
                    Image
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
                      onClick={sendMessage}
                      disabled={!canSendMessage}
                      title={hasSelectedAPIKey ? undefined : 'Select an API key first'}
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
    flexWrap: 'wrap',
    alignItems: 'flex-start',
    gap: 16,
    margin: '10px 0 12px',
  },
  generatedImageFrame: {
    display: 'inline-flex',
    flexDirection: 'column',
    alignItems: 'flex-start',
    gap: 8,
    flex: '1 1 260px',
    maxWidth: 'min(100%, 420px)',
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
  imageDownloadBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    gap: 6,
    minHeight: 30,
    padding: '5px 10px',
    borderRadius: '999px',
    border: `1px solid ${cssVar('borderSubtle')}`,
    background: cssVar('bgSurface'),
    color: cssVar('textSecondary'),
    fontFamily: cssVar('fontSans'),
    fontSize: 12,
    fontWeight: 500,
    cursor: 'pointer',
    transition: cssVar('transition'),
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
    padding: '10px 12px 8px',
    transition: cssVar('transition'),
  },
  inputWrapperStreaming: {
    paddingTop: 8,
    paddingBottom: 8,
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
