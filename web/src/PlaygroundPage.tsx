import { useState, useEffect, useRef, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { cssVar } from '@airgate/theme';
import { api, chatCompletionsStream } from './api';
import type { APIKeyItem, Conversation, GroupItem, Message, Platform, ModelInfo, UserInfo } from './api';

const MOBILE_BREAKPOINT = 960;

export default function PlaygroundPage() {
  const { t } = useTranslation();

  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [activeId, setActiveId] = useState<number | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [streamContent, setStreamContent] = useState('');
  const [isStreaming, setIsStreaming] = useState(false);
  const [input, setInput] = useState('');

  const [platforms, setPlatforms] = useState<Platform[]>([]);
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [selectedPlatform, setSelectedPlatform] = useState('');
  const [selectedModel, setSelectedModel] = useState('');
  const [userInfo, setUserInfo] = useState<UserInfo | null>(null);
  const [availableKeys, setAvailableKeys] = useState<APIKeyItem[]>([]);
  const [availableGroups, setAvailableGroups] = useState<GroupItem[]>([]);
  const [selectedKeyId, setSelectedKeyId] = useState<number | null>(null);
  const [resolvedAPIKey, setResolvedAPIKey] = useState('');
  const [error, setError] = useState('');
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [isMobile, setIsMobile] = useState(() => (
    typeof window !== 'undefined' ? window.innerWidth <= MOBILE_BREAKPOINT : false
  ));

  const messagesEndRef = useRef<HTMLDivElement>(null);
  const abortRef = useRef<AbortController | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    api.listConversations().then(setConversations).catch(() => {});
    api.listPlatforms().then(p => {
      setPlatforms(p);
      if (p.length > 0) setSelectedPlatform(p[0].Name);
    }).catch(() => {});
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
    if (!selectedPlatform) return;
    api.listModels(selectedPlatform).then(m => {
      setModels(m);
      if (!m.some(item => item.id === selectedModel)) {
        setSelectedModel(m[0]?.id || '');
      }
    }).catch(() => {});
  }, [selectedPlatform, selectedModel]);

  useEffect(() => {
    if (!selectedKeyId) return;
    const selectedKey = availableKeys.find(item => item.id === selectedKeyId);
    if (!selectedKey?.group_id) return;
    const selectedGroup = availableGroups.find(item => item.id === selectedKey.group_id);
    if (!selectedGroup) return;
    if (selectedPlatform !== selectedGroup.platform) {
      setSelectedPlatform(selectedGroup.platform);
    }
  }, [selectedKeyId, availableKeys, availableGroups, selectedPlatform]);

  useEffect(() => {
    if (!activeId) { setMessages([]); return; }
    api.listMessages(activeId).then(setMessages).catch(() => {});
  }, [activeId]);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages, streamContent]);

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

  const resolveGroupID = useCallback(() => {
    if (userInfo?.api_key_id && userInfo.api_key_platform) {
      const sessionKey = availableKeys.find(item => item.id === userInfo.api_key_id);
      return sessionKey?.group_id || 0;
    }
    const selectedKey = availableKeys.find(item => item.id === selectedKeyId);
    return selectedKey?.group_id || 0;
  }, [availableKeys, selectedKeyId, userInfo]);

  const ensureAPIKey = useCallback(async () => {
    if (resolvedAPIKey) return resolvedAPIKey;
    if (userInfo?.api_key_id) {
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

  const createConversation = useCallback(async () => {
    try {
      const conv = await api.createConversation({
        title: '',
        group_id: resolveGroupID(),
        platform: selectedPlatform,
        model: selectedModel,
      });
      setConversations(prev => [conv, ...prev]);
      setActiveId(conv.id);
      setMessages([]);
      if (isMobile) {
        setSidebarOpen(false);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : t('playground.create_failed'));
    }
  }, [isMobile, resolveGroupID, selectedPlatform, selectedModel, t]);

  const deleteConversation = useCallback(async (id: number) => {
    try {
      await api.deleteConversation(id);
      setConversations(prev => prev.filter(c => c.id !== id));
      if (activeId === id) {
        setActiveId(null);
        setMessages([]);
      }
    } catch { /* ignore */ }
  }, [activeId]);

  const sendMessage = useCallback(async () => {
    if (!input.trim() || isStreaming || !activeId) return;

    const content = input.trim();
    const groupID = resolveGroupID();
    const requestMessages = [...messages, {
      id: Date.now(),
      conversation_id: activeId,
      role: 'user',
      content,
      platform: selectedPlatform,
      model: selectedModel,
      group_id: groupID,
      input_tokens: 0,
      output_tokens: 0,
      cost: 0,
      created_at: new Date().toISOString(),
    }];

    setInput('');
    setError('');
    setMessages(requestMessages);
    setIsStreaming(true);
    setStreamContent('');

    try {
      const apiKey = await ensureAPIKey();
      await api.persistMessage({
        conversation_id: activeId,
        role: 'user',
        content,
        platform: selectedPlatform,
        model: selectedModel,
        group_id: groupID,
      });

      const abort = new AbortController();
      abortRef.current = abort;

      let accumulated = '';
      await chatCompletionsStream(
        apiKey,
        {
          model: selectedModel,
          messages: requestMessages.map(msg => ({ role: msg.role, content: msg.content })),
          stream: true,
        },
        {
          onData: (text) => {
            accumulated += text;
            setStreamContent(accumulated);
          },
          onDone: async (usage) => {
            if (!accumulated) {
              setError(t('playground.no_response'));
              setStreamContent('');
              setIsStreaming(false);
              return;
            }
            const persisted = await api.persistMessage({
              conversation_id: activeId,
              role: 'assistant',
              content: accumulated,
              platform: selectedPlatform,
              model: usage.model || selectedModel,
              group_id: groupID,
              input_tokens: usage.input_tokens,
              output_tokens: usage.output_tokens,
              cost: usage.cost,
            });
            setMessages(prev => [...prev, persisted]);
            setConversations(prev => prev.map(c =>
              c.id === activeId && !c.title
                ? { ...c, title: content.slice(0, 30) + (content.length > 30 ? '...' : ''), updated_at: new Date().toISOString() }
                : c
            ));
            setStreamContent('');
            setIsStreaming(false);
          },
          onError: (err) => {
            setError(err);
            setIsStreaming(false);
            setStreamContent('');
          },
        },
        abort.signal,
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : 'stream failed');
      setIsStreaming(false);
      setStreamContent('');
    }
  }, [activeId, ensureAPIKey, input, isStreaming, messages, resolveGroupID, selectedModel, selectedPlatform, t]);

  const stopStreaming = useCallback(() => {
    abortRef.current?.abort();
    if (streamContent) {
      setMessages(prev => [...prev, {
        id: Date.now() + 1,
        conversation_id: activeId!,
        role: 'assistant',
        content: streamContent,
        platform: '', model: selectedModel, group_id: 0,
        input_tokens: 0, output_tokens: 0, cost: 0,
        created_at: new Date().toISOString(),
      }]);
    }
    setStreamContent('');
    setIsStreaming(false);
  }, [streamContent, activeId, selectedModel]);

  const activeConv = conversations.find(c => c.id === activeId);
  const hasSelectedAPIKey = Boolean(userInfo?.api_key_id || selectedKeyId);
  const canSendMessage = Boolean(input.trim()) && hasSelectedAPIKey && !isStreaming;

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
              {!userInfo?.api_key_id && (
                <>
                  <div style={{ ...styles.selectorGroup, ...(isMobile ? styles.selectorGroupMobile : null) }}>
                    <label style={styles.selectorLabel}>API Key</label>
                    <select
                      style={{ ...styles.select, ...(isMobile ? styles.selectMobile : null) }}
                      value={selectedKeyId ?? ''}
                      onChange={e => {
                        const nextID = Number(e.target.value || 0);
                        setSelectedKeyId(nextID || null);
                        setResolvedAPIKey('');
                      }}
                    >
                      <option value="">Select key</option>
                      {availableKeys.map(item => (
                        <option key={item.id} value={item.id}>{item.name}</option>
                      ))}
                    </select>
                  </div>

                  {!isMobile && <div style={styles.selectorDivider} />}
                </>
              )}

              <div style={{ ...styles.selectorGroup, ...(isMobile ? styles.selectorGroupMobile : null) }}>
                <label style={styles.selectorLabel}>{t('playground.platform')}</label>
                <select
                  style={{ ...styles.select, ...(isMobile ? styles.selectMobile : null) }}
                  value={selectedPlatform}
                  onChange={e => setSelectedPlatform(e.target.value)}
                  disabled={!!selectedKeyId || !!userInfo?.api_key_id}
                >
                  {platforms
                    .filter(p => !selectedKeyId || availableGroups.find(g => g.id === availableKeys.find(item => item.id === selectedKeyId)?.group_id)?.platform === p.Name)
                    .filter(p => !userInfo?.api_key_platform || userInfo.api_key_platform === p.Name)
                    .map(p => (
                      <option key={p.Name} value={p.Name}>{p.DisplayName || p.Name}</option>
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
                    <option key={m.id} value={m.id}>{m.name || m.id}</option>
                  ))}
                </select>
              </div>
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
                <div style={styles.messageRole}>
                  {msg.role === 'user' ? t('playground.you') : t('playground.assistant')}
                </div>
                <div style={styles.messageContent}>{msg.content}</div>
                {msg.role === 'assistant' && msg.model && (
                  <div style={styles.messageMeta}>
                    <span style={styles.metaBadge}>{msg.model}</span>
                  </div>
                )}
              </div>
            </div>
          ))}

          {isStreaming && streamContent && (
            <div style={{ ...styles.messageRow, ...(isMobile ? styles.messageRowMobile : null) }}>
              <div style={styles.avatarAssistant}>
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M12 2a4 4 0 0 1 4 4v2a4 4 0 0 1-8 0V6a4 4 0 0 1 4-4z" />
                  <path d="M6 12h12l2 8H4l2-8z" />
                </svg>
              </div>
              <div style={styles.messageBody}>
                <div style={styles.messageRole}>{t('playground.assistant')}</div>
                <div style={styles.messageContent}>{streamContent}</div>
                <div style={styles.messageMeta}>
                  <span style={styles.streamingDot} />
                  <span>{t('playground.streaming')}</span>
                </div>
              </div>
            </div>
          )}

          {isStreaming && !streamContent && (
            <div style={{ ...styles.messageRow, ...(isMobile ? styles.messageRowMobile : null) }}>
              <div style={styles.avatarAssistant}>
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M12 2a4 4 0 0 1 4 4v2a4 4 0 0 1-8 0V6a4 4 0 0 1 4-4z" />
                  <path d="M6 12h12l2 8H4l2-8z" />
                </svg>
              </div>
              <div style={styles.messageBody}>
                <div style={styles.messageRole}>{t('playground.assistant')}</div>
                <div style={{ ...styles.messageContent, opacity: 0.5 }}>
                  <span style={styles.thinkingDots}>{t('playground.thinking')}</span>
                </div>
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

          <div ref={messagesEndRef} />
        </div>

        {/* Input */}
        {activeId && (
          <div style={{ ...styles.inputArea, ...(isMobile ? styles.inputAreaMobile : null) }}>
            <div style={styles.inputWrapper}>
              <textarea
                ref={inputRef}
                style={styles.textarea}
                value={input}
                onChange={e => {
                  setInput(e.target.value);
                  autoResize(e.target);
                }}
                onKeyDown={handleKeyDown}
                placeholder={t('playground.input_placeholder')}
                rows={1}
                disabled={isStreaming}
              />
              <div style={{ ...styles.inputActions, ...(isMobile ? styles.inputActionsMobile : null) }}>
                <span style={{ ...styles.inputHint, ...(isMobile ? styles.inputHintMobile : null) }}>{t('playground.input_hint')}</span>
                {isStreaming ? (
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
    padding: '10px 14px',
    gap: 10,
  },
  topBarLeft: {
    display: 'flex',
    alignItems: 'center',
    gap: 12,
  },
  topBarLeftMobile: {
    width: '100%',
    alignItems: 'flex-start',
    gap: 10,
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
    flexWrap: 'wrap',
    alignItems: 'stretch',
    gap: 8,
    padding: 8,
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
    width: '100%',
    padding: 0,
    flexDirection: 'column',
    alignItems: 'stretch',
    gap: 4,
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
    minHeight: 34,
    borderRadius: cssVar('radiusSm'),
    padding: '6px 8px',
    background: cssVar('bgDeep'),
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
  },

  // ── Messages ──
  messagesArea: {
    flex: 1,
    minHeight: 0,
    overflowY: 'auto',
    display: 'flex',
    flexDirection: 'column',
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
  messageRole: {
    fontSize: 12,
    fontWeight: 600,
    marginBottom: 4,
    color: cssVar('text'),
  },
  messageContent: {
    fontSize: 14,
    lineHeight: 1.7,
    whiteSpace: 'pre-wrap',
    wordBreak: 'break-word',
    color: cssVar('text'),
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
    padding: '12px 14px 16px',
  },
  inputWrapper: {
    display: 'flex',
    flexDirection: 'column',
    gap: 8,
    border: `1px solid ${cssVar('border')}`,
    borderRadius: cssVar('radiusMd'),
    background: cssVar('bgSurface'),
    padding: '12px 14px 8px',
    transition: cssVar('transition'),
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
    lineHeight: 1.6,
    minHeight: 24,
    maxHeight: 200,
  },
  inputActions: {
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'center',
    gap: 12,
  },
  inputActionsMobile: {
    flexDirection: 'column',
    alignItems: 'stretch',
  },
  inputHint: {
    fontSize: 11,
    color: cssVar('textTertiary'),
  },
  inputHintMobile: {
    order: 2,
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
    width: '100%',
    justifyContent: 'center',
  },
};
