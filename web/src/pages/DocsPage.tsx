import { Link, useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { useState, useMemo, useEffect, useRef, useCallback, type ReactNode } from 'react';
import { Button, Link as HeroLink } from '@heroui/react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { useSiteSettings, defaultLogoUrl } from '../app/providers/SiteSettingsProvider';
import { useTheme } from '../app/providers/ThemeProvider';
import { useClipboard } from '../shared/hooks/useClipboard';
import { getToken } from '../shared/api/client';
import { Sun, Moon, Activity, Copy, Check, ArrowRight, BookOpen } from 'lucide-react';

// 顶栏高度（含 sticky nav 的 padding）。所有"滚到指定 H2"的位置都需要在
// 视口顶部下方留出这么多空间，否则 H2 会被顶栏遮住。
const HEADER_OFFSET = 96;

// 内置默认文档：通过 Vite 的 `?raw` 后缀以纯字符串形式导入。
// 想改文档内容？直接编辑 src/content/default-docs.md 即可，无需触碰本组件。
import defaultDocsRaw from '../content/default-docs.md?raw';

/**
 * 内置默认文档页。
 *
 * 设计要点：
 *   - 当管理员未在「站点品牌 → 文档链接」填外部 URL 时，所有"文档"按钮 fallback 到这里
 *   - 公共可访问，独立布局（不挂 AppShell），登录前后都能进
 *   - 内容来自 `src/content/default-docs.md`，由 react-markdown + remark-gfm 渲染。
 *     渲染前会把若干占位符（{{site_name}} / {{base_url}} / {{install_command}}）替换成
 *     当前站点的实际值，方便用户直接复制示例代码。
 */
export default function DocsPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const site = useSiteSettings();
  const { theme, toggleTheme } = useTheme();
  const isLoggedIn = !!getToken();

  const siteName = site.site_name || 'AirGate';
  // 与后端 openclaw handler 的 BaseURL 推导逻辑保持一致：优先 api_base_url，再回退到 origin。
  const baseUrl = (
    site.api_base_url || (typeof window !== 'undefined' ? window.location.origin : '')
  ).replace(/\/$/, '');
  const installCommand = `curl -fsSL ${baseUrl}/openclaw/install.sh -o openclaw-install.sh && bash openclaw-install.sh`;
  const installCommandPowerShell = `iwr -useb ${baseUrl}/openclaw/install.ps1 | iex`;

  // 占位符替换：把 markdown 文本中所有 your-airgate.example.com / {{site_name}} 等
  // 全局替换为当前站点真实值。注意 markdown 源文件里用的是 your-airgate.example.com 这种
  // 字面量，方便单独编辑预览，所以这里也对它做替换。
  const markdown = useMemo(() => {
    return defaultDocsRaw
      .replace(/\{\{site_name\}\}/g, siteName)
      .replace(/\{\{base_url\}\}/g, baseUrl)
      .replace(/\{\{install_command\}\}/g, installCommand)
      .replace(/\{\{install_command_powershell\}\}/g, installCommandPowerShell)
      .replace(/https:\/\/your-airgate\.example\.com/g, baseUrl || 'https://your-airgate.example.com')
      .replace(/AirGate/g, siteName);
  }, [siteName, baseUrl, installCommand, installCommandPowerShell]);

  // 从 markdown 源里抽取 H2 作为目录。简单正则按行匹配 `## xxx`，
  // 不会被代码块里的 ## 干扰（围栏内的内容会被跳过）。
  const toc = useMemo(() => extractH2(markdown), [markdown]);

  // ===== H2 元素的 ref 注册表 =====
  //
  // 不再依赖 id + querySelector / location.hash 这套机制。直接把每个 H2
  // 渲染时的 DOM 元素塞进 h2RefsRef，TOC 点击和 scroll-spy 都直接读这个数组。
  // 这样：
  //   1) 不依赖 id 字符串和 React reconciler 的属性更新时机
  //   2) 不会被 TanStack Router 的 hash 处理逻辑干扰
  //   3) 不依赖 ReactMarkdown 渲染 h2 component 的调用顺序（用 dataset 标号）
  const h2RefsRef = useRef<(HTMLHeadingElement | null)[]>([]);

  // ===== TOC 高亮 =====
  //
  // 用 index 而不是 id 字符串，避免任何"id 取错"的可能性。
  // -1 表示还没滚动到任何小节（停在文档顶部），此时 TOC 不高亮任何项。
  const [activeIndex, setActiveIndex] = useState<number>(-1);

  // ===== 点击 TOC：滚到指定 H2 =====
  //
  // window.scrollTo + scrollY 计算绝对位置，比 scrollIntoView 更可控：
  //   - 能精确扣掉顶栏高度（HEADER_OFFSET），让 H2 落在顶栏正下方
  //   - 顺便用 history.replaceState 把 hash 写到 URL，方便分享，
  //     但 replaceState 不会触发 popstate / router 导航，所以不会被打断
  const scrollToIndex = useCallback((idx: number) => {
    const el = h2RefsRef.current[idx];
    if (!el) return;
    const top = el.getBoundingClientRect().top + window.scrollY - HEADER_OFFSET;
    window.scrollTo({ top, behavior: 'smooth' });
    setActiveIndex(idx);
    if (typeof history !== 'undefined' && history.replaceState) {
      history.replaceState(null, '', `#section-${idx + 1}`);
    }
  }, []);

  // ===== Scroll-spy =====
  //
  // 在 scroll 事件里直接遍历 h2RefsRef，找"最后一个 top 已经越过 HEADER_OFFSET+30
  // 这条线"的 H2，作为当前 active。这是 docs 站常用的"过线即激活"语义：
  //   - 短小节也能激活（不需要 H2 落在窄带里）
  //   - 用户读正文中间时，最后越线的 H2 仍然 active，不会卡死
  // RAF throttle 防止 scroll 事件密集触发引起多余 setState。
  useEffect(() => {
    if (toc.length === 0) return;

    const SPY_OFFSET = HEADER_OFFSET + 24; // 越过顶栏再多 24px 才算激活
    let raf = 0;

    const onScroll = () => {
      if (raf) return;
      raf = requestAnimationFrame(() => {
        raf = 0;
        const refs = h2RefsRef.current;
        let next = -1;
        for (let i = 0; i < refs.length; i++) {
          const el = refs[i];
          if (!el) continue;
          if (el.getBoundingClientRect().top - SPY_OFFSET <= 0) {
            next = i; // 继续往后找，最终会落在"最后一个越线的"
          } else {
            break; // H2 都是按文档顺序排的，遇到第一个未越线就可以停
          }
        }
        // 函数式 setState 防止 stale 比较；React 自带相同值跳过 re-render
        setActiveIndex((prev) => (prev === next ? prev : next));
      });
    };

    onScroll(); // 首次同步一下，处理刷新后已经在中段的情况
    window.addEventListener('scroll', onScroll, { passive: true });
    return () => {
      window.removeEventListener('scroll', onScroll);
      if (raf) cancelAnimationFrame(raf);
    };
  }, [toc.length]);

  // ===== mount 时如果 URL 已经带 #section-N，主动滚一次 =====
  //
  // 浏览器原生只在初始导航时尝试根据 hash 滚动，但那一刻 React 还没把
  // h2 渲染出来，原生滚动会失败。这里在 ref 都填好之后再滚一次。
  useEffect(() => {
    if (toc.length === 0) return;
    const hash = typeof window !== 'undefined' ? window.location.hash : '';
    const m = /^#section-(\d+)$/.exec(hash);
    if (!m || !m[1]) return;
    const n = parseInt(m[1], 10);
    if (Number.isNaN(n) || n < 1 || n > toc.length) return;
    // 等一帧让 ref 注册完毕
    requestAnimationFrame(() => {
      const el = h2RefsRef.current[n - 1];
      if (el) {
        const top = el.getBoundingClientRect().top + window.scrollY - HEADER_OFFSET;
        window.scrollTo({ top, behavior: 'auto' });
        setActiveIndex(n - 1);
      }
    });
  }, [toc.length]);

  return (
    <div className="min-h-screen bg-bg-deep text-text">
      {/* 顶栏：拉到 7xl，和正文同宽，避免顶栏窄、正文宽的撕裂感 */}
      <nav className="sticky top-0 z-20 bg-bg-deep/80 backdrop-blur border-b border-border/50">
        <div className="flex items-center justify-between px-6 md:px-12 py-4 max-w-7xl mx-auto">
          <Link to="/home" className="flex items-center gap-2.5">
            <img src={site.site_logo || defaultLogoUrl} alt="" className="w-8 h-8 rounded-sm object-cover" />
            <span className="text-base font-bold">{siteName}</span>
          </Link>
          <div className="flex items-center gap-2">
            <HeroLink
              href="/status"
              className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-text-secondary hover:text-text transition-colors"
            >
              <Activity className="w-3.5 h-3.5" />
              {t('nav.status')}
            </HeroLink>
            <Button
              aria-label={theme === 'dark' ? '切换亮色模式' : '切换暗色模式'}
              isIconOnly
              size="sm"
              variant="ghost"
              onPress={toggleTheme}
            >
              {theme === 'dark' ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
            </Button>
            <Button
              className="ml-2"
              size="sm"
              variant="primary"
              onPress={() => navigate({ to: isLoggedIn ? '/' : '/login' })}
            >
              {isLoggedIn ? t('home.go_dashboard') : t('home.login')}
            </Button>
          </div>
        </div>
      </nav>

      {/* 文档主体：左侧 TOC + 右侧正文，两侧不再大片留白 */}
      <div className="max-w-7xl mx-auto px-6 md:px-12 py-10 grid grid-cols-1 lg:grid-cols-[220px_minmax(0,1fr)] xl:grid-cols-[240px_minmax(0,1fr)_200px] gap-x-12 gap-y-8">
        {/* 左侧目录（lg 以上才显示） */}
        <aside className="hidden lg:block">
          <div className="sticky top-24 max-h-[calc(100vh-7rem)] overflow-y-auto pr-2">
            <div className="flex items-center gap-2 mb-3 text-text-secondary">
              <BookOpen className="w-4 h-4" />
              <span className="text-xs font-semibold uppercaser">{t('docs.toc')}</span>
            </div>
            <nav className="space-y-0.5">
              {toc.map((item, idx) => (
                <Button
                  key={item.id}
                  className="w-full justify-start text-left"
                  size="sm"
                  variant={activeIndex === idx ? 'secondary' : 'ghost'}
                  onPress={() => scrollToIndex(idx)}
                >
                  {item.text}
                </Button>
              ))}
            </nav>
          </div>
        </aside>

        {/* pb-[70vh]：给最后一节预留至少 70% 视口高度的底部留白，
            否则末尾的 H2（如"常见问题"）滚不到视口顶部，TOC 锚点跳转看起来"没反应" */}
        <article className="min-w-0 pb-[70vh]">
          <div className="markdown-body">
          <ReactMarkdown
            remarkPlugins={[remarkGfm]}
            components={{
              // 标题：保留语义化标签 + scroll-margin（用于锚点跳转留出顶栏空间）
              h1: ({ children }) => (
                <h1 className="text-3xl font-bold mb-4 mt-2 scroll-mt-24">{children}</h1>
              ),
              h2: ({ children }) => {
                // 通过把 children 拍平成纯文本，再去 toc 里按 text 找下标 ——
                // 比"用计数器按顺序取号"健壮：不依赖渲染顺序、不受 React 并发模式
                // 中断重试影响、不受 markdown 局部 re-render 影响。
                const text = flattenText(children).trim();
                const idx = toc.findIndex((it) => it.text === text);
                const id = idx >= 0 ? `section-${idx + 1}` : undefined;
                return (
                  <h2
                    id={id}
                    ref={(el) => {
                      // 把 DOM 元素塞进 ref 数组，scrollToIndex / scroll-spy 直接用它
                      if (idx >= 0) h2RefsRef.current[idx] = el;
                    }}
                    className="text-xl font-bold mt-10 mb-3 pb-2 border-b border-border scroll-mt-24"
                  >
                    {children}
                  </h2>
                );
              },
              h3: ({ children }) => (
                <h3 className="text-base font-semibold mt-6 mb-2 text-text scroll-mt-24">{children}</h3>
              ),
              p: ({ children }) => (
                <p className="text-[14px] leading-relaxed text-text-secondary my-3">{children}</p>
              ),
              ul: ({ children }) => (
                <ul className="list-disc pl-6 my-3 space-y-1.5 text-[14px] text-text-secondary">
                  {children}
                </ul>
              ),
              ol: ({ children }) => (
                <ol className="list-decimal pl-6 my-3 space-y-1.5 text-[14px] text-text-secondary">
                  {children}
                </ol>
              ),
              li: ({ children }) => <li className="leading-relaxed">{children}</li>,
              a: ({ href, children }) => {
                const isExternal = !!href && /^https?:\/\//.test(href);
                return (
                  <HeroLink
                    href={href}
                    {...(isExternal ? { target: '_blank', rel: 'noopener noreferrer' } : {})}
                    className="text-[var(--ag-primary)] hover:underline"
                  >
                    {children}
                  </HeroLink>
                );
              },
              blockquote: ({ children }) => (
                <blockquote className="border-l-4 border-[var(--ag-primary)] bg-bg-elevated rounded-r-lg pl-4 pr-4 py-3 my-4 text-[13px] text-text-tertiary">
                  {children}
                </blockquote>
              ),
              hr: () => <hr className="my-8 border-border" />,
              table: ({ children }) => (
                <div className="ag-markdown-table table-root table-root--secondary my-4">
                  <div className="table__scroll-container">
                    <table className="table__content w-full text-[13px]">{children}</table>
                  </div>
                </div>
              ),
              thead: ({ children }) => <thead className="table__header">{children}</thead>,
              th: ({ children }) => (
                <th className="table__column px-3 py-2 text-left font-semibold text-text">
                  {children}
                </th>
              ),
              td: ({ children }) => (
                <td className="table__cell px-3 py-2 text-text-secondary">
                  {children}
                </td>
              ),
              // 行内 code 与代码块由 react-markdown 通过 inline 区分；
              // remark-gfm 下我们用 className 是否含 "language-" 来判定。
              code: ({ className, children, ...props }) => {
                const text = String(children ?? '').replace(/\n$/, '');
                const match = /language-(\w+)/.exec(className || '');
                if (!match) {
                  return (
                    <code
                      className="px-1.5 py-0.5 rounded bg-surface border border-glass-border text-[12px] font-mono text-text"
                      {...props}
                    >
                      {children}
                    </code>
                  );
                }
                return <CodeBlock language={match[1] ?? 'code'} code={text} />;
              },
              // 关键：让 pre 直接透传 children，避免在 CodeBlock 外面再包一层 pre 破坏样式
              pre: ({ children }) => <>{children}</>,
            }}
          >
              {markdown}
          </ReactMarkdown>
          </div>

          {/* 底部 CTA */}
          <div className="border-t border-border mt-12 pt-8 flex items-center justify-between">
            <span className="text-sm text-text-tertiary">{t('docs.cta_hint')}</span>
            <Button
              variant="primary"
              onPress={() => navigate({ to: isLoggedIn ? '/' : '/login' })}
            >
              {isLoggedIn ? t('home.go_dashboard') : t('home.login')}
              <ArrowRight className="w-4 h-4" />
            </Button>
          </div>
        </article>

        {/* 右侧占位列：xl 以上才有，纯粹用来视觉平衡 + 未来可放"On this page"等组件 */}
        <div className="hidden xl:block" />
      </div>
    </div>
  );
}

// ==================== TOC 抽取 ====================

interface TocItem {
  id: string;
  text: string;
}

/**
 * 从 markdown 源文件里抽取 H2 标题作为目录。
 *
 * - 跳过 ``` 围栏代码块内的内容，避免代码里的 `## ` 误判
 * - id 用 "section-N" 形式（N 从 1 开始），不依赖标题文本，
 *   这样 1) 不必处理 Chinese slug；2) 编辑文档改标题不会破坏锚点稳定性
 *   （只要小节顺序不变）。这个简单约定对内置文档够用了。
 */
/**
 * 把 React children（可能是字符串、数组、嵌套元素）拍平成纯文本。
 * 用于从 h2 component 的 children 里取标题文字，再去 toc 里反查下标。
 */
function flattenText(node: ReactNode): string {
  if (node == null || typeof node === 'boolean') return '';
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(flattenText).join('');
  if (typeof node === 'object' && 'props' in node) {
    // ReactElement 形态
    const props = (node as { props?: { children?: ReactNode } }).props;
    return flattenText(props?.children);
  }
  return '';
}

function extractH2(md: string): TocItem[] {
  const lines = md.split('\n');
  const items: TocItem[] = [];
  let inFence = false;
  let counter = 0;
  for (const line of lines) {
    if (line.startsWith('```')) {
      inFence = !inFence;
      continue;
    }
    if (inFence) continue;
    const m = /^##\s+(.+?)\s*$/.exec(line);
    if (m && m[1]) {
      counter++;
      items.push({ id: `section-${counter}`, text: m[1] });
    }
  }
  return items;
}

// ==================== Code block ====================

function CodeBlock({ code, language }: { code: string; language: string }): ReactNode {
  const copy = useClipboard();
  const [copied, setCopied] = useState(false);
  const handleCopy = () => {
    copy(code);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <div className="my-4 rounded-xl border border-glass-border bg-bg-elevated overflow-hidden">
      <div className="flex items-center justify-between px-4 py-2 border-b border-border bg-surface">
        <span className="text-[11px] uppercaser text-text-tertiary">{language}</span>
        <Button
          size="sm"
          variant="ghost"
          onPress={handleCopy}
        >
          {copied ? <Check className="w-3 h-3" /> : <Copy className="w-3 h-3" />}
          {copied ? 'Copied' : 'Copy'}
        </Button>
      </div>
      <pre className="px-4 py-3 overflow-x-auto text-[12px] font-mono text-text leading-relaxed">
        <code>{code}</code>
      </pre>
    </div>
  );
}
