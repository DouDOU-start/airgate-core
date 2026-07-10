import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

// 公告正文 Markdown 渲染：react-markdown 默认转义 HTML，天然防 XSS，勿引入 rehype-raw。
// 经 AnnouncementMarkdown.tsx 懒加载引入，勿直接 import 本文件（会把 markdown chunk 打回主链路）。
export default function AnnouncementMarkdownImpl({ content }: { content: string }) {
  return (
    <div className="min-w-0 break-words text-sm leading-6 text-text">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          h1: ({ children }) => <h1 className="mb-1.5 mt-3 text-lg font-bold first:mt-0">{children}</h1>,
          h2: ({ children }) => <h2 className="mb-1.5 mt-3 text-base font-bold first:mt-0">{children}</h2>,
          h3: ({ children }) => <h3 className="mb-1 mt-2 text-sm font-semibold first:mt-0">{children}</h3>,
          p: ({ children }) => <p className="my-1.5 first:mt-0 last:mb-0">{children}</p>,
          a: ({ href, children }) => (
            <a
              href={href}
              target="_blank"
              rel="noopener noreferrer"
              className="text-primary underline underline-offset-2"
            >
              {children}
            </a>
          ),
          ul: ({ children }) => <ul className="my-1.5 list-disc space-y-0.5 pl-5">{children}</ul>,
          ol: ({ children }) => <ol className="my-1.5 list-decimal space-y-0.5 pl-5">{children}</ol>,
          pre: ({ children }) => (
            <pre className="my-2 overflow-x-auto rounded-[var(--radius)] border border-border bg-bg p-3 text-xs">
              {children}
            </pre>
          ),
          code: ({ children, ...props }) => (
            <code className="rounded bg-bg px-1 py-0.5 font-mono text-[0.85em]" {...props}>
              {children}
            </code>
          ),
          blockquote: ({ children }) => (
            <blockquote className="my-2 border-l-2 border-border pl-3 text-text-secondary">
              {children}
            </blockquote>
          ),
          table: ({ children }) => (
            <div className="my-2 overflow-x-auto">
              <table className="w-full border-collapse text-xs">{children}</table>
            </div>
          ),
          th: ({ children }) => (
            <th className="border border-border bg-bg px-2 py-1 text-left font-semibold">{children}</th>
          ),
          td: ({ children }) => <td className="border border-border px-2 py-1">{children}</td>,
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
