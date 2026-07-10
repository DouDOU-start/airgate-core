import { lazy, Suspense } from 'react';

// react-markdown + remark-gfm 体量较大（gzip ~47KB），而公告铃铛/弹窗挂在 AppShell 上
// 每个登录会话都会渲染。这里做懒加载边界，只在公告正文真正渲染时才拉 markdown chunk。
const AnnouncementMarkdownImpl = lazy(() => import('./AnnouncementMarkdownImpl'));

export function AnnouncementMarkdown({ content }: { content: string }) {
  return (
    <Suspense fallback={null}>
      <AnnouncementMarkdownImpl content={content} />
    </Suspense>
  );
}
